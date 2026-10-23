package tui

import (
	"fmt"
	"strings"
	"text/template"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/logging"
	"tyrtop.com/iontk/internal/tutorial"
	"tyrtop.com/iontk/internal/ui"
)

type tutorialStep struct {
	lines []string
	box   bool
	keys  []string
	stop  string
	begin func(m tuiModel) (tuiModel, tea.Cmd)
}

func (m tuiModel) stepAllows(st tutorialStep, key string) bool {
	for _, want := range st.keys {
		if m.keyMeans(want, key) {
			return true
		}
	}
	return false
}

func (m tuiModel) stepStops(st tutorialStep, key string) bool {
	return m.keyMeans(st.stopKey(), key)
}

func (m tuiModel) keyMeans(want, key string) bool {
	if want == tutorial.Typing {
		return isText(key)
	}
	for _, k := range m.keysNamed(want) {
		if k == key {
			return true
		}
	}
	return false
}

func (m tuiModel) keysNamed(want string) []string {
	if keys := m.binds().keysFor(m.screen(), want); len(keys) > 0 {
		return keys
	}
	return []string{want}
}

func (m tuiModel) keyNamed(want string) string { return m.keysNamed(want)[0] }

func (m tuiModel) pageText(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if !strings.Contains(line, "{{") {
			out = append(out, line)
			continue
		}
		t, err := template.New("p").Funcs(template.FuncMap{
			"key": m.keyNamed,
		}).Parse(line)
		if err != nil {
			out = append(out, "!"+err.Error())
			continue
		}
		var b strings.Builder
		if err := t.Execute(&b, nil); err != nil {
			out = append(out, "!"+err.Error())
			continue
		}
		out = append(out, b.String())
	}
	return out
}

func (st tutorialStep) stopKey() string {
	if st.stop == "" {
		return tutorial.DefaultStop
	}
	return st.stop
}

func isText(key string) bool {
	return key == "space" || utf8.RuneCountInString(key) == 1
}

var tutorialPlaces = map[string]func(m tuiModel) (tuiModel, tea.Cmd){
	"menu": func(m tuiModel) (tuiModel, tea.Cmd) {
		m.phase = phaseMenu
		return m, nil
	},
	"elements": func(m tuiModel) (tuiModel, tea.Cmd) {
		next, cmd := m.startContinue()
		return next.(tuiModel), cmd
	},
	"settings": func(m tuiModel) (tuiModel, tea.Cmd) {
		return m.openSettingsAt(secGeneral)
	},
	"theme-section": func(m tuiModel) (tuiModel, tea.Cmd) {
		return m.openSettingsAt(secTheme)
	},
	"macros": func(m tuiModel) (tuiModel, tea.Cmd) {
		next, cmd := m.openMacros()
		return next.(tuiModel), cmd
	},
}

func (m tuiModel) openSettingsAt(section int) (tuiModel, tea.Cmd) {
	m.setSection, m.setFocus, m.setCursor = section, focusNav, 0
	m.phase = phaseSettings
	return m, m.spinner.Tick
}

type tutorialFlow struct {
	name   string
	source string
	steps  []tutorialStep
}

var tutorialScript = func(st *config.Store) ([]tutorialFlow, error) {
	sets, err := tutorial.Flows(app.TutorialsPath(st))
	flows := make([]tutorialFlow, 0, len(sets))
	for _, set := range sets {
		flows = append(flows, tutorialFlow{name: set.Name, source: set.Source, steps: stepsFrom(set.Shown())})
	}
	return flows, err
}

func (m tuiModel) readFlows() tuiModel {
	flows, err := tutorialScript(m.store)
	m.flows, m.flowsErr = flows, ""
	if err != nil {
		m.flowsErr = err.Error()
		logging.L().Warn("tutorial refused", "err", err)
	}
	if note := m.unboundPages(); note != "" {
		m.flowsErr = joinErr(m.flowsErr, note)
	}
	return m
}

func (m tuiModel) unboundPages() string {
	km := m.binds()
	var bad []string
	for _, flow := range m.flows {
		for i, st := range flow.steps {
			for _, want := range append([]string{st.stopKey()}, st.keys...) {
				if want == tutorial.Typing || len(km.keysFor(m.screen(), want)) > 0 {
					continue
				}
				if _, own := km.actions[want]; !own {
					if _, ok := anyBuiltin(want); !ok {
						continue
					}
				}
				bad = append(bad, fmt.Sprintf("%s page %d waits for %q, which no key is bound to",
					flow.name, i+1, want))
			}
		}
	}
	return strings.Join(bad, "; ")
}

