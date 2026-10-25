package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/column"
	"tyrtop.com/iontk/internal/dataview"
	"tyrtop.com/iontk/internal/fixture"
	"tyrtop.com/iontk/internal/logging"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/tutorial"
	"tyrtop.com/iontk/internal/ui"
	"tyrtop.com/iontk/internal/view"
)

const (
	editKey     = "alt+e"
	editKeyHint = editKey + " yaml"
)

type editTarget struct {
	what  string
	path  string
	seed  func(m *tuiModel) string
	check func(b []byte) error
	apply func(m *tuiModel, l app.List) (string, tea.Cmd, error)
}

type yamlEdit struct {
	on      bool
	target  editTarget
	area    textarea.Model
	err     string
	note    string
	leaving bool
	back    tuiPhase
}

func (m tuiModel) openEdit() (tea.Model, tea.Cmd) {
	t, ok := m.editTarget()
	if !ok {
		m.status = "nothing on this page comes from a file yet"
		return m, nil
	}
	if t.path == "" {
		m.status = "no config directory to write " + t.what + " to"
		return m, nil
	}

	text := ""
	if b, err := os.ReadFile(t.path); err == nil {
		text = string(b)
	} else if !os.IsNotExist(err) {
		m.status = fmt.Sprintf("read %s: %s", t.path, err)
		return m, nil
	} else if t.seed != nil {
		text = t.seed(&m)
	}

	area := textarea.New()
	area.ShowLineNumbers = true
	area.CharLimit = 0
	area.MaxHeight = 0
	area.SetValue(text)
	area.MoveToBegin()
	cmd := area.Focus()

	m.edit = yamlEdit{on: true, target: t, area: area, back: m.phase}
	if _, err := os.Stat(t.path); err != nil {
		m.edit.note = "new file"
	}
	m.phase = phaseEdit
	m.fitEdit()
	return m, cmd
}

func (m *tuiModel) fitEdit() {
	if !m.edit.on {
		return
	}
	m.edit.area.SetWidth(max(20, m.width-2))
	m.edit.area.SetHeight(max(3, m.height-4))
}

func (m tuiModel) onEditKey(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	leaving := m.edit.leaving
	m.edit.leaving = false

	switch key {
	case "ctrl+s":
		return m.saveEdit()

	case "esc":
		if m.dirtyEdit() && !leaving {
			m.edit.leaving = true
			m.edit.note = "esc again to leave without saving"
			return m, nil
		}
		return m.leaveEdit("")

	case "ctrl+c":
		return m, tea.Quit
	}

	var cmd tea.Cmd
	m.edit.area, cmd = m.edit.area.Update(msg)
	return m, cmd
}

func (m tuiModel) dirtyEdit() bool {
	b, err := os.ReadFile(m.edit.target.path)
	if err != nil {
		return strings.TrimSpace(m.edit.area.Value()) != ""
	}
	return strings.TrimRight(string(b), "\n") != strings.TrimRight(m.edit.area.Value(), "\n")
}

func (m tuiModel) saveEdit() (tea.Model, tea.Cmd) {
	t := m.edit.target
	text := strings.TrimRight(m.edit.area.Value(), "\n") + "\n"

	if t.check != nil {
		if err := t.check([]byte(text)); err != nil {
			m.edit.err, m.edit.note = err.Error(), ""
			return m, nil
		}
	}
	save := &runtime.Call{Args: map[string]string{"file": t.path, "text": text}}
	if err := m.runtime().Exec(save, "config.save"); err != nil {
		m.edit.err, m.edit.note = err.Error(), ""
		return m, nil
	}
	logging.L().Info("yaml saved", "what", t.what, "path", t.path, "bytes", len(text))

	m.edit.err = ""
	m.edit.note = "saved " + shortenPath(t.path, 46)
	if t.apply == nil {
		return m, nil
	}
	mm := m
	l, _ := save.Result.(app.List)
	note, cmd, err := t.apply(&mm, l)
	if err != nil {
		mm.edit.err = err.Error()
		return mm, nil
	}
	if note != "" {
		mm.edit.note = mm.edit.note + " · " + note
	}
	return mm, cmd
}

func (m tuiModel) leaveEdit(status string) (tea.Model, tea.Cmd) {
	back := m.edit.back
	m.edit = yamlEdit{}
	m.phase = back
	m.status = status
	return m, nil
}

