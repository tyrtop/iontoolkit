package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/ioncmd"
	"tyrtop.com/iontk/internal/sase/ioncli"
	"tyrtop.com/iontk/internal/ui"
)

type slashMode int

const (
	slashOff slashMode = iota
	slashMenu
	slashCommands
	slashFreq
	slashHelp
	slashDebug
)

const slashRows = 6

var slashVerbs = []string{"login", "commands", "fc", "help", "debug"}

func newSlashInput() textinput.Model {
	in := textinput.New()
	in.Prompt = "/"
	in.Placeholder = "commands du interf · fc · login · help · debug"
	in.SetWidth(40)
	return in
}

func (m tuiModel) openSlash() (tea.Model, tea.Cmd) {
	m.slash = slashMenu
	m.slashInput = newSlashInput()
	m.slashCursor = 0
	m.slashItems = slashVerbs
	return m, m.slashInput.Focus()
}

func (m tuiModel) closeSlash() tuiModel {
	m.slash = slashOff
	m.slashInput.Blur()
	m.slashItems = nil
	m.slashCursor = 0
	return m
}

func (m tuiModel) catalog() []string {
	all := ioncmd.Embedded()
	if app.Defaults.AllowWrites || m.cfg.AllowWrites {
		return all
	}
	return ioncmd.ReadOnly(all, ioncli.IsReadOnly)
}

func resolveVerb(word string) (string, bool) {
	word = strings.ToLower(strings.TrimSpace(word))
	if word == "" {
		return "", false
	}
	for _, v := range slashVerbs {
		if v == word {
			return v, true
		}
	}
	if hits := prefixVerbs(word); len(hits) == 1 {
		return hits[0], true
	}
	hits := ioncmd.Fuzzy(slashVerbs, word)
	if len(hits) == 1 {
		return hits[0], true
	}
	return "", false
}

func prefixVerbs(word string) []string {
	var out []string
	for _, v := range slashVerbs {
		if strings.HasPrefix(v, word) {
			out = append(out, v)
		}
	}
	return out
}

func (m tuiModel) refilterSlash() tuiModel {
	q := m.slashInput.Value()

	if m.slash == slashMenu {
		if verb, rest, ok := strings.Cut(q, " "); ok {
			if name, found := resolveVerb(verb); found {
				return m.enterVerb(name, rest)
			}
		}
	}

	switch m.slash {
	case slashMenu:
		m.slashItems = ioncmd.Fuzzy(slashVerbs, q)
	case slashCommands:
		m.slashItems = ioncmd.Fuzzy(m.catalog(), q)
	case slashFreq:
		m = m.setFreq(q)
	default:
		m.slashItems = nil
	}
	if m.slashCursor >= len(m.slashItems) {
		m.slashCursor = max(0, len(m.slashItems)-1)
	}
	return m
}

func (m tuiModel) enterVerb(name, query string) tuiModel {
	switch name {
	case "commands":
		m.slash = slashCommands
		m.slashInput = newSlashInput()
		m.slashInput.Prompt = "/commands "
		m.slashInput.Placeholder = "fuzzy search"
		m.slashInput.SetValue(query)
		m.slashInput.SetCursor(len(query))
		_ = m.slashInput.Focus()
		m.slashCursor = 0
		m.slashItems = ioncmd.Fuzzy(m.catalog(), query)
	case "fc":
		m.slash = slashFreq
		m.slashInput = newSlashInput()
		m.slashInput.Prompt = "/fc "
		m.slashInput.Placeholder = "most used first"
		m.slashInput.SetValue(query)
		m.slashInput.SetCursor(len(query))
		_ = m.slashInput.Focus()
		m.slashCursor = 0
		m = m.setFreq(query)
	case "help":
		m.slash = slashHelp
		m.slashItems = nil
	case "debug":
		m.slash = slashDebug
		m.slashItems = nil
	}
	return m
}

func (m tuiModel) activateSlash() (tea.Model, tea.Cmd) {
	switch m.slash {
	case slashMenu:
		pick := ""
		if m.slashCursor < len(m.slashItems) {
			pick = m.slashItems[m.slashCursor]
		}
		if verb, found := resolveVerb(m.slashInput.Value()); found {
			pick = verb
		}
		if pick == "" {
			return m, nil
		}

		switch pick {
		case "commands", "fc", "help", "debug":
			m = m.enterVerb(pick, "")
			if m.slash == slashCommands || m.slash == slashFreq {
				return m, m.slashInput.Focus()
			}
			m.slashInput.Blur()
			return m, nil
		case "login":
			s := m.sessions.Active()
			if s == nil {
				return m.closeSlash(), nil
			}
			creds := m.activeSessionCreds()
			if !s.Login(creds) {
				m = m.closeSlash()
				m.status = "no stored credentials for this tenant"
				return m, nil
			}
			m = m.closeSlash()
			m.status = "signing in as " + creds.User
			return m, nil
		}
		return m, nil

	case slashFreq:
		use, ok := m.freqAt(m.slashCursor)
		if !ok {
			return m.closeSlash(), nil
		}
		return m.insertCommand(use.Command)

	case slashCommands:
		text := strings.TrimSpace(m.slashInput.Value())
		if m.slashCursor < len(m.slashItems) {
			text = m.slashItems[m.slashCursor]
		}
		if text == "" {
			return m.closeSlash(), nil
		}
		return m.insertCommand(text)
	}
	return m.closeSlash(), nil
}

