package tui

import (
	_ "embed"
	"fmt"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
	"tyrtop.com/iontk/internal/tmpl"
	"tyrtop.com/iontk/internal/view"
)

//go:embed menu.yaml
var menuYAML []byte

var builtinMenu = sync.OnceValues(func() ([]menuEntry, error) { return parseMenu(menuYAML, nil) })

var menuActions = []struct {
	name   string
	label  string
	action menuAction
}{
	{"elements", "Elements", actElements},
	{"service-accounts", "Service Accounts", actSwitch},
	{"sign-in", "Sign In with Browser", actSignIn},
	{"refresh-token", "Refresh Browser Token", actRefreshToken},
	{"macros", "Macros", actMacros},
	{"workflows", "Workflows", actWorkflows},
	{"settings", "Settings", actSettings},
	{"quit", "Quit", actQuit},
}

type menuSpec struct {
	Kind    string       `yaml:"kind"`
	Name    string       `yaml:"name"`
	Entries []menuEntryS `yaml:"entries"`

	Keys    map[string]string `yaml:"keys"`
	Actions map[string]any    `yaml:"actions"`
}

type menuEntryS struct {
	Name  string `yaml:"name"`
	Label string `yaml:"label"`
	When  string `yaml:"when"`
}

func (e *menuEntryS) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		return n.Decode(&e.Name)
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if k := n.Content[i].Value; k != "name" && k != "label" && k != "when" {
			return fmt.Errorf("field %s not found; an entry takes name, label, when", k)
		}
	}
	type raw menuEntryS
	var r raw
	if err := n.Decode(&r); err != nil {
		return err
	}
	*e = menuEntryS(r)
	return nil
}

func parseMenu(b []byte, runs func(string) bool) ([]menuEntry, error) {
	var spec menuSpec
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&spec); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if len(spec.Entries) == 0 {
		return nil, fmt.Errorf("no entries: a menu with none in it draws nothing")
	}

	out := make([]menuEntry, 0, len(spec.Entries))
	seen := map[string]bool{}
	for i, e := range spec.Entries {
		if e.Name == "" {
			return nil, fmt.Errorf("entry %d has no name", i+1)
		}
		if seen[e.Name+"\x00"+e.When] {
			return nil, fmt.Errorf("two entries are called %q", e.Name)
		}
		seen[e.Name+"\x00"+e.When] = true
		var when *tmpl.T
		if e.When != "" {
			t, err := tmpl.Compile(e.When)
			if err != nil {
				return nil, fmt.Errorf("entry %q when: %w", e.Name, err)
			}
			when = t
		}
		at := menuActionNamed(e.Name)
		if at < 0 && runs != nil && runs(e.Name) {
			label := e.Label
			if label == "" {
				label = e.Name
			}
			out = append(out, menuEntry{label: label, action: actCommand, command: e.Name, when: when})
			continue
		}
		if at < 0 {
			return nil, fmt.Errorf("entry %q is not one iontk runs (%s, or a runtime command: iontk help lists them)",
				e.Name, strings.Join(menuActionNames(), ", "))
		}

		label := menuActions[at].label
		if e.Label != "" {
			label = e.Label
		}
		out = append(out, menuEntry{label: label, action: menuActions[at].action, when: when})
	}
	if !hasAction(out, actQuit) {
		return nil, fmt.Errorf("no quit entry: add quit, so the menu says how to leave")
	}
	return out, nil
}

func menuActionNamed(name string) int {
	for i, a := range menuActions {
		if a.name == name {
			return i
		}
	}
	return -1
}

func menuActionName(want menuAction) string {
	for _, a := range menuActions {
		if a.action == want {
			return a.name
		}
	}
	return ""
}

func menuActionNames() []string {
	out := make([]string, 0, len(menuActions))
	for _, a := range menuActions {
		out = append(out, a.name)
	}
	return out
}

func hasAction(entries []menuEntry, want menuAction) bool {
	for _, e := range entries {
		if e.action == want {
			return true
		}
	}
	return false
}

func menuSeed(entries []menuEntry) string {
	var b strings.Builder
	b.WriteString("# The main menu, in order. A name on its own is the entry as iontk words\n")
	b.WriteString("# it; give it a label of your own to change what it says.\n\n")
	b.WriteString("kind: " + view.KindMenu + "\nentries:\n")
	for _, e := range entries {
		name := e.command
		if e.action != actCommand {
			name = menuActionName(e.action)
		}
		switch {
		case e.when != nil:
			b.WriteString(fmt.Sprintf("  - {name: %q, label: %q, when: %q}\n", name, e.label, e.when.Text()))
		case e.action == actCommand:
			b.WriteString(fmt.Sprintf("  - {name: %q, label: %q}\n", name, e.label))
		default:
			b.WriteString("  - " + name + "\n")
		}
	}
	return b.String()
}
