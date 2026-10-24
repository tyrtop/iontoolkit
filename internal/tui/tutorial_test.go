package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/tutorial"
)

func sized(t *testing.T, m tuiModel, w, h int) tuiModel {
	t.Helper()
	mm, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return mm.(tuiModel)
}

func scriptedTutorial(m tuiModel, steps ...tutorialStep) tuiModel {
	m.tut = tutorialRun{on: true, steps: steps}
	m = m.fitTutorial()
	return m
}

func openingStep(text string) tutorialStep {
	return tutorialStep{lines: []string{text}, box: true}
}

func TestTheAskArrivesOnlyUntilItIsAnswered(t *testing.T) {
	tests := []struct {
		name string
		cfg  *config.Config
		want bool
	}{
		{"a config root with nothing in it", &config.Config{}, true},
		{"already answered once", &config.Config{TutorialSeen: true}, false},
		{"no config at all", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := pickModel(t)
			m.runtime().UseConfig(m.store(), tt.cfg)
			if got := m.raiseTutorialAsk().askTutorial; got != tt.want {
				t.Errorf("askTutorial = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSayingNoPointsAtTheSettingsSectionAndDoesNotAskAgain(t *testing.T) {
	m := pickModel(t)
	m.runtime().UseConfig(m.store(), &config.Config{})
	m = m.raiseTutorialAsk()

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'n', Text: "n"}))
	m = mm.(tuiModel)

	if m.askTutorial {
		t.Error("the yes/no box is still up after n")
	}
	if !m.askTutorialNote {
		t.Fatal("no is meant to leave a note saying where the tutorial lives")
	}
	if got := plain(m.View().Content); !strings.Contains(got, tutorialSection) {
		t.Errorf("the note never names the settings section:\n%s", got)
	}
	if !m.cfg().TutorialSeen {
		t.Error("the answer was not remembered, so the box comes back next launch")
	}

	saved, err := m.store().Config()
	if err != nil {
		t.Fatal(err)
	}
	if !saved.TutorialSeen {
		t.Error("the answer was not written to the config file")
	}

	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if mm.(tuiModel).askTutorialNote {
		t.Error("enter did not dismiss the note")
	}
}

func TestSayingYesStartsTheShippedPages(t *testing.T) {
	t.Cleanup(func() { app.Fixture = nil })

	m := pickModel(t)
	m.runtime().UseConfig(m.store(), &config.Config{})
	m = m.raiseTutorialAsk()

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'y', Text: "y"}))
	m = mm.(tuiModel)

	if !m.tut.on {
		t.Fatalf("yes did not start the tutorial: %q", m.status)
	}
	if len(m.tut.steps) < 2 {
		t.Errorf("the run has %d steps, want the pages the file ships", len(m.tut.steps))
	}
	if !m.real.rt.Config().TutorialSeen {
		t.Error("the answer was not remembered")
	}

	saved, err := m.real.rt.Store().Config()
	if err != nil {
		t.Fatal(err)
	}
	if !saved.TutorialSeen {
		t.Error("the answer was not written to the real config file")
	}
}

func TestAnEmptyScriptSaysSoRatherThanStarting(t *testing.T) {
	withTutorialScript(t)

	m := pickModel(t)
	next, _ := m.startTutorial()
	m = next.(tuiModel)

	if m.tut.on {
		t.Error("a tutorial with no pages started anyway")
	}
	if !strings.Contains(m.status, "no pages") {
		t.Errorf("status = %q, want it to say the script is empty", m.status)
	}
}

func TestAFactoryResetAsksAgain(t *testing.T) {
	m := settingsModel(t)
	m.cfg().TutorialSeen = true
	m.setSection, m.setFocus, m.setCursor = secReset, focusContent, factoryResetRow
	m.resetArmed = true

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'y', Text: "y"}))
	if !mm.(tuiModel).askTutorial {
		t.Error("a factory reset left the tutorial marked as seen")
	}
}

