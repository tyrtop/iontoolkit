package ioncli

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/coder/websocket"
)

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

// DefaultPagers are lines a device stops on to wait for a keypress. The text
// is firmware-specific, so these are only the ones we've seen.
var DefaultPagers = []string{
	"press space to continue",
	"press any key to continue",
	"--more--",
	"-- more --",
}

// A pager prompt is whatever comes after the last line break, since the device
// stops there without a newline. The offset matters because the next page
// prints the same prompt text.
func lastLineAt(s string) (string, int) {
	s = strings.TrimRight(s, " \t\b")
	i := strings.LastIndexAny(s, "\r\n") + 1
	return strings.TrimSpace(s[i:]), i
}

// Takes the earliest match in the line, not the first pattern in the list.
// ION 6.3.4 prints "--More--(4%)[Press space to continue, 'q' to quit.]",
// which matches two of them, and cutting the later one left "--More--(4%)[" in
// every paged command's output.
//
// Whatever follows the prompt is the device's own progress and hint, so it goes
// too. Backspaces are the exception: the device erased the prompt in place, and
// what comes after them is the next page.
func stripPager(line string, pagers []string) (string, bool) {
	low := strings.ToLower(line)
	if len(low) != len(line) {
		return line, false // not ASCII, so the offsets would not line up
	}
	start, end := -1, -1
	for _, p := range pagers {
		lp := strings.ToLower(p)
		if p == "" || len(lp) != len(p) {
			continue
		}
		if i := strings.Index(low, lp); i >= 0 && (start < 0 || i < start) {
			start, end = i, i+len(p)
		}
	}
	if start < 0 {
		return line, false
	}
	rest := line[end:]
	if j := strings.LastIndexByte(rest, '\b'); j >= 0 {
		rest = rest[j+1:]
	} else {
		rest = ""
	}
	return strings.Trim(line[:start]+rest, " \t\b"), true
}

func isPager(line string, pagers []string) bool {
	_, hit := stripPager(line, pagers)
	return hit && line != ""
}

func strip(s string) string {
	return ansi.ReplaceAllString(s, "")
}

func clean(out, prompt, cmd string, pagers []string) string {
	s := strip(out)

	marker := prompt + " " + cmd
	if i := strings.LastIndex(s, marker); i >= 0 {
		s = s[i+len(marker):]
	}

	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	lines := strings.Split(s, "\n")
	kept := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], " \t")
		if rest, hit := stripPager(line, pagers); hit {
			if rest == "" {
				// The device erases its own prompt, and by now those carriage
				// returns have turned into blank lines.
				for i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == "" {
					i++
				}
				continue
			}
			line = rest
		}
		kept = append(kept, line)
	}
	s = strings.Join(kept, "\n")

	s = strings.Trim(s, " \b\r\n")
	s = strings.TrimSuffix(s, prompt)
	return strings.Trim(s, " \b\r\n")
}

func readUntil(ctx context.Context, conn *websocket.Conn, pattern string) (string, error) {
	var buf strings.Builder
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return buf.String(), err
		}
		buf.Write(data)
		if strings.Contains(strip(buf.String()), pattern) {
			return buf.String(), nil
		}
	}
}

// A paging device waits for a keypress and never sends the prompt. If nothing
// answers it, any command with more than a screen of output uses up the whole
// session timeout.
func readUntilPrompt(ctx context.Context, conn *websocket.Conn, prompt string, pagers []string) (string, error) {
	var buf strings.Builder
	// The device doesn't erase its prompt right away, and a second space would
	// end up in whatever runs next. Tracked by offset, not by text, because
	// consecutive pages print the same line.
	answeredAt := -1
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return buf.String(), err
		}
		buf.Write(data)

		seen := strip(buf.String())
		if strings.HasSuffix(strings.TrimRight(seen, " \b\r\n"), prompt) {
			return buf.String(), nil
		}

		// The tail only, never the whole buffer. Once a pager line has gone
		// past, a Contains match over the buffer is true from then on.
		last, at := lastLineAt(seen)
		if at > answeredAt && isPager(last, pagers) {
			// A plain space, no carriage return. The device wants a keypress,
			// and a return would run as an empty command.
			if err := conn.Write(ctx, websocket.MessageText, []byte(" ")); err != nil {
				return buf.String(), fmt.Errorf("answering %q: %w", last, err)
			}
			answeredAt = at
		}
	}
}

