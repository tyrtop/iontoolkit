package tui

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"gopkg.in/yaml.v3"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/tutorial"
	"tyrtop.com/iontk/internal/ui"
	"tyrtop.com/iontk/internal/view"
)

const (
	KeybindsFileName = "keybinds.yaml"
	ActionsFileName  = "actions.yaml"
	unbound          = "none"
)

func KeybindsPath(st *config.Store) string {
	if app.KeybindsFile != "" {
		return app.KeybindsFile
	}
	if st == nil {
		return ""
	}
	return filepath.Join(st.Root, KeybindsFileName)
}

func ActionsPath(st *config.Store) string {
	if st == nil {
		return ""
	}
	return filepath.Join(st.Root, ActionsFileName)
}

type keymap struct {
	bound   map[string]map[string]string
	actions map[string][]actionStep
	lua     func(name string) bool
	runs    func(line string) bool
	from    string
}

type keybindsFile struct {
	Kind    string                       `yaml:"kind"`
	Name    string                       `yaml:"name"`
	Keys    map[string]string            `yaml:"keys"`
	Screens map[string]map[string]string `yaml:"screens"`
	Actions map[string][]actionStep      `yaml:"actions"`
}

type actionStep struct {
	Do     string            `yaml:"do"`
	Goto   string            `yaml:"goto"`
	Set    map[string]string `yaml:"set"`
	Status string            `yaml:"status"`
}

func actionVerbs() []string { return []string{"do", "goto", "set", "status"} }

func (s *actionStep) UnmarshalYAML(n *yaml.Node) error {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if k := n.Content[i].Value; !contains(actionVerbs(), k) {
			return fmt.Errorf("%s is not a verb iontk has; they are %s",
				k, strings.Join(actionVerbs(), ", "))
		}
	}
	type raw actionStep
	var r raw
	if err := n.Decode(&r); err != nil {
		return err
	}
	*s = actionStep(r)
	return nil
}

//go:embed keys.yaml
var keysYAML []byte

func defaultKeymap() keymap {
	km := keymap{bound: map[string]map[string]string{}, actions: map[string][]actionStep{}}
	if err := km.readBytes(keysYAML, scrAny, "keys.yaml"); err != nil {
		panic(err)
	}
	km.from = ""
	return km
}

var shippedOrder = sync.OnceValue(func() map[string][]string {
	out := map[string][]string{}
	var doc yaml.Node
	if yaml.Unmarshal(keysYAML, &doc) != nil || len(doc.Content) == 0 {
		return out
	}
	add := func(screen string, pairs *yaml.Node) {
		for i := 0; i+1 < len(pairs.Content); i += 2 {
			at := screen + "\x00" + pairs.Content[i+1].Value
			out[at] = append(out[at], pairs.Content[i].Value)
		}
	}
	top := doc.Content[0]
	for i := 0; i+1 < len(top.Content); i += 2 {
		switch top.Content[i].Value {
		case "keys":
			add(scrAny, top.Content[i+1])
		case "screens":
			list := top.Content[i+1]
			for j := 0; j+1 < len(list.Content); j += 2 {
				add(list.Content[j].Value, list.Content[j+1])
			}
		}
	}
	return out
})

func (km *keymap) bind(screen, key, action string) {
	if km.bound[screen] == nil {
		km.bound[screen] = map[string]string{}
	}
	km.bound[screen][key] = action
}

func (km keymap) action(screen, key string) (string, bool) {
	for _, s := range []string{screen, scrAny} {
		if name, ok := km.bound[s][key]; ok {
			if name == unbound {
				return "", false
			}
			return name, true
		}
		if s == scrAny {
			break
		}
	}
	return "", false
}

