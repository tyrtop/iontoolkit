package tui

import (
	"strings"
	"tyrtop.com/iontk/internal/runtime"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/logging"
	"tyrtop.com/iontk/internal/sase/write"
	"tyrtop.com/iontk/internal/ui"
)

const (
	writeKey     = "w"
	writeKeyHint = writeKey + " edit"
)

type writeStep int

const (
	writeOff writeStep = iota
	writeSpecs
	writeLoading
	writePick
	writeForm
	writeReview
	writeSending
)

type writeState struct {
	step    writeStep
	eid     string
	tsg     string
	vars    map[string]string
	specs   []write.Spec
	spec    write.Spec
	recs    []map[string]any
	cursor  int
	rec     map[string]any
	inputs  []textinput.Model
	focus   int
	plan    write.Plan
	blocked string
	err     string
}

type writeRecsMsg struct {
	eid  string
	recs []map[string]any
	err  string
}

type writePlannedMsg struct {
	eid     string
	plan    write.Plan
	found   bool
	blocked string
	err     string
}

type writeSentMsg struct {
	eid   string
	label string
	err   string
}

func (m tuiModel) startWrite() (tea.Model, tea.Cmd) {
	tab, ok := m.activeData()
	if !ok {
		return m, nil
	}
	writes, _ := m.runtime().Writes()
	if writes == nil || len(writes.Writes) == 0 {
		m.status = "nothing is editable — writes.yaml names no records"
		return m, nil
	}
	m.wr = writeState{eid: tab.EID, tsg: tab.TSG, vars: tab.Vars(), specs: writes.Writes}
	if len(m.wr.specs) > 1 {
		m.wr.step = writeSpecs
		return m, nil
	}
	return m.loadWriteRecords(m.wr.specs[0])
}

func (m tuiModel) loadWriteRecords(spec write.Spec) (tea.Model, tea.Cmd) {
	m.wr.spec, m.wr.step, m.wr.cursor, m.wr.err = spec, writeLoading, 0, ""
	rt, ctx, eid := m.runtime(), m.ctx, m.wr.eid
	return m, tea.Batch(m.spinner.Tick, func() tea.Msg {
		c := &runtime.Call{Ctx: ctx, Args: map[string]string{"what": spec.Name, "element": eid}}
		msg := writeRecsMsg{eid: eid}
		if err := rt.Exec(c, "write.records"); err != nil {
			msg.err = err.Error()
		}
		msg.recs, _ = c.Result.([]map[string]any)
		return msg
	})
}

func (m tuiModel) onWriteRecs(msg writeRecsMsg) (tea.Model, tea.Cmd) {
	if m.wr.step != writeLoading || msg.eid != m.wr.eid {
		return m, nil
	}
	m.wr.step, m.wr.recs, m.wr.err = writePick, msg.recs, msg.err
	return m, nil
}

func (m tuiModel) openWriteForm() tuiModel {
	m.wr.rec = m.wr.recs[m.wr.cursor]
	m.wr.inputs = make([]textinput.Model, len(m.wr.spec.Fields))
	for i, f := range m.wr.spec.Fields {
		in := textinput.New()
		in.Prompt = ""
		in.SetWidth(40)
		in.SetValue(write.Show(write.Value(m.wr.rec, f.Key)))
		m.wr.inputs[i] = in
	}
	m.wr.focus, m.wr.err, m.wr.step = 0, "", writeForm
	m.wr.inputs[0].Focus()
	return m
}

func (m tuiModel) moveWriteFocus(by int) tuiModel {
	m.wr.inputs[m.wr.focus].Blur()
	m.wr.focus = (m.wr.focus + by + len(m.wr.inputs)) % len(m.wr.inputs)
	m.wr.inputs[m.wr.focus].Focus()
	return m
}

