package write

import (
	"embed"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
	"tyrtop.com/iontk/internal/endpoint"
)

//go:embed builtin.yaml
var builtinFS embed.FS

const FileName = "writes.yaml"

type Set struct {
	Writes []Spec `yaml:"writes"`
}

type Spec struct {
	Name      string              `yaml:"name"`
	Title     string              `yaml:"title,omitempty"`
	Endpoints []endpoint.Endpoint `yaml:"endpoints"`
	List      string              `yaml:"list"`
	Item      string              `yaml:"item"`
	Label     string              `yaml:"label,omitempty"`
	Match     string              `yaml:"match,omitempty"`
	Fields    []Field             `yaml:"fields"`

	Verified bool `yaml:"verified,omitempty"`

	Source string            `yaml:"-"`
	list   endpoint.Endpoint `yaml:"-"`
	item   endpoint.Endpoint `yaml:"-"`
}

type Field struct {
	Key   string `yaml:"key"`
	Label string `yaml:"label,omitempty"`
	Type  string `yaml:"type,omitempty"`
}

const (
	TypeString = "string"
	TypeBool   = "bool"
	TypeInt    = "int"
	TypeList   = "list"
)

func BuiltinYAML() string {
	b, err := builtinFS.ReadFile("builtin.yaml")
	if err != nil {
		return ""
	}
	return string(b)
}

func Builtin() (*Set, error) {
	b, err := builtinFS.ReadFile("builtin.yaml")
	if err != nil {
		return nil, fmt.Errorf("read built-in writes: %w", err)
	}
	s, err := Parse(b)
	if err != nil {
		return nil, fmt.Errorf("built-in writes: %w", err)
	}
	return s, nil
}

func Load(path string) (*Set, error) {
	set, err := Builtin()
	if err != nil {
		return nil, err
	}
	if path == "" {
		return set, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return set, nil
		}
		return set, fmt.Errorf("read %s: %w", path, err)
	}
	user, err := Parse(b)
	if err != nil {
		return set, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	for _, s := range user.Writes {
		s.Source = path
		if i := set.index(s.Name); i >= 0 {
			set.Writes[i] = s
			continue
		}
		set.Writes = append(set.Writes, s)
	}
	return set, nil
}

func Parse(b []byte) (*Set, error) {
	var set Set
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&set); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if err := set.normalize(); err != nil {
		return nil, err
	}
	return &set, nil
}

func (s *Set) index(name string) int {
	for i, w := range s.Writes {
		if strings.EqualFold(w.Name, name) {
			return i
		}
	}
	return -1
}

func (s *Set) Spec(name string) (Spec, bool) {
	if i := s.index(name); i >= 0 {
		return s.Writes[i], true
	}
	return Spec{}, false
}

func (s *Set) Names() []string {
	out := make([]string, 0, len(s.Writes))
	for _, w := range s.Writes {
		out = append(out, w.Name)
	}
	return out
}

func (s *Set) normalize() error {
	seen := map[string]bool{}
	for i := range s.Writes {
		w := &s.Writes[i]
		if w.Name == "" {
			return fmt.Errorf("write %d has no name", i+1)
		}
		if seen[strings.ToLower(w.Name)] {
			return fmt.Errorf("two writes are called %q", w.Name)
		}
		seen[strings.ToLower(w.Name)] = true
		if err := w.normalize(); err != nil {
			return fmt.Errorf("write %q: %w", w.Name, err)
		}
	}
	return nil
}

func (w *Spec) normalize() error {
	calls, err := endpoint.Build(w.Name, "", nil, w.Endpoints)
	if err != nil {
		return err
	}
	if w.list, err = calls.SDWAN(w.List); err != nil {
		return fmt.Errorf("list: %w", err)
	}
	if w.item, err = calls.SDWAN(w.Item); err != nil {
		return fmt.Errorf("item: %w", err)
	}
	switch w.item.Method {
	case http.MethodPut, http.MethodPatch:
	default:
		return fmt.Errorf("item method %s: an edit sends the record back with PUT or PATCH", w.item.Method)
	}
	if w.Title == "" {
		w.Title = w.Name
	}
	if w.Label == "" {
		w.Label = "{{ .item.name }}"
	}
	if w.Match == "" {
		w.Match = "name"
	}
	if len(w.Fields) == 0 {
		return fmt.Errorf("names no fields, so there is nothing to edit")
	}
	keys := map[string]bool{}
	for i := range w.Fields {
		f := &w.Fields[i]
		if f.Key == "" {
			return fmt.Errorf("field %d has no key", i+1)
		}
		if keys[f.Key] {
			return fmt.Errorf("field %q is listed twice", f.Key)
		}
		keys[f.Key] = true
		if f.Label == "" {
			f.Label = f.Key
		}
		switch f.Type {
		case "":
			f.Type = TypeString
		case TypeString, TypeBool, TypeInt, TypeList:
		default:
			return fmt.Errorf("field %q has type %q; it is one of string, bool, int, list", f.Key, f.Type)
		}
	}
	return nil
}

func (w Spec) Field(key string) (Field, bool) {
	for _, f := range w.Fields {
		if f.Key == key {
			return f, true
		}
	}
	return Field{}, false
}

func (w Spec) FieldKeys() []string {
	out := make([]string, 0, len(w.Fields))
	for _, f := range w.Fields {
		out = append(out, f.Key)
	}
	return out
}
