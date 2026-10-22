package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/ui"
)

func altDigit(key string) (int, bool) {
	if len(key) != 5 || !strings.HasPrefix(key, "alt+") || key[4] < '1' || key[4] > '9' {
		return 0, false
	}
	return int(key[4] - '0'), true
}

func plainDigit(key string) (int, bool) {
	if len(key) != 1 || key[0] < '1' || key[0] > '9' {
		return 0, false
	}
	return int(key[0] - '0'), true
}

func (m tuiModel) useFavourite(slot int) (tea.Model, tea.Cmd) {
	f, ok := m.cmds.Favourite(slot)
	if !ok {
		m.status = fmt.Sprintf("alt+%d is empty — alt+f saves the line you are on", slot)
		return m, nil
	}

	note := ""
	if model := m.activeModel(); model != "" && f.Model != "" && f.Model != model {
		note = fmt.Sprintf("alt+%d was saved on %s and this is %s — check it before sending", slot, f.Model, model)
	}

	next, cmd := m.insertCommand(f.Command)
	nm := next.(tuiModel)
	if nm.status == "" {
		nm.status = note
	}
	return nm, cmd
}

func (m tuiModel) openFavourites() (tea.Model, tea.Cmd) {
	m.favPick = true
	if m.favCursor < 1 || m.favCursor > config.FavouriteSlots {
		m.favCursor = 1
	}
	m.status = ""
	return m, nil
}

func (m tuiModel) onFavouriteKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "q", "alt+w", "alt+f":
		m.favPick = false
		m.status = ""
		return m, nil
	case "up", "k":
		m.favCursor = max(1, m.favCursor-1)
		return m, nil
	case "down", "j":
		m.favCursor = min(config.FavouriteSlots, m.favCursor+1)
		return m, nil
	case "enter":
		return m.saveFavourite(m.favCursor)
	case "ctrl+d", "x":
		return m.clearFavourite(m.favCursor)
	case "a":
		return m.openFavouriteForm(m.favCursor)
	case exportKey:
		f, ok := m.cmds.Favourite(m.favCursor)
		if !ok {
			m.status = fmt.Sprintf("alt+%d is empty", m.favCursor)
			return m, nil
		}
		slot := fmt.Sprintf("alt+%d", m.favCursor)
		return m.exportText(slot, fmt.Sprintf("favourite-%d", m.favCursor), "txt", f.Command)
	}
	if n, ok := plainDigit(key); ok {
		m.favCursor = n
		return m.saveFavourite(n)
	}
	if n, ok := altDigit(key); ok {
		m.favCursor = n
		return m.saveFavourite(n)
	}
	return m, nil
}

func (m tuiModel) saveFavourite(slot int) (tea.Model, tea.Cmd) {
	line := ""
	if s := m.sessions.Active(); s != nil {
		line = strings.TrimSpace(s.Line())
	}
	if line == "" {
		m.status = "nothing on the line to save — a adds one by hand"
		return m, nil
	}
	if err := m.cmds.SetFavourite(slot, line, m.activeModel()); err != nil {
		m.status = err.Error()
		return m, nil
	}
	m.favPick = false
	m.status = fmt.Sprintf("alt+%d is now %s", slot, line)
	return m, saveCommands(m.store, m.cmds)
}

func (m tuiModel) clearFavourite(slot int) (tea.Model, tea.Cmd) {
	if !m.cmds.ClearFavourite(slot) {
		m.status = fmt.Sprintf("alt+%d is already empty", slot)
		return m, nil
	}
	m.status = fmt.Sprintf("alt+%d cleared", slot)
	return m, saveCommands(m.store, m.cmds)
}

func (m tuiModel) openFavouriteForm(slot int) (tea.Model, tea.Cmd) {
	cmd := newField("command", "dump device status", false)
	if f, ok := m.cmds.Favourite(slot); ok {
		cmd.input.SetValue(f.Command)
	} else if s := m.sessions.Active(); s != nil {
		cmd.input.SetValue(strings.TrimSpace(s.Line()))
	}
	at := newField("slot", "1-9", false)
	at.input.SetValue(strconv.Itoa(slot))

	m.favPick = false
	return m.openForm(formFavourite, fmt.Sprintf("Favourite alt+%d", slot), phaseAddUser,
		[]formField{cmd, at})
}

func (m tuiModel) submitFavourite() (tea.Model, tea.Cmd) {
	command := m.formValue(0)
	slot, err := strconv.Atoi(m.formValue(1))
	if err != nil {
		m.status = "slot is a number from 1 to " + strconv.Itoa(config.FavouriteSlots)
		return m, nil
	}
	if err := m.cmds.SetFavourite(slot, command, m.activeModel()); err != nil {
		m.status = err.Error()
		return m, nil
	}
	m.form, m.formKind = nil, formNone
	m.phase = m.formReturn
	m.status = fmt.Sprintf("alt+%d is now %s", slot, command)
	return m, saveCommands(m.store, m.cmds)
}

func (m tuiModel) overlayFavourites(content string) string {
	line := ""
	if s := m.sessions.Active(); s != nil {
		line = strings.TrimSpace(s.Line())
	}

	heading := ui.StyleCmd.Render("favourites") + ui.StyleDim.Render("   press 1-9 to add favourite")
	if line == "" {
		heading = ui.StyleCmd.Render("favourites") + ui.StyleWarn.Render("   nothing on the line to add")
	}
	lines := []string{heading}
	for slot := 1; slot <= config.FavouriteSlots; slot++ {
		cursor := "  "
		if slot == m.favCursor {
			cursor = ui.StyleCmd.Render("> ")
		}
		row := cursor + ui.StyleDim.Render(strconv.Itoa(slot)+" ")

		f, ok := m.cmds.Favourite(slot)
		if !ok {
			lines = append(lines, row+ui.StyleDim.Render("empty"))
			continue
		}
		cmd := fmt.Sprintf("%-34s", ui.Truncate(f.Command, 34))
		tag := ""
		if f.Model != "" {
			style := ui.StyleDim
			if model := m.activeModel(); model != "" && f.Model != model {
				style = ui.StyleWarn
			}
			tag = style.Render(f.Model)
		}
		lines = append(lines, row+cmd+"  "+tag)
	}
	foot := "1-9 add this line · a by hand · " + exportKeyHint + " · ctrl+d clear · esc close"
	if line != "" {
		lines = append(lines, "", ui.StyleDim.Render("adding: ")+ui.Truncate(line, 42))
	}
	lines = append(lines, "",
		ui.StyleDim.Render(foot),
		ui.StyleDim.Render("alt+1-9 puts one on the line"))

	modal := ui.Modal(lines, m.width)
	keep := max(0, m.height-ui.ModalHeight(len(lines))-1)
	rows := strings.Split(content, "\n")
	if len(rows) > keep {
		rows = rows[:keep]
	}
	return strings.Join(rows, "\n") + "\n" + modal
}