func TestTheSettingsSectionListsTheTutorials(t *testing.T) {
	m := settingsModel(t)
	m.width = 180
	m.setSection, m.setFocus, m.setCursor = secTutorial, focusContent, 0
	m = m.enteredSection()

	got := plain(m.viewSettings())
	if !strings.Contains(got, "Placeholder Tour") {
		t.Errorf("the Tutorial section does not list the shipped tutorial:\n%s", got)
	}
	if !strings.Contains(got, tutorial.DirName) {
		t.Errorf("the section does not say where tutorials live:\n%s", got)
	}

	withTutorialScript(t, tutorialStep{lines: []string{"hello"}, box: true})
	t.Cleanup(func() { app.Fixture = nil })

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if !mm.(tuiModel).tut.on {
		t.Errorf("enter on the Tutorial row did not start it: %q", mm.(tuiModel).status)
	}
}

func TestTheStopKeyDoesItsOwnJobOnTheWayPast(t *testing.T) {
	m := pickModel(t)
	m = scriptedTutorial(m,
		tutorialStep{lines: []string{"press / to filter"}, keys: []string{"/"}, stop: "/"},
		tutorialStep{lines: []string{"type a name"}, keys: []string{tutorial.Typing}, stop: "enter"},
	)

	if m.tut.at != 0 {
		t.Fatalf("tutorial starts at step %d, want the first", m.tut.at+1)
	}

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: '/', Text: "/"}))
	m = mm.(tuiModel)
	if !m.filterOn {
		t.Error("the stop key did not reach the page: / was in its keys")
	}
	if m.tut.at != 1 {
		t.Fatalf("after / the tutorial is on step %d, want 2", m.tut.at+1)
	}

	m = typeString(t, m, "s0100")
	if got := m.filter.Value(); got != "s0100" {
		t.Errorf("the filter holds %q, want the whole name", got)
	}
	if !m.tut.on {
		t.Fatal("typing ended the page; only its stop key should")
	}

	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if got := mm.(tuiModel); got.tut.on {
		t.Errorf("on step %d, want enter to have ended the last page", got.tut.at+1)
	}
}

func TestAStopKeyTheKeysDoNotNameIsTakenByTheTutorial(t *testing.T) {
	m := pickModel(t)
	m = scriptedTutorial(m,
		tutorialStep{lines: []string{"read this"}, stop: "enter"},
		tutorialStep{lines: []string{"and this"}, stop: "enter"},
	)

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = mm.(tuiModel)
	if m.tut.at != 1 {
		t.Fatalf("enter left the page on step %d, want step 2", m.tut.at+1)
	}
	if m.phase != phasePicking {
		t.Error("enter reached the element list; a stop key the page does not allow is the tutorial's")
	}
}

func TestPagesBecomeStepsAndHiddenOnesAreGone(t *testing.T) {
	set, err := tutorial.Parse([]byte(`
pages:
  - name: welcome
    box: yes
    text: [hello]
  - name: hidden
    show: no
    text: [never seen]
    keys: [/]
  - name: list
    text: [press enter]
    keys: [enter]
`), "test")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	steps := stepsFrom(set.Shown())
	if len(steps) != 2 {
		t.Fatalf("got %d steps, want the two visible pages", len(steps))
	}
	if !steps[0].box || steps[0].lines[0] != "hello" {
		t.Errorf("first step = %+v, want the welcome box", steps[0])
	}
	if got := steps[1].stop; got != "enter" {
		t.Errorf("stop = %q, want a page with no stop: of its own to end on enter", got)
	}
	if !pickModel(t).stepAllows(steps[1], "enter") {
		t.Error("the page names enter among its keys, so it should reach the list")
	}
}

