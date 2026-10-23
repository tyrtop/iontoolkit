package column

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
	"tyrtop.com/iontk/internal/tmpl"
	"tyrtop.com/iontk/internal/ui"
)

const FileName = "columns.yaml"

//go:embed elements.yaml
var builtinYAML []byte

func Builtin() (*Set, error) {
	set, err := Parse(builtinYAML)
	if err != nil {
		return nil, fmt.Errorf("built-in columns: %w", err)
	}
	return set, nil
}

func BuiltinYAML() string { return string(builtinYAML) }

func (s *Set) Source(name string) string {
	for _, c := range s.Columns {
		if !c.Fleet() {
			continue
		}
		if strings.HasSuffix(c.Fetch, "/"+name) {
			return c.Fetch
		}
	}
	return ""
}

type Set struct {
	Kind    string            `yaml:"kind"`
	Name    string            `yaml:"name"`
	Keys    map[string]string `yaml:"keys"`
	Actions map[string]any    `yaml:"actions"`

	Columns []Spec `yaml:"columns"`
}

type Spec struct {
	Name   string `yaml:"name"`
	Header string `yaml:"header"`
	Min    int    `yaml:"min"`
	Max    int    `yaml:"max"`
	Flex   *bool  `yaml:"flex"`
	Mark   *bool  `yaml:"mark"`
	Go     string `yaml:"go"`
	When   string `yaml:"when"`
	On     string `yaml:"on"`
	From   string `yaml:"from"`
	Value  string `yaml:"value"`
	Style  []Rule `yaml:"style"`

	Fetch string `yaml:"fetch"`
	Bulk  string `yaml:"bulk"`
	Key   string `yaml:"key"`

	Template *tmpl.T
	Shown    *tmpl.T
	Rules    []CompiledRule
}

type Rule struct {
	When string `yaml:"when"`
	Role string `yaml:"role"`
}

type CompiledRule struct {
	When *tmpl.T
	Role string
}

const (
	FromElement = "element"
	FromSite    = "site"
)

const (
	DefaultKey  = "element_id"
	FleetKey    = "id"
	FleetOn     = "id"
	ElementsSrc = "elements"
	SitesSrc    = "sites"
)

func (c Spec) Fetches() bool { return c.Fetch != "" }

func (c Spec) PerElement() bool { return c.Fetch != "" && strings.Contains(c.Fetch, "{") }

func (c Spec) Fleet() bool { return c.Fetch != "" && !strings.Contains(c.Fetch, "{") }

func checkPath(p string) error { return tmpl.CheckPath(ids.Replace(p)) }

var ids = strings.NewReplacer("{", "", "}", "")

var specKeys = []string{"name", "header", "min", "max", "flex", "mark", "go",
	"when", "on", "from", "value", "style", "fetch", "bulk", "key"}

func (s *Spec) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		return n.Decode(&s.Name)
	}
	if err := knownKeys(n, specKeys); err != nil {
		return err
	}
	type raw Spec
	var r raw
	if err := n.Decode(&r); err != nil {
		return err
	}
	*s = Spec(r)
	return nil
}

func (r *Rule) UnmarshalYAML(n *yaml.Node) error {
	if err := knownKeys(n, []string{"when", "role"}); err != nil {
		return err
	}
	type raw Rule
	var got raw
	if err := n.Decode(&got); err != nil {
		return err
	}
	*r = Rule(got)
	return nil
}

// KnownFields does not reach a type that unmarshals itself
func knownKeys(n *yaml.Node, allowed []string) error {
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i].Value
		if !slices.Contains(allowed, key) {
			return fmt.Errorf("field %s not found; a column takes %s",
				key, strings.Join(allowed, ", "))
		}
	}
	return nil
}

