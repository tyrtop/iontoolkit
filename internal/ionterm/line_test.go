package ionterm

import (
	"context"
	"strings"
	"testing"
)

func promptedSession(t *testing.T) (*Session, *fakeConn) {
	t.Helper()
	d := &fakeDialer{conn: newFakeConn(nil)}
	m := NewManager()
	t.Cleanup(m.CloseAll)

	s, err := m.Open(context.Background(), d, "1", "store-0003-rtr-01", 80, 10, Credentials{})
	if err != nil {
		t.Fatal(err)
	}
	d.conn.feed <- []byte("\r\nstore-0003-rtr-01#")
	eventually(t, "the prompt to reach the screen", func() bool {
		return strings.Contains(plain(s.Render()), "store-0003-rtr-01#")
	})
	return s, d.conn
}

func TestTheLineIsWhatTheDeviceShowsNotWhatWeTyped(t *testing.T) {
	s, conn := promptedSession(t)

	if got := s.Line(); got != "" {
		t.Fatalf("Line() = %q at a bare prompt, want empty", got)
	}

	conn.feed <- []byte("dump interface status all")
	eventually(t, "the recalled command to reach the screen", func() bool {
		return strings.Contains(plain(s.Render()), "dump interface status all")
	})

	if got := s.Line(); got != "dump interface status all" {
		t.Errorf("Line() = %q, want the command on the device's line; it was recalled with the up arrow, so iontk never typed it",
			got)
	}
}

func TestTheLineDropsThePromptHoweverItIsWritten(t *testing.T) {
	tests := []struct {
		name, row, host, want string
	}{
		{"the element's own prompt", "store-0003-rtr-01# dump device status", "store-0003-rtr-01", "dump device status"},
		{"a hostname we do not know", "someone-else# uptime", "store-0003-rtr-01", "uptime"},
		{"a > prompt", "ion> show version", "store-0003-rtr-01", "show version"},
		{"no prompt at all", "  uptime  ", "store-0003-rtr-01", "uptime"},
		{"a bare prompt", "store-0003-rtr-01# ", "store-0003-rtr-01", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := strings.TrimSpace(afterPrompt(tt.row, tt.host)); got != tt.want {
				t.Errorf("afterPrompt(%q) = %q, want %q", tt.row, got, tt.want)
			}
		})
	}
}

func TestTheTypedLineAnswersWhileTheEchoIsStillInFlight(t *testing.T) {
	s, _ := promptedSession(t)

	s.note(0, "uptime")
	if got := s.Line(); got != "uptime" {
		t.Errorf("Line() = %q, want what we typed until the device echoes it", got)
	}
}