func TestTheBarKeepsTheFrameHeightAndNamesTheStep(t *testing.T) {
	m := sized(t, pickModel(t), 100, 30)
	plainFrame := len(strings.Split(m.View().Content, "\n"))

	m = scriptedTutorial(m,
		tutorialStep{lines: []string{"press / to filter"}},
		tutorialStep{lines: []string{"and then enter"}},
	)
	frame := m.View().Content

	if got := len(strings.Split(frame, "\n")); got != plainFrame {
		t.Errorf("the frame is %d lines with the tutorial up, %d without; the bar must take reserved height",
			got, plainFrame)
	}
	got := plain(frame)
	if !strings.Contains(got, "step 1 of 2") {
		t.Errorf("the bar does not say where you are:\n%s", got)
	}
	if !strings.Contains(got, "press / to filter") {
		t.Errorf("the bar does not say what to do:\n%s", got)
	}
	if !strings.Contains(got, tutorialQuitKey) {
		t.Errorf("the bar never offers a way out:\n%s", got)
	}
}

func TestTheBarSitsAboveTheFooterNotOverIt(t *testing.T) {
	m := sized(t, pickModel(t), 100, 30)
	m = scriptedTutorial(m, tutorialStep{lines: []string{"press / to filter"}})

	lines := strings.Split(plain(m.View().Content), "\n")
	bar, footer := -1, -1
	for i, l := range lines {
		if strings.Contains(l, "step 1 of 1") {
			bar = i
		}
		if strings.Contains(l, "/ filter") {
			footer = i
		}
	}
	if bar < 0 || footer < 0 {
		t.Fatalf("bar at %d, footer at %d; both should be on screen:\n%s", bar, footer, strings.Join(lines, "\n"))
	}
	if bar > footer {
		t.Errorf("the bar is at line %d, below the footer at %d", bar, footer)
	}
}

func TestOnlyABoxTakesEveryKey(t *testing.T) {
	m := pickModel(t)
	m = scriptedTutorial(m,
		openingStep("welcome"),
		tutorialStep{lines: []string{"press / to filter"}, keys: []string{"/"}, stop: "/"},
	)

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: '/', Text: "/"}))
	m = mm.(tuiModel)
	if m.filterOn {
		t.Error("/ reached the element list while the opening box was up")
	}
	if m.tut.at != 0 {
		t.Error("a key the box held still advanced the tutorial")
	}

	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = mm.(tuiModel)
	if m.tut.at != 1 {
		t.Fatalf("enter left the box on step %d, want step 2", m.tut.at+1)
	}

	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: '/', Text: "/"}))
	if !mm.(tuiModel).filterOn {
		t.Error("/ did not reach the element list once the box was gone")
	}
}

func TestAStepHoldsEveryKeyButTheOnesItNames(t *testing.T) {
	m := pickModel(t)
	m = scriptedTutorial(m,
		tutorialStep{lines: []string{"press / to filter"}, keys: []string{"/"}, stop: "/"},
		tutorialStep{
			lines: []string{"type part of a name"},
			keys:  []string{tutorial.Typing},
			stop:  "enter",
		},
	)

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mm.(tuiModel)
	if m.phase != phasePicking {
		t.Errorf("esc took the user off the element list; the step named only /")
	}

	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: '/', Text: "/"}))
	m = mm.(tuiModel)
	if !m.filterOn || m.tut.at != 1 {
		t.Fatalf("/ left the filter %v on step %d, want it open on step 2", m.filterOn, m.tut.at+1)
	}

	mode := m.mode()
	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'm', Mod: tea.ModAlt}))
	m = mm.(tuiModel)
	if m.mode() != mode {
		t.Errorf("mode = %v, want alt+m held on a step that names only typing", m.mode())
	}
	if got := m.filter.Value(); got != "" {
		t.Errorf("a held chord reached the filter as %q", got)
	}

	m = typeString(t, m, "s0100")
	if got := m.filter.Value(); got != "s0100" {
		t.Errorf("typing did not reach the filter, which holds %q", got)
	}
}

