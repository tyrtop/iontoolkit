package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/coder/websocket"

	"tyrtop.com/iontk/internal/ionterm"
	"tyrtop.com/iontk/internal/sase/api"
)

func TestRowStatusMarksTabsAndDials(t *testing.T) {
	m := pickModel(t)
	setTestClient(&m, liveClient())
	m.mergeTenantElements("1", "t", []api.Element{
		{ID: "open", Name: "has-a-tab"},
		{ID: "dialing", Name: "connecting"},
		{ID: "idle", Name: "nothing-doing"},
	})
	if _, err := m.sessions.Open(t.Context(), stubDialer{}, "open", "has-a-tab", 200, 50, ionterm.Credentials{}); err != nil {
		t.Fatal(err)
	}
	m.markDialing("dialing")

	rows := map[string]string{}
	for _, el := range m.all {
		rows[el.ID] = plain(m.rowStatus(el))
	}
	if got := rows["open"]; !strings.Contains(got, "●") {
		t.Errorf("rowStatus for an element with a tab = %q, want an open marker", got)
	}
	if got := strings.TrimSpace(rows["idle"]); got != "" {
		t.Errorf("rowStatus for an untouched element = %q, want blank", got)
	}
	if got := strings.TrimSpace(rows["dialing"]); got == "" || got == "●" {
		t.Errorf("rowStatus while dialling = %q, want a spinner frame", got)
	}
}

func TestTheStatusColumnHasNoHeader(t *testing.T) {
	m := pickModel(t)
	m.rebuildRows()

	cols := m.table.Columns()
	if len(cols) < 2 {
		t.Fatalf("got %d columns, want the status column added", len(cols))
	}
	if cols[1].Title != "" {
		t.Errorf("column 1 title = %q, want no header", cols[1].Title)
	}
	for i, r := range m.table.Rows() {
		if len(r) != len(cols) {
			t.Fatalf("row %d has %d cells, want %d", i, len(r), len(cols))
		}
	}
}

func TestSpinnerTicksOnlyWhileSomethingIsDialling(t *testing.T) {
	m := pickModel(t)
	if m.connectingAny() {
		t.Error("connectingAny() = true with nothing in flight")
	}

	m.markDialing("e1")
	if !m.connectingAny() {
		t.Error("connectingAny() = false while a dial is in flight")
	}

	m.dialFailed("e1", "nope")
	if m.connectingAny() {
		t.Error("connectingAny() = true for a dial that already failed")
	}

	m.dialDone("e1")
	if m.connectingAny() {
		t.Error("connectingAny() = true once the dial finished")
	}
}

func TestConnectBarReportsTheRetryAndWhetherTheIONIsOnline(t *testing.T) {
	m := pickModel(t)
	setTestClient(&m, liveClient())
	m.mergeTenantElements("1", "t", []api.Element{{ID: "e1", Name: "rtr", Connected: true}})
	if _, err := m.sessions.Open(t.Context(), stubDialer{}, "e1", "rtr", 200, 50, ionterm.Credentials{}); err != nil {
		t.Fatal(err)
	}
	m.noteDial(dialProgressMsg{eid: "e1", attempt: 2, of: 3, err: "dial: connection refused"})

	bar := plain(m.connectBar())
	if !strings.Contains(bar, "2/3") {
		t.Errorf("bar = %q, want the attempt count", bar)
	}
	if !strings.Contains(bar, "online") {
		t.Errorf("bar = %q, want the element's reachability", bar)
	}
	if !strings.Contains(bar, "connection refused") {
		t.Errorf("bar = %q, want why the last attempt failed", bar)
	}
}

func TestConnectBarSaysOfflineForAnUnreachableION(t *testing.T) {
	m := pickModel(t)
	setTestClient(&m, liveClient())
	m.mergeTenantElements("1", "t", []api.Element{{ID: "e1", Name: "rtr", Connected: false}})
	if _, err := m.sessions.Open(t.Context(), stubDialer{}, "e1", "rtr", 200, 50, ionterm.Credentials{}); err != nil {
		t.Fatal(err)
	}

	if bar := plain(m.connectBar()); !strings.Contains(bar, "offline") {
		t.Errorf("bar = %q, want it to say the ION is offline", bar)
	}
}

