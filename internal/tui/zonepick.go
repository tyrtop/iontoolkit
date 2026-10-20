package tui

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/clock"
	"tyrtop.com/iontk/internal/ui"
)

func newZoneInput() textinput.Model {
	in := textinput.New()
	in.Prompt = "/"
	in.Placeholder = "chicago · london · utc · local"
	in.SetWidth(40)
	return in
}

func (m tuiModel) openZonePick() (tea.Model, tea.Cmd) {
	m.zoneInput = newZoneInput()
	m.zonePick = newPicker(clock.Zones(), "")
	m.zoneReturn = m.phase
	m.phase = phaseZone
	m.status = ""
	return m, m.zoneInput.Focus()
}

func (m tuiModel) closeZonePick() tuiModel {
	m.zoneInput.Blur()
	m.phase = m.zoneReturn
	return m
}

func (m tuiModel) onZoneKey(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		m = m.closeZonePick()
		m.status = ""
		return m, nil

	case "up", "ctrl+p":
		m.zonePick = m.zonePick.move(-1)
		return m, nil
	case "down", "ctrl+n":
		m.zonePick = m.zonePick.move(1)
		return m, nil
	case "pgup":
		m.zonePick = m.zonePick.move(-suggestRows)
		return m, nil
	case "pgdown":
		m.zonePick = m.zonePick.move(suggestRows)
		return m, nil

	case "enter":
		spec, ok := m.zonePick.current()
		if !ok {
			spec = strings.TrimSpace(m.zoneInput.Value())
		}
		nm, applied := m.applyZone(spec)
		if !applied {
			return nm, nil
		}
		return nm.closeZonePick(), nil
	}

	var cmd tea.Cmd
	m.zoneInput, cmd = m.zoneInput.Update(msg)
	m.zonePick = m.zonePick.refilter(clock.Zones(), m.zoneInput.Value())
	return m, cmd
}

func (m tuiModel) viewZonePick() string {
	var b strings.Builder
	b.WriteString(ui.StyleCmd.Render("Zone for Printed Times") + "\n\n")
	b.WriteString(m.zoneInput.View() + "   " + m.zonePick.count("zones") + "\n\n")
	b.WriteString(m.zonePick.view(m.width, suggestRows))
	b.WriteString("\n")

	line := ui.StyleDim.Render("fuzzy search · every stamp names its zone")
	if spec, ok := m.zonePick.current(); ok {
		if stamp, err := clock.StampIn(spec, time.Now()); err == nil {
			line = ui.StyleDim.Render("now  ") + ui.StyleElement.Render(stamp)
		}
	}
	b.WriteString(line + "\n")

	return b.String() + ui.Footer(m.status, "↑↓ move · enter use · esc back")
}