func TestTypingAndThenEnterAreTwoDifferentSteps(t *testing.T) {
	m := pickModel(t)
	m.filterOn = true
	m.filter.Focus()
	m = scriptedTutorial(m,
		tutorialStep{
			lines: []string{"type part of a name, then press enter"},
			keys:  []string{tutorial.Typing, "backspace", "enter"},
			stop:  "enter",
		},
		tutorialStep{lines: []string{"and now tick a row"}, keys: []string{"space"}, stop: "space"},
	)

	m = typeString(t, m, "s0100")
	if m.tut.at != 0 {
		t.Fatalf("on step %d after typing, want to still be on the first", m.tut.at+1)
	}
	if got := m.filter.Value(); got != "s0100" {
		t.Errorf("the filter holds %q; the rest of the typing was eaten", got)
	}

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = mm.(tuiModel)
	if m.filterOn {
		t.Error("enter did not close the filter: a stop key in keys: still does its own job")
	}
	if m.tut.at != 1 {
		t.Errorf("on step %d after enter, want the second", m.tut.at+1)
	}
}

func TestAHeldStepStillQuitsAndStillAdvances(t *testing.T) {
	m := pickModel(t)
	m = scriptedTutorial(m, tutorialStep{
		lines: []string{"press / to filter"},
		keys:  []string{"/"},
		stop:  "/",
	})

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'x', Mod: tea.ModCtrl}))
	if got := mm.(tuiModel); got.tut.on {
		t.Error("ctrl+x did not leave a step that holds the keyboard")
	}
}

func TestTheBoxIsDrawnWithThePagesOwnWords(t *testing.T) {
	m := sized(t, pickModel(t), 100, 30)
	m = scriptedTutorial(m, openingStep("nothing here touches a real ION"))

	got := plain(m.View().Content)
	if !strings.Contains(got, "nothing here touches a real ION") {
		t.Errorf("the box is missing the step's text:\n%s", got)
	}
	if !strings.Contains(got, "enter finish") {
		t.Errorf("the last step should offer to finish:\n%s", got)
	}
}

func TestQuittingRestoresTheFullHeight(t *testing.T) {
	m := sized(t, pickModel(t), 100, 30)
	full := m.height

	m = scriptedTutorial(m, tutorialStep{lines: []string{"press / to filter"}})
	if m.height != full-tutorialBarLines {
		t.Errorf("height = %d while the tutorial runs, want %d", m.height, full-tutorialBarLines)
	}

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'x', Mod: tea.ModCtrl}))
	m = mm.(tuiModel)
	if m.tut.on {
		t.Fatalf("%s did not end the tutorial", tutorialQuitKey)
	}
	if m.height != full {
		t.Errorf("height = %d after leaving, want the full %d back", m.height, full)
	}
}

func TestAResizeKeepsTheBarsReservation(t *testing.T) {
	m := pickModel(t)
	m = scriptedTutorial(m, tutorialStep{lines: []string{"press / to filter"}})

	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = mm.(tuiModel)

	if m.termHeight != 40 {
		t.Errorf("termHeight = %d, want 40", m.termHeight)
	}
	if m.height != 40-tutorialBarLines {
		t.Errorf("height = %d, want %d", m.height, 40-tutorialBarLines)
	}
}

func runTenantLoads(t *testing.T, m tuiModel, cmd tea.Cmd) tuiModel {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command to run")
	}
	msgs := []tea.Msg{cmd()}
	if batch, ok := msgs[0].(tea.BatchMsg); ok {
		msgs = nil
		for _, c := range batch {
			if c != nil {
				msgs = append(msgs, c())
			}
		}
	}
	loaded := 0
	for _, msg := range msgs {
		els, ok := msg.(tenantElementsMsg)
		if !ok {
			continue
		}
		loaded++
		next, _ := m.Update(els)
		m = next.(tuiModel)
	}
	if loaded == 0 {
		t.Fatalf("none of the %d commands loaded a tenant", len(msgs))
	}
	return m
}

func withTutorialScript(t *testing.T, steps ...tutorialStep) {
	t.Helper()
	withTutorialFlows(t, tutorialFlow{name: "Test", steps: steps})
}

func withTutorialFlows(t *testing.T, flows ...tutorialFlow) {
	t.Helper()
	real := tutorialScript
	tutorialScript = func(*config.Store) ([]tutorialFlow, error) { return flows, nil }
	t.Cleanup(func() { tutorialScript = real })
}