func TestTheConnectBarKeepsItsRow(t *testing.T) {
	m := pickModel(t)
	setTestClient(&m, liveClient())
	if _, err := m.sessions.Open(t.Context(), stubDialer{}, "e1", "rtr", 200, 50, ionterm.Credentials{}); err != nil {
		t.Fatal(err)
	}

	connected := len(strings.Split(m.connectBar(), "\n"))
	m.noteDial(dialProgressMsg{eid: "e1", attempt: 1, of: 3})
	dialling := len(strings.Split(m.connectBar(), "\n"))
	if connected != 1 || dialling != 1 {
		t.Errorf("bar height = %d connected, %d dialling, want 1 either way", connected, dialling)
	}
}

func TestAFailedConnectionAsksWhetherToTryAgain(t *testing.T) {
	m := pickModel(t)
	setTestClient(&m, liveClient())
	m.mergeTenantElements("1", "t", []api.Element{{ID: "e1", Name: "rtr", Connected: true}})

	target := sessionTarget{eid: "e1", name: "rtr", tsg: "1"}
	mm, _ := m.Update(sessionErrMsg{target: target, err: "dial: failed to WebSocket dial: connection refused"})
	got := mm.(tuiModel)

	if got.askRetry.eid != "e1" {
		t.Fatalf("askRetry = %+v, want the failed element", got.askRetry)
	}
	prompt := plain(got.retryPrompt())
	if !strings.Contains(prompt, "unable to connect") {
		t.Errorf("prompt = %q, want it to say what happened", prompt)
	}
	for _, want := range []string{"rtr", "y", "n"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt = %q, want it to contain %q", prompt, want)
		}
	}
}

func TestAnsweringYesDialsTheElementAgain(t *testing.T) {
	m := pickModel(t)
	setTestClient(&m, liveClient())
	m.mergeTenantElements("1", "t", []api.Element{{ID: "e1", Name: "rtr"}})
	m.askRetry = sessionTarget{eid: "e1", name: "rtr", tsg: "1"}
	m.dialFailed("e1", "connection refused")

	mm, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'y', Text: "y"}))
	got := mm.(tuiModel)

	if cmd == nil {
		t.Error("answering yes produced no command, want the element dialled again")
	}
	if got.askRetry.eid != "" {
		t.Errorf("askRetry = %+v, want the prompt dismissed", got.askRetry)
	}
	if st, ok := got.dialing["e1"]; ok && st.failed {
		t.Error("the element is still marked as failed after a retry started")
	}
}

func TestAnsweringNoDismissesThePrompt(t *testing.T) {
	m := pickModel(t)
	setTestClient(&m, liveClient())
	m.phase = phaseSession
	m.askRetry = sessionTarget{eid: "e1", name: "rtr", tsg: "1"}
	m.dialFailed("e1", "connection refused")

	mm, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'n', Text: "n"}))
	got := mm.(tuiModel)

	if got.askRetry.eid != "" {
		t.Errorf("askRetry = %+v, want the prompt gone", got.askRetry)
	}
	if _, ok := got.dialing["e1"]; ok {
		t.Error("the failed dial was left in the list after being dismissed")
	}
	if got.phase != phasePicking {
		t.Errorf("phase = %v, want the element list", got.phase)
	}
}

func TestThePromptOwnsTheKeyboard(t *testing.T) {
	m := pickModel(t)
	setTestClient(&m, liveClient())
	m.askRetry = sessionTarget{eid: "e1", name: "rtr", tsg: "1"}
	before := len(m.selected)

	mm, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: ' ', Text: " "}))
	got := mm.(tuiModel)
	if len(got.selected) != before {
		t.Errorf("selected changed from %d to %d behind the prompt", before, len(got.selected))
	}
	if cmd != nil {
		t.Error("space dialled the element, want only enter and y to answer yes")
	}
	if got.askRetry.eid != "" {
		t.Error("the prompt survived an unrelated keypress, want it dismissed")
	}
}

