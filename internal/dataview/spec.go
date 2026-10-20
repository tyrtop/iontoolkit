package dataview

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
	"tyrtop.com/iontk/internal/sase/ioncli"
)

//go:embed builtin.yaml
var builtinFS embed.FS

type Set struct {
	Views []View `yaml:"views"`
}

type View struct {
	Name     string            `yaml:"name"`
	Fetch    map[string]string `yaml:"fetch"`
	Sections []Section         `yaml:"sections"`
	Compare  *Compare          `yaml:"compare"`
}

type Compare struct {
	When     string            `yaml:"when"`
	Fetch    map[string]string `yaml:"fetch"`
	Commands map[string]string `yaml:"commands"`
	Rows     []CompareRow      `yaml:"rows"`
}

type CompareRow struct {
	Label  string `yaml:"label"`
	Portal string `yaml:"portal"`
	Device string `yaml:"device"`
}

type Section struct {
	Title string `yaml:"title"`
	When  string `yaml:"when"`
	Rows  []Row  `yaml:"rows"`

	ForEach  string            `yaml:"for_each"`
	Fetch    map[string]string `yaml:"fetch"`
	MaxItems int               `yaml:"max_items"`
	Columns  []Column          `yaml:"columns"`
	Empty    string            `yaml:"empty"`
}

type Row struct {
	Label string `yaml:"label"`
	Value string `yaml:"value"`
}

type Column struct {
	Header string `yaml:"header"`
	Value  string `yaml:"value"`
	Width  int    `yaml:"width"`
}

const defaultMaxItems = 60

func Builtin() (*Set, error) {
	b, err := builtinFS.ReadFile("builtin.yaml")
	if err != nil {
		return nil, fmt.Errorf("read built-in views: %w", err)
	}
	s, err := Parse(b)
	if err != nil {
		return nil, fmt.Errorf("built-in views: %w", err)
	}
	return s, nil
}

const FileName = "views.yaml"

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

	for _, v := range user.Views {
		if i := set.index(v.Name); i >= 0 {
			set.Views[i] = v
			continue
		}
		set.Views = append(set.Views, v)
	}
	return set, nil
}

func (s *Set) index(name string) int {
	for i, v := range s.Views {
		if strings.EqualFold(v.Name, name) {
			return i
		}
	}
	return -1
}

func (s *Set) View(name string) (View, bool) {
	if i := s.index(name); i >= 0 {
		return s.Views[i], true
	}
	return View{}, false
}

func (s *Set) Names() []string {
	out := make([]string, 0, len(s.Views))
	for _, v := range s.Views {
		out = append(out, v.Name)
	}
	return out
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

func (s *Set) normalize() error {
	if len(s.Views) == 0 {
		return fmt.Errorf("no views: a file with none in it looks like the mode being broken")
	}
	seen := map[string]bool{}
	for i := range s.Views {
		v := &s.Views[i]
		if v.Name == "" {
			return fmt.Errorf("view %d has no name", i+1)
		}
		if seen[strings.ToLower(v.Name)] {
			return fmt.Errorf("two views are called %q", v.Name)
		}
		seen[strings.ToLower(v.Name)] = true

		if len(v.Sections) == 0 {
			return fmt.Errorf("view %q has no sections", v.Name)
		}
		for name, p := range v.Fetch {
			if err := checkPath(p); err != nil {
				return fmt.Errorf("view %q fetch %q: %w", v.Name, name, err)
			}
		}
		for j := range v.Sections {
			if err := v.Sections[j].normalize(v.Name); err != nil {
				return err
			}
		}
		if err := v.Compare.normalize(v.Name); err != nil {
			return err
		}
	}
	return nil
}

func (sec *Section) normalize(view string) error {
	where := fmt.Sprintf("view %q section %q", view, sec.Title)
	switch {
	case sec.ForEach != "" && len(sec.Rows) > 0:
		return fmt.Errorf("%s has both rows and for_each; a section is one or the other", where)
	case sec.ForEach == "" && len(sec.Columns) > 0:
		return fmt.Errorf("%s has columns but no for_each to draw them from", where)
	case sec.ForEach != "" && len(sec.Columns) == 0:
		return fmt.Errorf("%s iterates %s but declares no columns", where, sec.ForEach)
	case sec.ForEach == "" && len(sec.Rows) == 0:
		return fmt.Errorf("%s is empty", where)
	}
	for name, p := range sec.Fetch {
		if sec.ForEach == "" {
			return fmt.Errorf("%s fetches %q but has no for_each; per-element fetches belong on the view", where, name)
		}
		if err := checkPath(p); err != nil {
			return fmt.Errorf("%s fetch %q: %w", where, name, err)
		}
	}
	if sec.MaxItems <= 0 {
		sec.MaxItems = defaultMaxItems
	}
	return nil
}

func (c *Compare) normalize(view string) error {
	if c == nil {
		return nil
	}
	if len(c.Rows) == 0 {
		return fmt.Errorf("view %q compares nothing: a compare block with no rows is a dial for no answer", view)
	}
	if len(c.Commands) == 0 {
		return fmt.Errorf("view %q compares without asking the device anything", view)
	}
	cmds := make([]string, 0, len(c.Commands))
	for _, name := range sortedKeys(c.Commands) {
		cmds = append(cmds, c.Commands[name])
	}
	if err := ioncli.CheckReadOnly(cmds); err != nil {
		return fmt.Errorf("view %q compare: %w", view, err)
	}
	for i, r := range c.Rows {
		switch {
		case r.Label == "":
			return fmt.Errorf("view %q compare row %d has no label", view, i+1)
		case r.Portal == "" || r.Device == "":
			return fmt.Errorf("view %q compare row %q needs both a portal and a device side", view, r.Label)
		}
	}
	for name, p := range c.Fetch {
		if err := checkPath(p); err != nil {
			return fmt.Errorf("view %q compare fetch %q: %w", view, name, err)
		}
	}
	return nil
}

// a read of the SASE SD-WAN API and nothing else: no host of its own, no
// method, no escaping /sdwan with ".."
func checkPath(p string) error {
	if p == "" {
		return fmt.Errorf("is empty")
	}
	if strings.Contains(p, "://") || strings.HasPrefix(p, "//") {
		return fmt.Errorf("%q names a host; a view gives a path like /sdwan/v3.2/api/elements/{{ .element_id }}", p)
	}
	if !strings.HasPrefix(p, "/sdwan/") {
		return fmt.Errorf("%q does not start with /sdwan/", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return fmt.Errorf("%q walks up out of /sdwan", p)
		}
	}
	return nil
}
