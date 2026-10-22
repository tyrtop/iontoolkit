package tutorial

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

type Page struct {
	Name string   `yaml:"name"`
	Show *bool    `yaml:"show"`
	Box  bool     `yaml:"box"`
	Text []string `yaml:"text"`
	Keys []string `yaml:"keys"`
	Stop string   `yaml:"stop"`
	Go   string   `yaml:"go"`
}

const (
	Typing      = "typing"
	DefaultStop = "enter"
)

func (p Page) StopKey() string {
	if p.Stop == "" {
		return DefaultStop
	}
	return p.Stop
}

type Set struct {
	Name  string `yaml:"name"`
	Pages []Page `yaml:"pages"`

	Source string `yaml:"-"`
}

var Places = []string{
	"menu",
	"elements",
	"settings",
	"theme-section",
	"macros",
}

//go:embed builtin.yaml
var builtinYAML []byte

const (
	FileName = "tutorial.yaml"
	DirName  = "tutorials"
)

func Flows(path string) ([]*Set, error) {
	var out []*Set
	var bad []string

	if path != "" {
		files, err := flowFiles(path)
		if err != nil {
			bad = append(bad, fmt.Sprintf("read %s: %s", path, err))
		}
		for _, f := range files {
			b, rerr := os.ReadFile(f)
			if rerr != nil {
				bad = append(bad, fmt.Sprintf("%s: %s", filepath.Base(f), rerr))
				continue
			}
			set, perr := Parse(b, filepath.Base(f))
			if perr != nil {
				bad = append(bad, perr.Error())
				continue
			}
			if set.Name == "" {
				set.Name = titleFrom(f)
			}
			set.Source = f
			if i := indexOf(out, set.Name); i >= 0 {
				out[i] = set
				continue
			}
			out = append(out, set)
		}
	}

	if len(out) == 0 {
		one, err := Builtin()
		if err != nil {
			return nil, err
		}
		out = append(out, one)
	}
	if len(bad) > 0 {
		return out, fmt.Errorf("%s", strings.Join(bad, "; "))
	}
	return out, nil
}

func indexOf(sets []*Set, name string) int {
	for i, s := range sets {
		if strings.EqualFold(s.Name, name) {
			return i
		}
	}
	return -1
}

func flowFiles(path string) ([]string, error) {
	if pathIsFile(path) {
		return []string{path}, nil
	}

	var out []string
	legacy := filepath.Join(filepath.Dir(strings.TrimSuffix(path, string(filepath.Separator))), FileName)
	if fi, err := os.Stat(legacy); err == nil && !fi.IsDir() {
		out = append(out, legacy)
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".yaml", ".yml":
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	for _, n := range names {
		out = append(out, filepath.Join(path, n))
	}
	return out, nil
}

func pathIsFile(path string) bool {
	if fi, err := os.Stat(path); err == nil {
		return !fi.IsDir()
	}
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yaml" || ext == ".yml"
}

func titleFrom(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	words := strings.FieldsFunc(base, func(r rune) bool { return r == '-' || r == '_' || r == ' ' })
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	if len(words) == 0 {
		return base
	}
	return strings.Join(words, " ")
}

func Builtin() (*Set, error) { return Parse(builtinYAML, "builtin.yaml") }

func BuiltinYAML() string { return string(builtinYAML) }

func FlowFileName(name string) string {
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
		return "tutorial.yaml"
	}
	return b.String() + ".yaml"
}

func Parse(b []byte, name string) (*Set, error) {
	var s Set
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil && err.Error() != "EOF" {
		return nil, fmt.Errorf("parse %s: %w", name, err)
	}
	if err := s.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return &s, nil
}

func (p Page) Visible() bool { return p.Show == nil || *p.Show }

func (p Page) Label(n int) string {
	if p.Name != "" {
		return fmt.Sprintf("page %d (%s)", n+1, p.Name)
	}
	return fmt.Sprintf("page %d", n+1)
}

func (s *Set) Shown() []Page {
	var out []Page
	for _, p := range s.Pages {
		if p.Visible() {
			out = append(out, p)
		}
	}
	return out
}

func (s *Set) validate() error {
	names := map[string]bool{}
	for i, p := range s.Pages {
		where := p.Label(i)

		if p.Name != "" {
			if names[p.Name] {
				return fmt.Errorf("%s: two pages are named %q", where, p.Name)
			}
			names[p.Name] = true
		}
		if len(p.Text) == 0 {
			return fmt.Errorf("%s: no text, so it would show an empty bar", where)
		}
		if p.Box && len(p.Keys) > 0 {
			return fmt.Errorf("%s: a box holds every key while it is up, so keys: would do nothing; drop one of the two", where)
		}
		for _, k := range append(p.Keys, p.Stop) {
			if k != "" && strings.ContainsAny(k, " \t") {
				return fmt.Errorf("%s: %q is not a key; name them the way the footer does -- enter, esc, space, alt+m, / -- or %s for any character",
					where, k, Typing)
			}
		}
		if p.Go != "" && !has(Places, p.Go) {
			return fmt.Errorf("%s: unknown go: %q; one of %s",
				where, p.Go, strings.Join(Places, ", "))
		}
	}
	return nil
}

func has(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