func TestTheModalIsRenderedOverWhateverIsBehindIt(t *testing.T) {
	m := pickModel(t)
	setTestClient(&m, liveClient())
	m.width, m.height = 100, 30
	m.askRetry = sessionTarget{eid: "e1", name: "rtr", tsg: "1"}

	view := plain(m.View().Content)
	if !strings.Contains(view, "unable to connect") {
		t.Errorf("the view does not show the prompt:\n%s", view)
	}
	if lines := strings.Count(view, "\n"); lines > m.height {
		t.Errorf("view is %d lines, want it clipped to %d", lines, m.height)
	}
}

func TestTheChosenAnswerIsHighlighted(t *testing.T) {
	m := pickModel(t)
	m.askRetry = sessionTarget{eid: "e1", name: "rtr", tsg: "1"}

	const reverse = "\x1b[1;7"

	yesChosen := m.retryChoices()
	if !strings.Contains(yesChosen, reverse) {
		t.Fatalf("no highlight in %q, want the chosen answer marked", yesChosen)
	}
	if i, j := strings.Index(yesChosen, "yes"), strings.Index(yesChosen, reverse); i < j {
		t.Errorf("choices = %q, want yes highlighted by default", yesChosen)
	}

	m.askRetryNo = true
	noChosen := m.retryChoices()
	if yesChosen == noChosen {
		t.Error("the highlight did not move when the answer changed")
	}
	if !strings.Contains(noChosen, reverse) {
		t.Errorf("no highlight in %q once no was chosen", noChosen)
	}
}

func TestTheChoicesKeepTheirWidthWhicheverIsChosen(t *testing.T) {
	m := pickModel(t)
	m.askRetry = sessionTarget{eid: "e1", name: "rtr", tsg: "1"}

	yes := plain(m.retryChoices())
	m.askRetryNo = true
	no := plain(m.retryChoices())

	if len(yes) != len(no) {
		t.Errorf("choices are %d wide with yes chosen and %d with no: %q vs %q", len(yes), len(no), yes, no)
	}
}

func TestNavigatingThePromptDoesNotAnswerIt(t *testing.T) {
	m := pickModel(t)
	setTestClient(&m, liveClient())
	m.askRetry = sessionTarget{eid: "e1", name: "rtr", tsg: "1"}

	for _, key := range []tea.Key{
		{Code: tea.KeyRight},
		{Code: tea.KeyLeft},
		{Code: tea.KeyTab},
		{Code: 'l', Text: "l"},
		{Code: 'h', Text: "h"},
	} {
		mm, cmd := m.Update(tea.KeyPressMsg(key))
		m = mm.(tuiModel)
		if m.askRetry.eid == "" {
			t.Fatalf("%v dismissed the prompt, want it to move between the answers", key)
		}
		if cmd != nil {
			t.Errorf("%v produced a command, want navigation to do nothing else", key)
		}
	}
	if !m.askRetryNo {
		t.Error("the highlight did not track the moves")
	}
}

func TestEnterTakesTheHighlightedAnswer(t *testing.T) {
	tests := []struct {
		name      string
		chooseNo  bool
		wantRetry bool
	}{
		{"yes highlighted", false, true},
		{"no highlighted", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := pickModel(t)
			setTestClient(&m, liveClient())
			m.mergeTenantElements("1", "t", []api.Element{{ID: "e1", Name: "rtr"}})
			m.askRetry = sessionTarget{eid: "e1", name: "rtr", tsg: "1"}
			m.askRetryNo = tt.chooseNo

			mm, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
			got := mm.(tuiModel)

			if got.askRetry.eid != "" {
				t.Error("the prompt survived enter")
			}
			if tt.wantRetry && cmd == nil {
				t.Error("enter on yes produced no command, want the element dialled again")
			}
			if !tt.wantRetry && cmd != nil {
				t.Error("enter on no produced a command, want nothing dialled")
			}
			if got.askRetryNo {
				t.Error("askRetryNo stayed set, so the next prompt would open on no")
			}
		})
	}
}

