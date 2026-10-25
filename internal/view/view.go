package view

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const DirName = "views"

const (
	KindList = "list"
	KindData = "data"
	KindMenu = "menu"
)

func Kinds() []string { return []string{KindList, KindData, KindMenu} }

type File struct {
	Kind   string
	Name   string
	Source string
	Bytes  []byte
}

type envelope struct {
	Kind string `yaml:"kind"`
	Name string `yaml:"name"`
}

func Load(path string) ([]File, error) {
	if path == "" {
		return nil, nil
	}
	names, err := files(path)
	if err != nil {
		return nil, err
	}

	var out []File
	var bad []string
	for _, name := range names {
		b, rerr := os.ReadFile(name)
		if rerr != nil {
			bad = append(bad, fmt.Sprintf("%s: %s", filepath.Base(name), rerr))
			continue
		}
		var env envelope
		if perr := yaml.Unmarshal(b, &env); perr != nil {
			bad = append(bad, fmt.Sprintf("%s: %s", filepath.Base(name), perr))
			continue
		}
		if env.Kind == "" {
			bad = append(bad, fmt.Sprintf("%s says no kind; it is one of %s",
				filepath.Base(name), strings.Join(Kinds(), ", ")))
			continue
		}
		if !known(env.Kind) {
			bad = append(bad, fmt.Sprintf("%s is a %q, which iontk does not draw; the kinds are %s",
				filepath.Base(name), env.Kind, strings.Join(Kinds(), ", ")))
			continue
		}
		if env.Name == "" {
			env.Name = titleFrom(name)
		}
		out = append(out, File{Kind: env.Kind, Name: env.Name, Source: name, Bytes: b})
	}
	if len(bad) > 0 {
		return out, fmt.Errorf("%s", strings.Join(bad, "; "))
	}
	return out, nil
}

func CheckEnvelope(b []byte, want string) error {
	var env envelope
	if err := yaml.Unmarshal(b, &env); err != nil {
		return err
	}
	switch {
	case env.Kind == "":
		return fmt.Errorf("a file under %s/ says what it is: kind: %s", DirName, want)
	case env.Kind != want:
		return fmt.Errorf("this file is a %q and the screen it draws is a %q", env.Kind, want)
	}
	return nil
}

func KindOf(b []byte) (string, error) {
	var env envelope
	err := yaml.Unmarshal(b, &env)
	return env.Kind, err
}

func (f File) Is(kind string) bool { return f.Kind == kind }

func known(kind string) bool {
	for _, k := range Kinds() {
		if k == kind {
			return true
		}
	}
	return false
}

func files(path string) ([]string, error) {
	if IsFile(path) {
		return []string{path}, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !isYAML(e.Name()) {
			continue
		}
		out = append(out, filepath.Join(path, e.Name()))
	}
	sort.Strings(out)
	return out, nil
}

func IsFile(path string) bool {
	if fi, err := os.Stat(path); err == nil {
		return !fi.IsDir()
	}
	return isYAML(path)
}

func isYAML(name string) bool {
	return strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")
}

func titleFrom(path string) string {
	base := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(path), ".yaml"), ".yml")
	words := strings.FieldsFunc(base, func(r rune) bool { return r == '-' || r == '_' || r == ' ' })
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

func FileName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteRune('-')
		}
	}
	if b.Len() == 0 {
		return "view.yaml"
	}
	return b.String() + ".yaml"
}