func (m tuiModel) reviewWrite() (tuiModel, tea.Cmd) {
	sets := make([]string, 0, len(m.wr.spec.Fields))
	for i, f := range m.wr.spec.Fields {
		sets = append(sets, f.Key+"="+m.wr.inputs[i].Value())
	}
	args := map[string]string{"what": m.wr.spec.Name, "elements": m.wr.eid, "match": "id=" + write.Show(m.wr.rec["id"])}
	rt, ctx, eid := m.runtime(), m.ctx, m.wr.eid
	m.wr.err = ""
	return m, func() tea.Msg {
		c := &runtime.Call{Ctx: ctx, Args: args, Rest: sets}
		err := rt.Exec(c, "write")
		msg := writePlannedMsg{eid: eid}
		if run, ok := c.Result.(*app.WriteRun); ok {
			msg.blocked = run.Blocked
			for _, ep := range run.Plans {
				if len(ep.Plans) > 0 {
					msg.plan, msg.found = ep.Plans[0], true
				}
			}
		}
		if err != nil {
			msg.err = err.Error()
		}
		return msg
	}
}

func (m tuiModel) onWritePlanned(msg writePlannedMsg) (tea.Model, tea.Cmd) {
	if m.wr.step != writeForm || msg.eid != m.wr.eid {
		return m, nil
	}
	switch {
	case msg.err != "" && !msg.found:
		m.wr.err = msg.err
	case !msg.found || msg.plan.Empty():
		m.wr.err = "nothing changed"
	default:
		m.wr.plan, m.wr.blocked, m.wr.err, m.wr.step = msg.plan, msg.blocked, "", writeReview
	}
	return m, nil
}

func (m tuiModel) sendWrite() (tea.Model, tea.Cmd) {
	if m.wr.blocked != "" {
		return m, nil
	}
	m.wr.step = writeSending
	rt, p, eid, ctx := m.runtime(), m.wr.plan, m.wr.eid, m.ctx
	args, sets := editCall(m.wr.spec.Name, eid, p)
	logging.L().Info("portal write", "spec", m.wr.spec.Name, "path", p.Path, "changes", len(p.Changes))
	return m, tea.Batch(m.spinner.Tick, func() tea.Msg {
		err := rt.Exec(&runtime.Call{Ctx: ctx, Args: args, Rest: sets, DoIt: true}, "write")
		msg := writeSentMsg{eid: eid, label: p.Label}
		if err != nil {
			msg.err = err.Error()
		}
		return msg
	})
}

func (m tuiModel) onWriteSent(msg writeSentMsg) (tea.Model, tea.Cmd) {
	if msg.eid != m.wr.eid {
		return m, nil
	}
	if msg.err != "" {
		m.wr.step, m.wr.err = writeReview, msg.err
		return m, nil
	}
	m.wr = writeState{}
	m.status = "saved " + msg.label
	return m.refreshData()
}

func (m tuiModel) onWriteKey(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	switch m.wr.step {
	case writeSpecs:
		switch key {
		case "esc", "q":
			m.wr = writeState{}
		case "up", "k":
			m.wr.cursor = max(0, m.wr.cursor-1)
		case "down", "j":
			m.wr.cursor = min(len(m.wr.specs)-1, m.wr.cursor+1)
		case "enter":
			return m.loadWriteRecords(m.wr.specs[m.wr.cursor])
		}

	case writeLoading, writeSending:
		if key == "esc" && m.wr.step == writeLoading {
			m.wr = writeState{}
		}

	case writePick:
		switch key {
		case "esc", "q":
			m.wr = writeState{}
		case "up", "k":
			m.wr.cursor = max(0, m.wr.cursor-1)
		case "down", "j":
			m.wr.cursor = min(len(m.wr.recs)-1, m.wr.cursor+1)
		case "enter":
			if len(m.wr.recs) > 0 {
				m = m.openWriteForm()
			}
		}

	case writeForm:
		switch key {
		case "esc":
			m.wr.step, m.wr.err = writePick, ""
		case "tab", "down":
			m = m.moveWriteFocus(1)
		case "shift+tab", "up":
			m = m.moveWriteFocus(-1)
		case "enter":
			return m.reviewWrite()
		default:
			var cmd tea.Cmd
			m.wr.inputs[m.wr.focus], cmd = m.wr.inputs[m.wr.focus].Update(msg)
			return m, cmd
		}

	case writeReview:
		switch key {
		case "esc", "n":
			m.wr.step, m.wr.err = writeForm, ""
		case "y":
			return m.sendWrite()
		}
	}
	return m, nil
}