func stepsFrom(pages []tutorial.Page) []tutorialStep {
	steps := make([]tutorialStep, 0, len(pages))
	for _, p := range pages {
		steps = append(steps, tutorialStep{
			lines: p.Text,
			box:   p.Box,
			keys:  p.Keys,
			stop:  p.StopKey(),
			begin: tutorialPlaces[p.Go],
		})
	}
	return steps
}

type tutorialRun struct {
	on      bool
	at      int
	name    string
	steps   []tutorialStep
	passing bool
}

const (
	tutorialBarLines = 3
	tutorialQuitKey  = "ctrl+x"
	tutorialSection  = "Settings → Tutorial"
)

func (m tuiModel) currentStep() (tutorialStep, bool) {
	if !m.tut.on || m.tut.at < 0 || m.tut.at >= len(m.tut.steps) {
		return tutorialStep{}, false
	}
	return m.tut.steps[m.tut.at], true
}

func (m tuiModel) startTutorial() (tea.Model, tea.Cmd) { return m.startFlow(0) }

func (m tuiModel) startFlow(at int) (tea.Model, tea.Cmd) {
	m = m.readFlows()
	if m.flowsErr != "" && len(m.flows) == 0 {
		m.status = m.flowsErr
		return m, nil
	}
	if at < 0 || at >= len(m.flows) {
		m.status = "no tutorial there"
		return m, nil
	}

	flow := m.flows[at]
	if len(flow.steps) == 0 {
		m.status = flow.name + " has no pages yet"
		return m, nil
	}
	if why := m.testingBlocked(); why != "" {
		m.status = why
		return m, nil
	}

	m.askTutorial, m.askTutorialNote = false, false
	m.tut = tutorialRun{on: true, steps: flow.steps, name: flow.name}
	m.phase = phaseMenu
	m.status = ""
	m = m.fitTutorial()
	logging.L().Info("tutorial started", "tutorial", flow.name, "steps", len(flow.steps))

	m, load := m.enterTestingWorld()
	return m, load
}

func (m tuiModel) stopTutorial() (tuiModel, tea.Cmd) {
	at, n, name := m.tut.at, len(m.tut.steps), m.tut.name
	m.tut = tutorialRun{}
	m = m.leaveTestingWorld().fitTutorial()
	m.phase = phaseMenu
	logging.L().Info("tutorial left", "tutorial", name, "step", at+1, "steps", n)
	return m, nil
}

func (m tuiModel) fitTutorial() tuiModel {
	if m.termHeight <= 0 {
		return m
	}
	m.height = m.usableHeight()
	m.rebuildRows()
	m.rebuildResultRows()
	return m
}

func (m tuiModel) usableHeight() int {
	h := m.termHeight
	if m.tut.on {
		h -= tutorialBarLines
	}
	return max(1, h)
}

func (m tuiModel) passStep() (tuiModel, tea.Cmd) {
	m.tut.at++
	if m.tut.at >= len(m.tut.steps) {
		return m.stopTutorial()
	}

	st, ok := m.currentStep()
	if !ok || st.begin == nil {
		return m, nil
	}
	return st.begin(m)
}

func (m tuiModel) onTutorialKey(key string) (tuiModel, tea.Cmd, bool) {
	st, ok := m.currentStep()
	if !ok {
		return m, nil, false
	}

	if key == tutorialQuitKey {
		next, cmd := m.stopTutorial()
		return next, cmd, true
	}

	if st.box {
		switch {
		case m.stepStops(st, key), key == "space":
			next, cmd := m.passStep()
			return next, cmd, true
		case key == "esc", key == "q":
			next, cmd := m.stopTutorial()
			return next, cmd, true
		}
		return m, nil, true
	}

	if m.stepAllows(st, key) {
		m.tut.passing = m.stepStops(st, key)
		return m, nil, false
	}
	if m.stepStops(st, key) {
		next, cmd := m.passStep()
		return next, cmd, true
	}
	return m, nil, true
}