func (m tuiModel) viewEdit() string {
	room := max(20, m.width-ui.Width(m.edit.target.what)-20)
	head := ui.StyleCmd.Render(m.edit.target.what) +
		ui.StyleDim.Render("  "+shortenPath(m.edit.target.path, room))

	say := ""
	switch {
	case m.edit.err != "":
		say = ui.StyleErr.Render(ui.Truncate(m.edit.err, max(20, m.width-2)))
	case m.edit.note != "":
		say = ui.StyleOK.Render(ui.Truncate(m.edit.note, max(20, m.width-2)))
	}

	return strings.Join([]string{head, m.edit.area.View(), say,
		ui.StyleDim.Render("ctrl+s save · esc leave")}, "\n")
}

func (m tuiModel) editTarget() (editTarget, bool) {
	switch m.phase {
	case phaseMenu:
		return m.menuTarget()
	case phasePicking:
		return m.columnsTarget(), true
	case phaseData:
		return m.viewsTarget()
	case phaseWorkflows:
		return m.workflowTarget()
	case phaseSettings:
		switch m.setSection {
		case secTutorial:
			return m.flowTarget()
		case secTheme:
			return m.themeTarget()
		case secKeys:
			return m.keybindsTarget()
		case secDebug:
			return m.fixturesTarget()
		}
	}
	return editTarget{}, false
}

func (m tuiModel) workflowTarget() (editTarget, bool) {
	cmds := m.listedCommands()
	if m.wf.step != wfList || m.wf.cursor >= len(cmds) || cmds[m.wf.cursor].Source == "" {
		return editTarget{}, false
	}
	path := cmds[m.wf.cursor].Source
	return editTarget{
		what: filepath.Base(filepath.Dir(path)) + "/" + filepath.Base(path),
		path: path,
		apply: func(m *tuiModel, _ app.List) (string, tea.Cmd, error) {
			if w := m.runtime().Warnings(); len(w) > 0 {
				return "", nil, fmt.Errorf("%s", strings.Join(w, "; "))
			}
			return fmt.Sprintf("%d workflow(s)", len(m.runtime().CommandsNamed(app.WorkflowPrefix))), nil, nil
		},
	}, true
}

func (m tuiModel) columnsTarget() editTarget {
	path := m.listFrom
	if path == "" {
		path = existingOr(ColumnsPath(m.store()), filepath.Join(ViewsDir(m.store()), elementsViewFile))
	}
	return editTarget{
		what: filepath.Base(path),
		path: path,
		seed: func(*tuiModel) string { return column.BuiltinYAML() },
		apply: func(m *tuiModel, l app.List) (string, tea.Cmd, error) {
			m.drawRuntime(l)
			if err := m.runtimeErr(); err != nil {
				return "", nil, err
			}
			m.forgetRows()
			m.rebuildRows()
			return fmt.Sprintf("%d columns", len(m.listCols)), nil, nil
		},
	}
}

func (m tuiModel) viewsTarget() (editTarget, bool) {
	tab, ok := m.activeData()
	if !ok {
		return editTarget{}, false
	}
	name := m.viewAtScroll(tab)
	v, ok := m.runtime().Views().View(name)
	if !ok {
		return editTarget{}, false
	}

	path := v.Source
	if path == "" {
		path = existingOr(legacyViewsPath(m.store()), filepath.Join(ViewsDir(m.store()), view.FileName(v.Name)))
	}
	return editTarget{
		what: filepath.Base(path),
		path: path,
		seed: func(*tuiModel) string {
			if strings.HasSuffix(path, dataview.FileName) {
				return dataview.BuiltinYAML()
			}
			text, err := dataview.ViewYAML(v)
			if err != nil {
				return ""
			}
			return "# " + v.Name + ", as iontk ships it. " + viewPointer + "\n\n" + text
		},
		apply: func(m *tuiModel, l app.List) (string, tea.Cmd, error) {
			m.drawRuntime(l)
			if err := m.runtimeErr(); err != nil {
				return "", nil, err
			}
			return fmt.Sprintf("%d views · r refetches the page", len(m.runtime().Views().Views)), nil, nil
		},
	}, true
}

const viewPointer = "internal/dataview/builtin.yaml is the worked example"

func (m tuiModel) menuTarget() (editTarget, bool) {
	path := m.menuFrom
	if path == "" {
		dir := ViewsDir(m.store())
		if dir == "" {
			return editTarget{}, false
		}
		path = filepath.Join(dir, menuViewFile)
	}
	return editTarget{
		what: filepath.Base(path),
		path: path,
		seed: func(m *tuiModel) string { return menuSeed(m.menuEntries()) },
		check: func(b []byte) error {
			if err := view.CheckEnvelope(b, view.KindMenu); err != nil {
				return err
			}
			_, err := parseMenu(b, m.runsCommand)
			return err
		},
		apply: func(m *tuiModel, l app.List) (string, tea.Cmd, error) {
			m.menuErr = ""
			m.drawRuntime(l)
			if err := m.runtimeErr(); err != nil {
				return "", nil, err
			}
			m.menuCursor = min(m.menuCursor, max(0, len(m.menuEntries())-1))
			return fmt.Sprintf("%d entries", len(m.menuEntries())), nil, nil
		},
	}, true
}