func (m tuiModel) viewWrite() string {
	var b strings.Builder
	b.WriteString(m.tabBar() + "\n")

	width := max(20, m.width-1)
	title := "Edit"
	if m.wr.spec.Name != "" {
		title += " " + m.wr.spec.Title
	}
	where := ""
	if tab, ok := m.activeData(); ok {
		where = tab.Name
	}
	b.WriteString(ui.StyleCmd.Render(title) + ui.StyleDim.Render("  "+where) + "\n\n")

	hint := "esc back"
	switch m.wr.step {
	case writeSpecs:
		for i, s := range m.wr.specs {
			b.WriteString(pickLine(i == m.wr.cursor, s.Title, width) + "\n")
		}
		hint = "↑↓ move · enter pick · esc back"

	case writeLoading:
		b.WriteString(ui.StyleDim.Render(m.spinner.View()+" reading "+m.wr.spec.Title+" records") + "\n")

	case writePick:
		if len(m.wr.recs) == 0 && m.wr.err == "" {
			b.WriteString(ui.StyleDim.Render("nothing to edit on this element") + "\n")
		}
		for i, rec := range m.wr.recs {
			b.WriteString(pickLine(i == m.wr.cursor, m.wr.spec.LabelOf(m.wr.vars, rec), width) + "\n")
		}
		hint = "↑↓ move · enter edit · esc back"

	case writeForm:
		b.WriteString(ui.StyleElement.Render(m.wr.spec.LabelOf(m.wr.vars, m.wr.rec)) + "\n\n")
		for i, f := range m.wr.spec.Fields {
			mark := "  "
			if i == m.wr.focus {
				mark = ui.StyleCmd.Render("> ")
			}
			b.WriteString(mark + ui.Label(f.Label) + m.wr.inputs[i].View() + ui.StyleDim.Render("  "+fieldHint(f)) + "\n")
		}
		hint = "tab/↑↓ field · enter review · esc back"

	case writeReview, writeSending:
		p := m.wr.plan
		b.WriteString(ui.StyleElement.Render(p.Label) + "\n")
		b.WriteString(ui.StyleDim.Render(ui.Truncate(p.Method+" "+p.Path, width)) + "\n\n")
		for _, c := range p.Changes {
			label := c.Field
			if f, ok := m.wr.spec.Field(c.Field); ok {
				label = f.Label
			}
			b.WriteString("  " + ui.Label(label) +
				ui.StyleErr.Render(dashed(write.Show(c.From))) + ui.StyleDim.Render("  →  ") +
				ui.StyleOK.Render(dashed(write.Show(c.To))) + "\n")
		}
		b.WriteString("\n")
		if m.wr.step == writeSending {
			b.WriteString(ui.StyleDim.Render(m.spinner.View()+" sending") + "\n")
			hint = ""
		} else if why := m.wr.blocked; why != "" {
			b.WriteString(ui.StyleWarn.Render(ui.Truncate(why, width)) + "\n")
			hint = "esc back to the fields"
		} else {
			b.WriteString(ui.StyleWarn.Render("this changes the portal") + "\n")
			hint = "y send · n/esc back to the fields"
		}
	}

	if m.wr.err != "" {
		b.WriteString("\n" + ui.StyleErr.Render(ui.Truncate(m.wr.err, width)) + "\n")
	}
	return b.String() + ui.Footer("", ui.Truncate(hint, width))
}

func pickLine(on bool, text string, width int) string {
	text = ui.Truncate(text, max(10, width-4))
	if on {
		return ui.StyleCmd.Render("> ") + ui.StyleElement.Render(text)
	}
	return "  " + text
}

func fieldHint(f write.Field) string {
	switch f.Type {
	case write.TypeBool:
		return "true or false"
	case write.TypeInt:
		return "a number"
	case write.TypeList:
		return "comma separated"
	}
	return ""
}

func editCall(what, eid string, p write.Plan) (map[string]string, []string) {
	args := map[string]string{"what": what, "elements": eid, "match": "id=" + write.Show(p.Before["id"])}
	sets := make([]string, 0, len(p.Changes))
	for _, c := range p.Changes {
		sets = append(sets, c.Field+"="+write.Show(c.To))
	}
	return args, sets
}