func Load(path string) (*Set, error) {
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	set, err := Parse(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
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

func (s *Set) normalize() error {
	if len(s.Columns) == 0 {
		return fmt.Errorf("no columns: a file with none in it looks like the element list being broken")
	}
	seen := map[string]bool{}
	marks := 0
	for i := range s.Columns {
		c := &s.Columns[i]
		if err := c.normalize(i); err != nil {
			return err
		}
		if seen[c.Name] {
			return fmt.Errorf("two columns are called %q", c.Name)
		}
		seen[c.Name] = true
		if c.Mark != nil && *c.Mark {
			marks++
		}
	}
	if marks > 1 {
		return fmt.Errorf("%d columns carry the selection mark; it goes on one", marks)
	}
	return nil
}

func (c *Spec) normalize(at int) error {
	if c.Name == "" {
		return fmt.Errorf("column %d has no name", at+1)
	}
	where := fmt.Sprintf("column %q", c.Name)

	switch {
	case c.Fetch == "" && (c.Bulk != "" || c.Key != ""):
		return fmt.Errorf("%s has a bulk: or a key: and no fetch: to fall back on", where)
	case c.Fetch != "" && c.Value == "":
		return fmt.Errorf("%s fetches and says no value", where)
	case c.Fetch != "" && c.From != "":
		return fmt.Errorf("%s fetches, so its value reads the answer; drop the from:", where)
	case c.On != "" && c.Fetch == "":
		return fmt.Errorf("%s has an on: and no fetch: for it to join", where)
	case c.Bulk != "" && c.Fleet():
		return fmt.Errorf("%s fetches once for the fleet, so a bulk: query adds nothing", where)
	case c.Fetch != "" && c.Go != "":
		return fmt.Errorf("%s fetches and also names a go:", where)
	case c.Go != "" && c.Value != "":
		return fmt.Errorf("%s has a value and a go:; it is one or the other", where)
	case c.Go != "" && c.From != "":
		return fmt.Errorf("%s reads %q and also names a go:; iontk works that one out itself", where, c.From)
	case c.From != "" && c.From != FromElement && c.From != FromSite:
		return fmt.Errorf("%s reads %q; a column reads %q or %q", where, c.From, FromElement, FromSite)
	case c.From == FromSite && c.Value == "":
		return fmt.Errorf("%s reads the site but says no value", where)
	case c.Min < 0 || c.Max < 0:
		return fmt.Errorf("%s has a negative width", where)
	case c.Max > 0 && c.Min > c.Max:
		return fmt.Errorf("%s has min %d above max %d", where, c.Min, c.Max)
	}

	if c.Value != "" {
		t, err := tmpl.Compile(c.Value)
		if err != nil {
			return fmt.Errorf("%s value: %w", where, err)
		}
		c.Template = t
	}
	if c.Fetch != "" {
		if err := checkPath(c.Fetch); err != nil {
			return fmt.Errorf("%s fetch: %w", where, err)
		}
		c.Bulk = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(c.Bulk), "POST"))
		if c.Bulk != "" {
			if err := checkPath(c.Bulk); err != nil {
				return fmt.Errorf("%s bulk: %w", where, err)
			}
		}
		switch {
		case c.PerElement():
			if c.Key == "" {
				c.Key = DefaultKey
			}
		default:
			if c.Key == "" {
				c.Key = FleetKey
			}
			if c.On == "" {
				c.On = FleetOn
			}
		}
	}
	if c.When != "" {
		t, err := tmpl.Compile(c.When)
		if err != nil {
			return fmt.Errorf("%s when: %w", where, err)
		}
		c.Shown = t
	}
	for _, r := range c.Style {
		if _, ok := ui.StyleNamed(r.Role); !ok {
			return fmt.Errorf("%s is styled %q, which is not one of %s", where, r.Role,
				strings.Join(ui.StyleNames(), ", "))
		}
		cr := CompiledRule{Role: r.Role}
		if r.When != "" {
			t, err := tmpl.Compile(r.When)
			if err != nil {
				return fmt.Errorf("%s style %q: %w", where, r.Role, err)
			}
			cr.When = t
		}
		c.Rules = append(c.Rules, cr)
	}
	return nil
}

func (c Spec) Subject() string {
	if c.From != "" {
		return c.From
	}
	return FromElement
}