func TestYAndNWorkWhicheverIsHighlighted(t *testing.T) {
	for _, tt := range []struct {
		key       string
		chooseNo  bool
		wantRetry bool
	}{
		{"y", true, true},
		{"n", false, false},
	} {
		t.Run(tt.key, func(t *testing.T) {
			m := pickModel(t)
			setTestClient(&m, liveClient())
			m.mergeTenantElements("1", "t", []api.Element{{ID: "e1", Name: "rtr"}})
			m.askRetry = sessionTarget{eid: "e1", name: "rtr", tsg: "1"}
			m.askRetryNo = tt.chooseNo

			mm, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: rune(tt.key[0]), Text: tt.key}))
			got := mm.(tuiModel)
			if got.askRetry.eid != "" {
				t.Errorf("%q did not answer the prompt", tt.key)
			}
			if tt.wantRetry != (cmd != nil) {
				t.Errorf("%q: command = %v, want retry = %v", tt.key, cmd != nil, tt.wantRetry)
			}
		})
	}
}

func TestThePromptSaysWhatThePortalThinks(t *testing.T) {
	m := pickModel(t)
	m.mergeTenantElements("1", "t", []api.Element{{ID: "e1", Name: "rtr", Connected: false}})
	m.askRetry = sessionTarget{eid: "e1", name: "rtr", tsg: "1"}

	if got := plain(m.retryPrompt()); !strings.Contains(got, "offline in the portal") {
		t.Errorf("prompt = %q, want it to say what the portal thinks", got)
	}
}

func TestTheConnectBarNamesBothHalves(t *testing.T) {
	tests := []struct {
		name      string
		connected bool
		dialing   bool
		kill      bool
		answered  bool
		want      []string
	}{
		{
			name:      "answering, on an ion the portal agrees about",
			connected: true,
			answered:  true,
			want:      []string{"successfully dialed", "ion online"},
		},
		{
			name:     "answering, on an ion the portal still calls offline",
			answered: true,
			want:     []string{"successfully dialed", "ion offline"},
		},
		{
			name: "socket up and the ion has said nothing",
			want: []string{"waiting for the ion", "ion offline"},
		},
		{
			name:      "dialling, where the portal is the only evidence",
			connected: true,
			dialing:   true,
			want:      []string{"dial 2/3", "ion online"},
		},
		{
			name: "failed, where it says why a retry will not help",
			kill: true,
			want: []string{"failed", "ion offline", "alt+r retry"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := pickModel(t)
			setTestElements(&m, []api.Element{
				{ID: "1", Name: "S0100-RTR-01", Model: "ion 1200", Connected: tt.connected},
			})
			m.phase = phaseSession
			t.Cleanup(m.sessions.CloseAll)

			conn := newRecConn()
			s, err := m.sessions.Open(context.Background(), recDialer{conn}, "1", "S0100-RTR-01", 200, 50, ionterm.Credentials{})
			if err != nil {
				t.Fatal(err)
			}
			if tt.answered {
				conn.feed("S0100-RTR-01# ")
				waitForTUI(t, s.Answered)
			}
			if tt.dialing {
				m.dialing = map[string]dialState{"1": {attempt: 2, of: 3}}
			}
			if tt.kill {
				conn.Close(websocket.StatusAbnormalClosure, "torn")
				waitForTUI(t, func() bool { return s.Err() != nil })
			}

			bar := plain(m.connectBar())
			for _, want := range tt.want {
				if !strings.Contains(bar, want) {
					t.Errorf("bar = %q, want it to say %q", bar, want)
				}
			}
		})
	}
}

func TestTheConnectBarSaysUnknownForAnUntrackedElement(t *testing.T) {
	m := pickModel(t)
	setTestElements(&m, []api.Element{{ID: "9", Name: "elsewhere"}})
	m.phase = phaseSession
	t.Cleanup(m.sessions.CloseAll)

	conn := newRecConn()
	if _, err := m.sessions.Open(context.Background(), recDialer{conn}, "1", "S0100-RTR-01", 200, 50, ionterm.Credentials{}); err != nil {
		t.Fatal(err)
	}
	if bar := plain(m.connectBar()); !strings.Contains(bar, "ion unknown") {
		t.Errorf("bar = %q, want it to admit it does not know", bar)
	}
}