// What the device had sent when a read gave up. Without it, a paging device
// and a prompt we don't match both look like a device that went quiet.
func tail(raw, redact string) string {
	s := strings.TrimRight(strip(raw), " \b\r\n")
	// Some firmware echoes the password back, and batch errors get written to
	// a report file.
	if redact != "" {
		s = strings.ReplaceAll(s, redact, "***")
	}
	if s == "" {
		return "nothing"
	}
	// A paged read can be a screen or more of output.
	const max = 120
	if r := []rune(s); len(r) > max {
		s = "\u2026" + string(r[len(r)-max:])
	}
	return fmt.Sprintf("%q", s)
}

// RunOptions is one session's worth of settings. A struct rather than more
// positional strings: there were already six and they were all strings.
type RunOptions struct {
	Prompt   string
	User     string
	Pass     string
	Commands []string
	// PagerOff is sent once after login to stop the device paging its output.
	// Empty sends nothing. The syntax differs by firmware and is unproven, so
	// it is a setting rather than a constant.
	PagerOff string
	// Pagers get answered with a space. Nil means DefaultPagers, empty turns
	// it off. Still used when PagerOff is set, since some firmware ignores
	// that.
	Pagers []string
}

func (o RunOptions) pagers() []string {
	if o.Pagers == nil {
		return DefaultPagers
	}
	return o.Pagers
}

func Run(ctx context.Context, conn *websocket.Conn, o RunOptions) ([]CommandOutput, error) {
	prompt, cmds := o.Prompt, o.Commands

	if raw, err := readUntil(ctx, conn, "login: "); err != nil {
		return nil, fmt.Errorf("waiting for login prompt: %w; last saw %s", err, tail(raw, o.Pass))
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte(o.User+"\r")); err != nil {
		return nil, err
	}

	if raw, err := readUntil(ctx, conn, "Password: "); err != nil {
		return nil, fmt.Errorf("waiting for password prompt: %w; last saw %s", err, tail(raw, o.Pass))
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte(o.Pass+"\r")); err != nil {
		return nil, err
	}

	if raw, err := readUntilPrompt(ctx, conn, prompt, o.pagers()); err != nil {
		return nil, fmt.Errorf("waiting for shell: %w; last saw %s", err, tail(raw, o.Pass))
	}

	if o.PagerOff != "" {
		if err := conn.Write(ctx, websocket.MessageText, []byte(o.PagerOff+"\r")); err != nil {
			return nil, fmt.Errorf("write %q: %w", o.PagerOff, err)
		}
		// A firmware that does not know the command answers with an error and
		// its prompt, which is indistinguishable from success here. Whether it
		// worked shows up in the commands that follow.
		if raw, err := readUntilPrompt(ctx, conn, prompt, o.pagers()); err != nil {
			return nil, fmt.Errorf("read after %q: %w; last saw %s", o.PagerOff, err, tail(raw, o.Pass))
		}
	}

	outs := make([]CommandOutput, 0, len(cmds))
	for _, cmd := range cmds {
		if err := conn.Write(ctx, websocket.MessageText, []byte(cmd+"\r")); err != nil {
			return outs, fmt.Errorf("write %q: %w", cmd, err)
		}
		raw, err := readUntilPrompt(ctx, conn, prompt, o.pagers())
		if err != nil {
			return outs, fmt.Errorf("read after %q: %w; last saw %s", cmd, err, tail(raw, o.Pass))
		}
		outs = append(outs, CommandOutput{
			Command: cmd,
			Output:  clean(raw, prompt, cmd, o.pagers()),
		})
	}
	return outs, nil
}