func TestStartingTheTutorialBorrowsAFakeFleet(t *testing.T) {
	withTutorialScript(t, tutorialStep{lines: []string{"look at the list"}})
	t.Cleanup(func() { app.Fixture = nil })

	m := pickModel(t)
	realStore, realCfg := m.store(), m.cfg()
	realElements := len(m.rows())

	next, cmd := m.startTutorial()
	m = next.(tuiModel)

	if app.Fixture == nil {
		t.Fatal("the tutorial did not swap in the fixture set")
	}
	if m.store() == realStore {
		t.Error("the tutorial is writing to the real config root")
	}
	if m.cfg() == realCfg {
		t.Error("the tutorial is using the real tenant list")
	}
	if m.tenant().Name != "Acme Co" {
		t.Errorf("tenant = %q, want the fixture tenant", m.tenant().Name)
	}
	if m.flags.IONPassword == "" {
		t.Error("no device password for the fake tenant, so a shell step could not log in")
	}

	m = runTenantLoads(t, m, cmd)
	if len(m.rows()) < 6 {
		t.Fatalf("the fake fleet loaded %d elements, want the six in tutorial.yaml", len(m.rows()))
	}

	var names []string
	for _, el := range m.rows() {
		names = append(names, el.Name)
	}
	if got := strings.Join(names, " "); !strings.Contains(got, "store-0003-rtr-01") {
		t.Errorf("elements = %q, want the tutorial fleet", got)
	}

	next, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'x', Mod: tea.ModCtrl}))
	m = next.(tuiModel)

	if app.Fixture != nil {
		t.Error("leaving the tutorial left the fixture set in place")
	}
	if m.store() != realStore || m.cfg() != realCfg {
		t.Error("the real store and config did not come back")
	}
	if len(m.rows()) != realElements {
		t.Errorf("%d elements after leaving, want the real %d back", len(m.rows()), realElements)
	}
	if m.real.held {
		t.Error("the saved world is still being held")
	}
}

func TestTheTutorialWillNotStartOverAnOpenSession(t *testing.T) {
	withTutorialScript(t, tutorialStep{lines: []string{"anything"}})
	t.Cleanup(func() { app.Fixture = nil })

	m, _ := favModel(t)
	next, _ := m.startTutorial()
	m = next.(tuiModel)

	if m.tut.on {
		t.Error("the tutorial started while a real session was open")
	}
	if app.Fixture != nil {
		t.Fatal("it swapped the fixture set in under a live session")
	}
	if !strings.Contains(m.status, "close the open sessions") {
		t.Errorf("status = %q, want it to say why not", m.status)
	}
}

func TestEvenAFixturesRunGetsTheBlankTutorialWorld(t *testing.T) {
	withTutorialScript(t, tutorialStep{lines: []string{"anything"}})

	m, set := fixtureTUI(t)
	launched := m.tenant()

	next, _ := m.startTutorial()
	m = next.(tuiModel)

	if !m.tut.on {
		t.Fatalf("the tutorial did not start in a fixtures run: %q", m.status)
	}
	if app.Fixture == set {
		t.Error("the tour ran against the fixtures the run was launched with, not its own blank world")
	}
	if m.tenant().Name != "Acme Co" {
		t.Errorf("tenant = %q, want the tutorial's own", m.tenant().Name)
	}
	if m.real.fixture != set {
		t.Error("the launched fixture set was not held for afterwards")
	}

	next, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'x', Mod: tea.ModCtrl}))
	m = next.(tuiModel)

	if app.Fixture != set {
		t.Error("the fixtures the run was launched with did not come back")
	}
	if m.tenant().Name != launched.Name {
		t.Errorf("tenant = %q after leaving, want %q back", m.tenant().Name, launched.Name)
	}
}

