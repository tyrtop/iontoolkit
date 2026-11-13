package runtime

import (
	"strings"
	"testing"
)

const banner = "****\nUse the config command carefully.\n****"

func screen(parts ...string) []string { return strings.Split(strings.Join(parts, "\n"), "\n") }

func TestAnAnswerIsWhatComesBetweenTheTypedLineAndAFreshPrompt(t *testing.T) {
	login := "login: admin\nPassword:"
	for _, tt := range []struct {
		name   string
		before []string
		text   string
		steps  [][]string
		want   string
		writes []string
	}{
		{
			name:   "a plain answer",
			before: screen(login, banner, "acme-rtr-01#"),
			text:   "dump device status",
			steps:  [][]string{screen(login, banner, "acme-rtr-01# dump device status", "Serial : FAKE0001", "acme-rtr-01#")},
			want:   "Serial : FAKE0001",
		},
		{
			name:   "the banner lands on top of a prompt that came first",
			before: screen(login, "acme-rtr-01#"),
			text:   "debug bw-test src-interface 4",
			steps: [][]string{
				screen(login, "acme-rtr-01# debug bw-test src-interface 4"),
				screen(login, "", banner, "", "acme-rtr-01# debug bw-test src-interface 4"),
				screen(login, "", banner, "", "acme-rtr-01# debug bw-test src-interface 4", "bad SOURCE", "acme-rtr-01#"),
			},
			want: "bad SOURCE",
		},
		{
			name:   "the same line typed a second time",
			before: screen(banner, "acme-rtr-01# uptime", "up 3 days", "acme-rtr-01#"),
			text:   "uptime",
			steps: [][]string{
				screen(banner, "acme-rtr-01# uptime", "up 3 days", "acme-rtr-01# uptime"),
				screen(banner, "acme-rtr-01# uptime", "up 3 days", "acme-rtr-01# uptime", "up 4 days", "acme-rtr-01#"),
			},
			want: "up 4 days",
		},
		{
			name:   "a page that waits for a space",
			before: screen(banner, "acme-rtr-01#"),
			text:   "dump interface status all",
			steps: [][]string{
				screen(banner, "acme-rtr-01# dump interface status all", "port 1 up", "--More--(40%)[Press space to continue, 'q' to quit.]"),
				screen(banner, "acme-rtr-01# dump interface status all", "port 1 up", "--More--(40%)[Press space to continue, 'q' to quit.]"),
				screen(banner, "acme-rtr-01# dump interface status all", "port 1 up", "port 2 down", "acme-rtr-01#"),
			},
			want:   "port 1 up\nport 2 down",
			writes: []string{" "},
		},
		{
			name:   "a fresh prompt without the line on it",
			before: screen(login, "acme-rtr-01#"),
			text:   "config interface 3 shutdown",
			steps: [][]string{
				screen(login, "", banner, "acme-rtr-01#"),
				screen(login, "", banner, "acme-rtr-01#", "acme-rtr-01#"),
			},
		},
		{
			name:   "a typed line long enough to wrap",
			before: screen(banner, "acme-rtr-01#"),
			text:   "debug bw-test src-interface 3 destination=pcm.cgnx.net",
			steps:  [][]string{screen(banner, "acme-rtr-01# debug bw-test src-interface 3 dest", "ination=pcm.cgnx.net", "Final Downlink capacity estimate: 194.25", "acme-rtr-01#")},
			want:   "Final Downlink capacity estimate: 194.25",
		},
		{
			name:   "a line still being echoed",
			before: screen(banner, "acme-rtr-01#"),
			text:   "dump device status",
			steps:  [][]string{screen(banner, "acme-rtr-01# dump dev")},
		},
	} {
		x := newExchange(strings.Join(tt.before, "\n"), "acme-rtr-01#", tt.text, "")
		var got string
		var done bool
		var writes []string
		for _, lines := range tt.steps {
			var write string
			got, done, write = x.step(lines)
			if write != "" {
				writes = append(writes, write)
			}
		}
		if wantDone := tt.want != ""; done != wantDone || got != tt.want {
			t.Errorf("%s: answer %q, done %v; want %q, done %v", tt.name, got, done, tt.want, wantDone)
		}
		if strings.Join(writes, "|") != strings.Join(tt.writes, "|") {
			t.Errorf("%s: wrote %q, want %q", tt.name, writes, tt.writes)
		}
	}
}