func (m tuiModel) insertCommand(text string) (tea.Model, tea.Cmd) {
	if !app.Defaults.AllowWrites && !m.cfg.AllowWrites && !ioncli.IsReadOnly(text) {
		m.status = "not read-only: " + text
		return m.closeSlash(), nil
	}
	if s := m.sessions.Active(); s != nil && s.Err() == nil {
		s.Insert(text + " ")
	}
	return m.closeSlash(), nil
}

func (m tuiModel) onSlashKey(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "ctrl+c":
		if m.slash == slashHelp || m.slash == slashDebug {
			m.slash = slashMenu
			m = m.refilterSlash()
			return m, m.slashInput.Focus()
		}
		return m.closeSlash(), nil
	case "enter":
		return m.activateSlash()
	case "tab":
		if m.slash == slashMenu {
			return m.activateSlash()
		}
		if m.slash == slashCommands && m.slashCursor < len(m.slashItems) {
			pick := m.slashItems[m.slashCursor]
			cur := strings.TrimRight(m.slashInput.Value(), " ")
			if pick != cur {
				m.slashInput.SetValue(pick + " ")
				m.slashInput.SetCursor(len(pick) + 1)
				m.slashCursor = 0
				return m.refilterSlash(), nil
			}
		}
		return m, nil

	case "ctrl+d":
		if m.slash == slashFreq {
			return m.forgetFreq()
		}
		return m, nil

	case "up", "ctrl+p":
		m.slashCursor = max(0, m.slashCursor-1)
		return m, nil
	case "down", "ctrl+n":
		m.slashCursor = min(max(0, len(m.slashItems)-1), m.slashCursor+1)
		return m, nil
	}

	if m.slash == slashHelp || m.slash == slashDebug {
		return m, nil
	}

	var cmd tea.Cmd
	m.slashInput, cmd = m.slashInput.Update(msg)
	m = m.refilterSlash()
	return m, cmd
}

func (m tuiModel) slashWanted() int {
	switch m.slash {
	case slashOff:
		return 0
	case slashHelp:
		return 12
	case slashDebug:
		return 8
	default:
		return slashRows + 3
	}
}

func (m tuiModel) slashHeight() int {
	want := m.slashWanted()
	if want == 0 {
		return 0
	}
	return max(0, min(want, m.height-5))
}

func (m tuiModel) viewSlash() string {
	var b strings.Builder
	b.WriteString(ui.StyleDim.Render(strings.Repeat("─", max(10, min(m.width, 80)))) + "\n")

	switch m.slash {
	case slashHelp:
		b.WriteString(ui.StyleCmd.Render("/help") + "\n\n")
		b.WriteString("  " + ui.StyleElement.Render("/login") + ui.StyleDim.Render("     fill the stored ION user and password") + "\n")
		b.WriteString("  " + ui.StyleElement.Render("/commands") + ui.StyleDim.Render("  browse the ION command reference, enter inserts") + "\n")
		b.WriteString("  " + ui.StyleElement.Render("/fc") + ui.StyleDim.Render("        commands you run most on this model, ctrl+d forgets one") + "\n")
		b.WriteString("  " + ui.StyleElement.Render("/debug") + ui.StyleDim.Render("     buffer and pane geometry") + "\n")
		b.WriteString("  " + ui.StyleElement.Render("/help") + ui.StyleDim.Render("      this list") + "\n\n")
		b.WriteString(ui.StyleDim.Render("  tab completes the selection · enter inserts without sending") + "\n")
		b.WriteString(ui.StyleDim.Render("  / only opens here when the device line is empty") + "\n")
		b.WriteString(ui.StyleDim.Render("esc back"))
		return b.String()

	case slashDebug:
		b.WriteString(ui.StyleCmd.Render("/debug") + "\n\n")
		cols, rows := m.paneSize()
		s := m.sessions.Active()
		if s == nil {
			b.WriteString(ui.StyleDim.Render("  no active session") + "\n")
		} else {
			bc, br := s.Size()
			cx, cy := s.Cursor()
			b.WriteString("  " + ui.Label("Buffer") + fmt.Sprintf("%d x %d\n", bc, br))
			b.WriteString("  " + ui.Label("Pane") + fmt.Sprintf("%d x %d\n", cols, rows))
			b.WriteString("  " + ui.Label("Cursor") + fmt.Sprintf("%d, %d\n", cx, cy))
			b.WriteString("  " + ui.Label("Terminal") + fmt.Sprintf("%d x %d\n", m.width, m.height))
			b.WriteString("  " + ui.Label("Line") + fmt.Sprintf("%q\n", s.Line()))
		}
		b.WriteString(ui.StyleDim.Render("esc back"))
		return b.String()
	}

	b.WriteString(m.slashInput.View())
	if n := len(m.slashItems); n > 0 {
		b.WriteString(ui.StyleDim.Render(fmt.Sprintf("   %d", n)))
	}
	b.WriteString("\n")

	start := 0
	if m.slashCursor >= slashRows {
		start = m.slashCursor - slashRows + 1
	}
	shown := 0
	for i := start; i < len(m.slashItems) && shown < slashRows; i++ {
		prefix := "  "
		line := m.slashItems[i]
		if i == m.slashCursor {
			prefix = ui.StyleCmd.Render("> ")
			line = ui.StyleElement.Render(line)
		}
		b.WriteString(prefix + ui.Truncate(line, max(10, m.width-4)) + "\n")
		shown++
	}
	for ; shown < slashRows; shown++ {
		b.WriteString("\n")
	}

	hint := "↑↓ select · tab complete · enter use · esc close"
	if m.slash == slashFreq {
		hint = "↑↓ select · enter use · ctrl+d forget · esc close"
	}
	b.WriteString(ui.StyleDim.Render(hint))
	return b.String()
}
