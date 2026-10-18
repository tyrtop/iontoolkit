package ioncli

import "testing"

const testPrompt = "S5032-RTR-01"

func repaint(line string) string {
	return "\x1b[J\x1b[2K\r\x1b[31m" + testPrompt + "\x1b[0m" + line
}

func TestStrip(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"erase display":           {"\x1b[JUp", "Up"},
		"erase line":              {"\x1b[2KUp", "Up"},
		"red then reset":          {"\x1b[31m" + testPrompt + "\x1b[0m", testPrompt},
		"full prompt repaint":     {repaint(" dump"), "\r" + testPrompt + " dump"},
		"256-color":               {"\x1b[38;5;196mX\x1b[0m", "X"},
		"no escapes":              {"plain text", "plain text"},
		"OSC title survives":      {"\x1b]0;S5032\x07Up", "\x1b]0;S5032\x07Up"},
		"charset select survives": {"\x1b(BUp", "\x1b(BUp"},

		"truncated CSI survives": {"Up\x1b[", "Up\x1b["},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := strip(tc.in); got != tc.want {
				t.Errorf("strip(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestClean(t *testing.T) {
	cases := map[string]struct {
		raw, prompt, cmd, want string
	}{
		"real: dump interface status, double repaint": {
			raw: "\x1b[J\x1b[2K\r\x1b[31mS5032-RTR-01#\x1b[0m dump interface status 4" +
				"\x1b[J\x1b[2K\r\x1b[31mS5032-RTR-01#\x1b[0m dump interface status 4\r\n" +
				"Interface\t: 4 \r\nDevice\t\t: e382 \r\nState\t\t: down\r\n\r\n" +
				"\x1b[J\x1b[2K\r\x1b[31mS5032-RTR-01#\x1b[0m  \b",
			prompt: "S5032-RTR-01#",
			cmd:    "dump interface status 4",
			want:   "Interface\t: 4\nDevice\t\t: e382\nState\t\t: down",
		},

		"colored prompt still matches the marker": {
			raw:    repaint(" uptime") + "\r\n5 days\r\n" + repaint("  \b"),
			prompt: testPrompt, cmd: "uptime",
			want: "5 days",
		},
		"empty output": {
			raw:    repaint(" uptime") + "\r\n" + repaint("  \b"),
			prompt: testPrompt, cmd: "uptime",
			want: "",
		},
		"trailing spaces stripped per line": {
			raw:    repaint(" t") + "\r\nVni\t\t: 0   \r\n" + repaint("  \b"),
			prompt: testPrompt, cmd: "t",
			want: "Vni\t\t: 0",
		},

		"marker miss leaves echo in output": {
			raw:    "dump interface status 4\r\nUp\r\n" + testPrompt + "  \b",
			prompt: testPrompt, cmd: "dump interface status 4",
			want: "dump interface status 4\nUp",
		},

		"bare CR becomes a newline": {
			raw:    repaint(" t") + "\r\nworking...\rworking... done\r\n" + repaint("  \b"),
			prompt: testPrompt, cmd: "t",
			want: "working...\nworking... done",
		},

		"mid-string backspace survives": {
			raw:    repaint(" t") + "\r\nabc\b\bxy\r\n" + repaint("  \b"),
			prompt: testPrompt, cmd: "t",
			want: "abc\b\bxy",
		},

		"marker inside output truncates": {
			raw: repaint(" show log") + "\r\nfirst line\r\n" +
				testPrompt + " show log\r\nlast line\r\n" + repaint("  \b"),
			prompt: testPrompt, cmd: "show log",
			want: "last line",
		},

		"output ending in prompt text is kept": {
			raw:    repaint(" t") + "\r\npeer " + testPrompt + "\r\n" + repaint("  \b"),
			prompt: testPrompt, cmd: "t",
			want: "peer " + testPrompt,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := clean(tc.raw, tc.prompt, tc.cmd)
			if got != tc.want {
				t.Errorf("clean()\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}