func sortedBindings(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func (m *tuiModel) loadKeys(st *config.Store) {
	km := defaultKeymap()
	km.lua = m.hasAction
	km.runs = m.runsCommand

	for _, path := range []string{ActionsPath(st), KeybindsPath(st)} {
		if path == "" {
			continue
		}
		if err := km.readFile(path, scrAny); err != nil {
			m.keysErr = joinErr(m.keysErr, err.Error())
			return
		}
	}
	for _, f := range m.viewFiles {
		if err := km.readBytes(f.Bytes, screenOf(f), filepath.Base(f.Source)); err != nil {
			m.keysErr = joinErr(m.keysErr, err.Error())
			return
		}
	}
	if err := km.check(); err != nil {
		m.keysErr = joinErr(m.keysErr, err.Error())
		return
	}
	m.keys = km
}

func screenOf(f view.File) string {
	switch f.Kind {
	case view.KindList:
		return scrElements
	case view.KindMenu:
		return scrMenu
	case view.KindData:
		return scrData
	}
	return scrAny
}

func (km *keymap) readFile(path, screen string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	return km.readBytes(b, screen, filepath.Base(path))
}

func (km *keymap) readBytes(b []byte, screen, what string) error {
	var f keybindsFile
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	if err := dec.Decode(&f); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	for key, action := range f.Keys {
		km.bind(screen, key, action)
	}
	for s, keys := range f.Screens {
		if !contains(screens(), s) {
			return fmt.Errorf("%s: %q is not a screen (%s)", what, s,
				strings.Join(screens()[1:], ", "))
		}
		for key, action := range keys {
			km.bind(s, key, action)
		}
	}
	for name, steps := range f.Actions {
		if len(steps) == 0 {
			return fmt.Errorf("%s: action %q does nothing", what, name)
		}
		km.actions[name] = steps
	}
	if len(f.Keys) > 0 || len(f.Screens) > 0 || len(f.Actions) > 0 {
		km.from = joinErr(km.from, what)
	}
	return nil
}

func (km keymap) check() error {
	for screen, keys := range km.bound {
		for key, name := range keys {
			if name == unbound {
				continue
			}
			if strings.ContainsAny(key, " ") {
				return fmt.Errorf("the binding %q has a space in it", key)
			}
			if _, ok := km.actions[name]; ok {
				continue
			}
			if km.lua != nil && km.lua(name) {
				continue
			}
			if km.runs != nil && km.runs(name) {
				continue
			}
			if _, ok := builtinAt(screen, name); !ok {
				return fmt.Errorf("%s is bound to %q, which is not an action here (%s)",
					key, name, strings.Join(actionNamesOn(screen), ", "))
			}
		}
	}
	for name, steps := range km.actions {
		if km.lua != nil && km.lua(name) {
			return fmt.Errorf("%q is written twice, once in yaml and once in actions/%s.lua", name, name)
		}
		for i, st := range steps {
			if err := km.checkStep(name, i, st); err != nil {
				return err
			}
		}
	}
	return nil
}

func (km keymap) checkStep(name string, at int, st actionStep) error {
	where := fmt.Sprintf("action %q step %d", name, at+1)
	n := 0
	if st.Do != "" {
		n++
		_, yaml := km.actions[st.Do]
		_, builtin := anyBuiltin(st.Do)
		lua := km.lua != nil && km.lua(st.Do)
		cmd := km.runs != nil && km.runs(st.Do)
		if !yaml && !builtin && !lua && !cmd {
			return fmt.Errorf("%s runs %q, which is not an action", where, st.Do)
		}
	}
	if st.Goto != "" {
		n++
		if !contains(tutorial.Places, st.Goto) {
			return fmt.Errorf("%s goes to %q; the places are %s", where, st.Goto,
				strings.Join(tutorial.Places, ", "))
		}
	}
	if len(st.Set) > 0 {
		n++
		for field := range st.Set {
			if !contains(setFields(), field) {
				return fmt.Errorf("%s sets %q; what can be set is %s", where, field,
					strings.Join(setFields(), ", "))
			}
		}
	}
	if st.Status != "" {
		n++
	}
	if n == 0 {
		return fmt.Errorf("%s says nothing", where)
	}
	return nil
}

func anyBuiltin(name string) (builtinAction, bool) {
	for _, a := range builtinActions() {
		if a.name == name {
			return a, true
		}
	}
	return builtinAction{}, false
}

func (m tuiModel) runGlobalBinding(key string) (tea.Model, tea.Cmd, bool) {
	return m.bindingOn(scrAny, key)
}

func (m tuiModel) bindingOn(screen, key string) (tea.Model, tea.Cmd, bool) {
	if isTextKey(key) && m.typing() {
		return m, nil, false
	}
	name, ok := m.binds().action(screen, key)
	if !ok {
		return m, nil, false
	}
	next, cmd, ran := m.runNamed(screen, name, 0)
	return next, cmd, ran
}

func (m tuiModel) binds() keymap {
	if m.keys.bound == nil {
		return defaultKeymap()
	}
	return m.keys
}

func (m tuiModel) typing() bool {
	switch m.phase {
	case phasePicking:
		return m.filterOn
	case phaseData:
		return m.dataFilterOn
	case phaseMacros:
		return m.macroFilterOn
	case phaseResults:
		return m.resFilterOn
	case phaseAddTenant, phaseAddUser, phasePassword, phaseCommand, phaseZone, phaseEdit:
		return true
	}
	return false
}

func (m tuiModel) runSteps(name string, steps []actionStep, depth int) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	for i, st := range steps {
		next, cmd, err := m.runStep(st, depth)
		if err != nil {
			m.status = fmt.Sprintf("%s step %d: %s", name, i+1, err)
			return m, tea.Batch(cmds...)
		}
		m = next
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return m, tea.Batch(cmds...)
}

func (m tuiModel) runStep(st actionStep, depth int) (tuiModel, tea.Cmd, error) {
	var cmds []tea.Cmd

	if st.Do != "" {
		next, cmd, ok := m.runNamed(m.screen(), st.Do, depth+1)
		if !ok {
			return m, nil, fmt.Errorf("%q is not an action here", st.Do)
		}
		m = next.(tuiModel)
		cmds = append(cmds, cmd)
	}
	if st.Goto != "" {
		place, ok := tutorialPlaces[st.Goto]
		if !ok {
			return m, nil, fmt.Errorf("%q is not a place", st.Goto)
		}
		next, cmd := place(m)
		m = next
		cmds = append(cmds, cmd)
	}
	for _, field := range sortedBindings(st.Set) {
		next, cmd, err := m.setField(field, st.Set[field])
		if err != nil {
			return m, nil, err
		}
		m = next
		cmds = append(cmds, cmd)
	}
	if st.Status != "" {
		m.status = st.Status
	}
	return m, tea.Batch(cmds...), nil
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func (km keymap) count() int {
	n := 0
	for _, keys := range km.bound {
		n += len(keys)
	}
	return n
}

func keybindsSeed(km keymap) string {
	var b strings.Builder
	b.WriteString("# Key bindings. A key named here does what you say over what iontk binds\n")
	b.WriteString("# it to, and `none` hands the key back. examples/keybinds.yaml has an\n")
	b.WriteString("# action of your own in it, and the verbs one can be written from.\n\n")
	b.WriteString("keys:\n")
	for _, key := range sortedBindings(km.bound[scrAny]) {
		b.WriteString("  " + quoteKey(key) + ": " + km.bound[scrAny][key] + "\n")
	}
	b.WriteString("\nscreens:\n")
	for _, s := range screens()[1:] {
		if len(km.bound[s]) == 0 {
			continue
		}
		b.WriteString("  " + s + ":\n")
		for _, key := range sortedBindings(km.bound[s]) {
			b.WriteString("    " + quoteKey(key) + ": " + km.bound[s][key] + "\n")
		}
	}
	return b.String()
}

// yaml reads these as something other than a string
func quoteKey(key string) string {
	if strings.ContainsAny(key, `/:{}[],&*#?|-<>=!%@\'"`) || key == "y" || key == "n" {
		return `"` + key + `"`
	}
	return key
}

func (m tuiModel) keysContent() []string {
	km := m.binds()
	out := []string{ui.Label("Keybinds") + m.keybindsLine(), ""}
	for _, s := range []string{scrAny, scrMenu, scrElements, scrData} {
		if len(km.bound[s]) == 0 {
			continue
		}
		out = append(out, ui.StyleCmd.Render(screenLabel(s)))
		for _, key := range sortedBindings(km.bound[s]) {
			action := km.bound[s][key]
			line := ui.Label(key) + ui.StyleElement.Render(action)
			if action == unbound {
				line = ui.Label(key) + ui.StyleDim.Render("unbound")
			} else if _, own := km.actions[action]; own {
				line += ui.StyleDim.Render("  from a file")
			}
			out = append(out, line)
		}
		out = append(out, "")
	}
	if len(km.actions) > 0 {
		names := make([]string, 0, len(km.actions))
		for name := range km.actions {
			names = append(names, name)
		}
		out = append(out, ui.StyleCmd.Render("YAML Actions"),
			ui.Label("")+ui.StyleElement.Render(strings.Join(sortedBindings(toSet(names)), ", ")), "")
	}
	if names := m.actionNames(); len(names) > 0 {
		out = append(out, ui.StyleCmd.Render("Lua Actions"),
			ui.Label("")+ui.StyleElement.Render(strings.Join(names, ", ")), "")
	}
	if e := m.actionsErr(); e != "" {
		out = append(out, ui.StyleErr.Render(ui.Truncate(e, max(20, m.width-4))), "")
	}
	if m.keysErr != "" {
		out = append(out, ui.StyleErr.Render(ui.Truncate(m.keysErr, max(20, m.width-4))), "")
	}
	return out
}

func (m tuiModel) keybindsLine() string {
	line := ui.StyleElement.Render(fmt.Sprintf("%d bound", m.binds().count()))
	path := KeybindsPath(m.store())
	if path == "" {
		return line
	}
	if _, err := os.Stat(path); err != nil {
		return line + ui.StyleDim.Render("  enter writes "+shortenPath(path, 40))
	}
	return line + ui.StyleDim.Render("  "+shortenPath(path, max(20, m.width-24)))
}

func screenLabel(screen string) string {
	switch screen {
	case scrAny:
		return "Everywhere"
	case scrMenu:
		return "Menu"
	case scrElements:
		return "Element List"
	case scrData:
		return "Rich Data"
	}
	return screen
}

func toSet(list []string) map[string]string {
	out := make(map[string]string, len(list))
	for _, s := range list {
		out[s] = s
	}
	return out
}

func (m tuiModel) legend(screen, says string, actions ...string) string {
	km := m.binds()
	var keys []string
	for _, a := range actions {
		keys = append(keys, km.keysFor(screen, a)...)
	}
	if len(keys) == 0 {
		return says
	}
	return strings.Join(keys, "/") + " " + says
}

func (km keymap) keysFor(screen, action string) []string {
	var out []string
	add := func(key string) {
		if contains(out, key) {
			return
		}
		out = append(out, key)
	}
	for _, s := range []string{screen, scrAny} {
		for _, k := range shippedOrder()[s+"\x00"+action] {
			if km.bound[s][k] == action {
				add(k)
			}
		}
		if s == scrAny {
			break
		}
	}
	for _, s := range []string{screen, scrAny} {
		for _, k := range sortedBindings(km.bound[s]) {
			if km.bound[s][k] == action {
				add(k)
			}
		}
		if s == scrAny {
			break
		}
	}
	return out
}