const (
	elementsViewFile = "elements.yaml"
	menuViewFile     = "menu.yaml"
)

func existingOr(have, want string) string {
	if have != "" && pathExists(have) {
		return have
	}
	if want != "" {
		return want
	}
	return have
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (m tuiModel) flowTarget() (editTarget, bool) {
	at := m.setCursor
	if at < 0 || at >= len(m.flows) {
		return editTarget{}, false
	}
	flow := m.flows[at]

	path := flow.source
	if path == "" {
		dir := app.TutorialsPath(m.store())
		if dir == "" {
			return editTarget{}, false
		}
		if pathLooksLikeFile(dir) {
			path = dir
		} else {
			path = filepath.Join(dir, tutorial.FlowFileName(flow.name))
		}
	}
	return editTarget{
		what: filepath.Base(path),
		path: path,
		seed: func(*tuiModel) string { return tutorial.BuiltinYAML() },
		apply: func(m *tuiModel, _ app.List) (string, tea.Cmd, error) {
			*m = m.readFlows()
			if m.flowsErr != "" {
				return "", nil, fmt.Errorf("%s", m.flowsErr)
			}
			return fmt.Sprintf("%d tutorials", len(m.flows)), nil, nil
		},
	}, true
}

func (m tuiModel) fixturesTarget() (editTarget, bool) {
	path := app.FixturesPath(m.store())
	if path == "" {
		return editTarget{}, false
	}
	return editTarget{
		what: filepath.Base(path),
		path: path,
		seed: func(*tuiModel) string { return fixture.BuiltinYAML() },
		apply: func(m *tuiModel, _ app.List) (string, tea.Cmd, error) {
			_, set, err := app.FakeFleet(m.store())
			if err != nil {
				return "", nil, err
			}
			if !m.real.held {
				return fmt.Sprintf("%d tenants", len(set.Tenants)), nil, nil
			}
			mm, cmd := m.leaveTestingWorld().enterTestingWorld()
			*m = mm
			return fmt.Sprintf("%d tenants · reloaded", len(set.Tenants)), cmd, nil
		},
	}, true
}

func (m tuiModel) keybindsTarget() (editTarget, bool) {
	path := KeybindsPath(m.store())
	if path == "" {
		return editTarget{}, false
	}
	return editTarget{
		what: filepath.Base(path),
		path: path,
		seed: func(m *tuiModel) string { return keybindsSeed(m.binds()) },
		check: func(b []byte) error {
			km := defaultKeymap()
			if err := km.readBytes(b, scrAny, filepath.Base(path)); err != nil {
				return err
			}
			return km.check()
		},
		apply: func(m *tuiModel, l app.List) (string, tea.Cmd, error) {
			m.keysErr = ""
			m.drawRuntime(l)
			if err := m.runtimeErr(); err != nil {
				return "", nil, err
			}
			return fmt.Sprintf("%d bindings", m.binds().count()), nil, nil
		},
	}, true
}

func (m tuiModel) themeTarget() (editTarget, bool) {
	t, ok := m.themeAt(m.setCursor)
	if !ok {
		return editTarget{}, false
	}
	dir := app.ThemesPath(m.store())
	path, ok := ui.UserThemePath(dir, t.Name)
	if !ok {
		return editTarget{}, false
	}
	return editTarget{
		what: t.Name,
		path: path,
		seed: func(*tuiModel) string {
			text, err := ui.ThemeYAML(t)
			if err != nil {
				return ""
			}
			return text
		},
		apply: func(m *tuiModel, _ app.List) (string, tea.Cmd, error) {
			if err := ui.LoadThemes(dir); err != nil {
				return "", nil, err
			}
			next, _ := ui.ThemeByName(m.cfg().Theme)
			mm, cmd := m.applyTheme(next)
			*m = mm.(tuiModel)
			return t.Name + " applied", cmd, nil
		},
	}, true
}

func pathLooksLikeFile(path string) bool {
	if fi, err := os.Stat(path); err == nil {
		return !fi.IsDir()
	}
	return strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml")
}