func TestAYAMLFileInTheConfigRootReplacesTheShippedPages(t *testing.T) {
	m := pickModel(t)
	path := filepath.Join(m.store().Root, tutorial.FileName)
	if err := os.WriteFile(path, []byte(`
pages:
  - name: welcome
    box: yes
    text: [written in yaml]
  - name: hidden
    show: no
    text: [never]
    keys: [/]
`), 0o600); err != nil {
		t.Fatal(err)
	}

	flows, err := tutorialScript(m.store())
	if err != nil {
		t.Fatalf("tutorialScript() error = %v", err)
	}
	if len(flows) != 1 {
		t.Fatalf("got %d tutorials, want the file to have replaced the shipped one", len(flows))
	}
	if len(flows[0].steps) != 1 {
		t.Fatalf("got %d steps, want the one visible page", len(flows[0].steps))
	}
	if flows[0].steps[0].lines[0] != "written in yaml" {
		t.Errorf("step = %+v, want the page from the file", flows[0].steps[0])
	}
	if flows[0].name != "Tutorial" {
		t.Errorf("name = %q, want it taken from the file name", flows[0].name)
	}
}

func TestABrokenTutorialFileIsReportedRatherThanIgnored(t *testing.T) {
	m := settingsModel(t)
	dir := filepath.Join(m.store().Root, tutorial.DirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("broken.yaml", "pages:\n  - name: p\n    text: [go]\n    go: nonsense\n")
	write("main-workflow.yaml", "pages:\n  - name: p\n    text: [go]\n")

	m.setSection, m.setFocus, m.setCursor = secTutorial, focusContent, 0
	m = m.enteredSection()

	if !strings.Contains(m.flowsErr, "nonsense") {
		t.Errorf("flowsErr = %q, want the page and the bad place named", m.flowsErr)
	}
	if len(m.flows) != 1 || m.flows[0].name != "Main Workflow" {
		t.Errorf("flows = %+v, want the one that parses, named after its file", m.flows)
	}
	if got := plain(m.viewSettings()); !strings.Contains(got, "nonsense") {
		t.Errorf("the section does not name the file it could not read:\n%s", got)
	}
}

func TestEveryFileInTheFolderIsATutorialOfItsOwn(t *testing.T) {
	m := settingsModel(t)
	dir := filepath.Join(m.store().Root, tutorial.DirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"1-main-workflow.yaml": "name: Main Workflow\npages:\n  - name: a\n    text: [one]\n",
		"2-upgrades.yaml":      "pages:\n  - name: b\n    text: [two]\n  - name: c\n    text: [three]\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	m.setSection, m.setFocus, m.setCursor = secTutorial, focusContent, 0
	m = m.enteredSection()

	if len(m.flows) != 2 {
		t.Fatalf("got %d tutorials, want one per file", len(m.flows))
	}
	if m.flows[0].name != "Main Workflow" || m.flows[1].name != "2 Upgrades" {
		t.Errorf("names = %q and %q, want the name: line then the file name",
			m.flows[0].name, m.flows[1].name)
	}
	if len(m.flows[1].steps) != 2 {
		t.Errorf("the second tutorial has %d steps, want its own two", len(m.flows[1].steps))
	}

	got := plain(m.viewSettings())
	for _, want := range []string{"Main Workflow", "2 Upgrades", "1 page", "2 pages"} {
		if !strings.Contains(got, want) {
			t.Errorf("the section does not list %q:\n%s", want, got)
		}
	}

	t.Cleanup(func() { app.Fixture = nil })
	m.setCursor = 1
	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if got := next.(tuiModel); got.tut.name != "2 Upgrades" {
		t.Errorf("enter started %q, want the row under the cursor", got.tut.name)
	}
}

func TestTheAskBoxSaysOnlyWhatItWasAskedTo(t *testing.T) {
	m := sized(t, pickModel(t), 100, 30)
	m.runtime().UseConfig(m.store(), &config.Config{})
	m = m.raiseTutorialAsk()

	got := plain(m.View().Content)
	if !strings.Contains(got, "Start Tutorial") {
		t.Errorf("the box lost its title:\n%s", got)
	}
	for _, want := range []string{"yes", "no"} {
		if !strings.Contains(got, want) {
			t.Errorf("the box has no %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"guided", "y yes", "n no"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("the box still says %q:\n%s", unwanted, got)
		}
	}
}

func TestAPageWithNoPredicateWaitsForEnterInTheBar(t *testing.T) {
	m := pickModel(t)
	m = scriptedTutorial(m,
		tutorialStep{lines: []string{"read this"}},
		tutorialStep{lines: []string{"now press /"}, keys: []string{"/"}, stop: "/"},
	)

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m = mm.(tuiModel)
	if m.tut.at != 0 {
		t.Error("a key that is not enter moved past a page the user has to accept")
	}

	mm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = mm.(tuiModel)
	if m.tut.at != 1 {
		t.Fatalf("enter left the page on step %d, want step 2", m.tut.at+1)
	}

	got := plain(m.View().Content)
	if strings.Contains(got, "enter next") {
		t.Errorf("the bar still offers enter on an action page:\n%s", got)
	}
}

func TestTheBarOffersEnterOnlyWhereItIsTheAnswer(t *testing.T) {
	m := sized(t, pickModel(t), 100, 30)
	m = scriptedTutorial(m, tutorialStep{lines: []string{"read this"}})

	if got := plain(m.View().Content); !strings.Contains(got, "enter next") {
		t.Errorf("the bar does not say enter moves on:\n%s", got)
	}
}

func TestAPageCanTakeTheUserSomewhereItself(t *testing.T) {
	m := pickModel(t)
	m.phase = phaseMenu
	m = scriptedTutorial(m,
		tutorialStep{lines: []string{"first"}},
		tutorialStep{lines: []string{"settings, without being asked for"}, begin: tutorialPlaces["settings"]},
	)

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = mm.(tuiModel)

	if m.tut.at != 1 {
		t.Fatalf("the tutorial is on step %d, want 2", m.tut.at+1)
	}
	if m.phase != phaseSettings {
		t.Errorf("phase = %v, want the tutorial to have opened settings itself", m.phase)
	}
}

func TestEveryPlaceTheYAMLAcceptsIsWired(t *testing.T) {
	for _, name := range tutorial.Places {
		if tutorialPlaces[name] == nil {
			t.Errorf("go: %s is offered in the YAML but nothing in the TUI goes there", name)
		}
	}
	for name := range tutorialPlaces {
		if !slices.Contains(tutorial.Places, name) {
			t.Errorf("the TUI goes to %s but the YAML refuses it at load", name)
		}
	}
}

func TestAPageWaitsForTheKeyTheActionIsBoundTo(t *testing.T) {
	m := keysModel(t, "screens:\n  elements:\n    f: filter\n    \"/\": none\n", nil)
	m.phase = phasePicking
	st := tutorialStep{lines: []string{`press {{ key "filter" }} to narrow the list`},
		keys: []string{"filter"}, stop: "filter"}

	if !m.stepAllows(st, "f") {
		t.Error("the page does not take the key the file put the filter on")
	}
	if m.stepAllows(st, "/") {
		t.Error("the page still takes the key the file took back")
	}
	if !m.stepStops(st, "f") {
		t.Error("f does not end a page that stops on the filter")
	}
	if got := m.pageText(st.lines)[0]; got != "press f to narrow the list" {
		t.Errorf("the page says %q, want it to name the key that is bound", got)
	}
}

func TestAPageThatNamesAKeyNobodyCanRebindStillWorks(t *testing.T) {
	m := pickModel(t)
	st := tutorialStep{keys: []string{"enter", tutorial.Typing}, stop: "enter"}

	if !m.stepAllows(st, "enter") || !m.stepStops(st, "enter") {
		t.Error("enter is a key, not an action, and has to go on working as one")
	}
	if !m.stepAllows(st, "x") {
		t.Error("typing stopped standing for a character")
	}
}

func TestAPageWaitingForAnUnboundActionIsNamed(t *testing.T) {
	m := keysModel(t, "screens:\n  elements:\n    alt+r: none\n", nil)
	m.flows = []tutorialFlow{{name: "Main", steps: []tutorialStep{{stop: "refresh"}}}}

	if got := m.unboundPages(); !strings.Contains(got, "refresh") {
		t.Errorf("unboundPages() = %q, want the page and the action named", got)
	}
}