func (m tuiModel) tutorialBar() string {
	st, ok := m.currentStep()
	if !ok {
		return strings.Repeat("\n", tutorialBarLines-1)
	}

	width := max(10, m.width-1)
	head := fmt.Sprintf("tutorial · step %d of %d · %s quits",
		m.tut.at+1, len(m.tut.steps), tutorialQuitKey)
	if !m.stepAllows(st, m.keyNamed(st.stopKey())) {
		head = fmt.Sprintf("tutorial · step %d of %d · %s next · %s quits",
			m.tut.at+1, len(m.tut.steps), m.keyNamed(st.stopKey()), tutorialQuitKey)
	}
	rule := ""
	if pad := width - ui.Width(head) - 4; pad > 0 {
		rule = " " + strings.Repeat("─", pad)
	}

	lines := m.pageText(st.lines)
	out := make([]string, 0, tutorialBarLines)
	out = append(out, ui.StyleDim.Render("── "+head+rule))
	for i := 0; i < tutorialBarLines-1; i++ {
		line := ""
		if i < len(lines) {
			line = "  " + ui.Truncate(lines[i], width-2)
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func (m tuiModel) tutorialModal() string {
	st, _ := m.currentStep()

	lines := make([]string, 0, len(st.lines)+3)
	lines = append(lines, ui.StyleCmd.Render("Tutorial")+
		ui.StyleDim.Render(fmt.Sprintf("   step %d of %d", m.tut.at+1, len(m.tut.steps))))
	lines = append(lines, "")
	for _, l := range m.pageText(st.lines) {
		lines = append(lines, ui.Truncate(l, ui.ModalWidth))
	}

	hint := m.keyNamed(st.stopKey()) + " next · esc leave"
	if m.tut.at == len(m.tut.steps)-1 {
		hint = m.keyNamed(st.stopKey()) + " finish"
	}
	lines = append(lines, "", ui.StyleDim.Render(hint))
	return ui.Modal(lines, m.width)
}

func (m tuiModel) overlayTutorial(content string) string {
	st, ok := m.currentStep()
	if !ok {
		return content
	}
	if st.box {
		return overlayUnder(content, m.tutorialModal(), m.termHeight)
	}

	lines := strings.Split(content, "\n")
	const footerLines = 2
	if len(lines) <= footerLines {
		return content + "\n" + m.tutorialBar()
	}

	body, footer := lines[:len(lines)-footerLines], lines[len(lines)-footerLines:]
	if keep := m.termHeight - footerLines - tutorialBarLines; keep > 0 && len(body) > keep {
		body = body[:keep]
	}
	return strings.Join(body, "\n") + "\n" + m.tutorialBar() + "\n" + strings.Join(footer, "\n")
}

func overlayUnder(content, box string, height int) string {
	keep := max(0, height-len(strings.Split(box, "\n"))-1)
	lines := strings.Split(content, "\n")
	if keep > 0 && len(lines) > keep {
		lines = lines[:keep]
	}
	return strings.Join(lines, "\n") + "\n" + box
}

func (m tuiModel) raiseTutorialAsk() tuiModel {
	if m.cfg == nil || m.cfg.TutorialSeen {
		return m
	}
	m.askTutorial = true
	return m
}

func (m tuiModel) tutorialAsk() string {
	return ui.Modal([]string{
		ui.StyleCmd.Render("Start Tutorial"),
		"",
		ui.StyleOK.Render("yes") + "    " + ui.StyleErr.Render("no"),
	}, m.width)
}

func (m tuiModel) tutorialNote() string {
	return ui.Modal([]string{
		ui.StyleCmd.Render("Tutorial"),
		"",
		ui.Truncate("Start it any time from "+tutorialSection+".", ui.ModalWidth),
		"",
		ui.StyleDim.Render("enter ok"),
	}, m.width)
}

func (m tuiModel) onTutorialAskKey(key string) (tea.Model, tea.Cmd) {
	if m.askTutorialNote {
		m.askTutorialNote = false
		return m, nil
	}

	switch key {
	case "y", "Y", "enter":
		m = m.rememberTutorialAsked()
		return m.startTutorial()
	case "n", "N", "esc", "q":
		m = m.rememberTutorialAsked()
		m.askTutorial, m.askTutorialNote = false, true
		return m, nil
	}
	return m, nil
}

func (m tuiModel) rememberTutorialAsked() tuiModel {
	if m.cfg == nil || m.cfg.TutorialSeen {
		return m
	}
	m.cfg.TutorialSeen = true
	if m.store != nil {
		if err := m.store.SaveConfig(m.cfg); err != nil {
			m.status = err.Error()
		}
	}
	return m
}
