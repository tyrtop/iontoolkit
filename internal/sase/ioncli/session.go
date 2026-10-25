package ioncli

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/coder/websocket"
)

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

type Conn interface {
	Read(ctx context.Context) (websocket.MessageType, []byte, error)
	Write(ctx context.Context, typ websocket.MessageType, p []byte) error
}

var DefaultPagers = []string{
	"press space to continue",
	"press any key to continue",
	"--more--",
	"-- more --",
}

func lastLineAt(s string) (string, int) {
	s = strings.TrimRight(s, " \t\b")
	i := strings.LastIndexAny(s, "\r\n") + 1
	return strings.TrimSpace(s[i:]), i
}

// earliest match: the ION 6.3.4 pager line matches two patterns
func stripPager(line string, pagers []string) (string, bool) {
	low := strings.ToLower(line)
	if len(low) != len(line) {
		return line, false
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

func readUntil(ctx context.Context, conn Conn, pattern string) (string, error) {
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

func readUntilPrompt(ctx context.Context, conn Conn, prompt string, pagers []string) (string, error) {
	var buf strings.Builder
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

		last, at := lastLineAt(seen)
		if at > answeredAt && isPager(last, pagers) {
			// the device wants a keypress; a return runs an empty command
			if err := conn.Write(ctx, websocket.MessageText, []byte(" ")); err != nil {
				return buf.String(), fmt.Errorf("answering %q: %w", last, err)
			}
			answeredAt = at
		}
	}
}

func tail(raw, redact string) string {
	s := strings.TrimRight(strip(raw), " \b\r\n")
	if redact != "" {
		s = strings.ReplaceAll(s, redact, "***")
	}
	if s == "" {
		return "nothing"
	}
	const max = 120
	if r := []rune(s); len(r) > max {
		s = "\u2026" + string(r[len(r)-max:])
	}
	return fmt.Sprintf("%q", s)
}

type RunOptions struct {
	Prompt   string
	User     string
	Pass     string
	Commands []string
	PagerOff string
	Pagers   []string
}

func (o RunOptions) pagers() []string {
	if o.Pagers == nil {
		return DefaultPagers
	}
	return o.Pagers
}

func Run(ctx context.Context, conn Conn, o RunOptions) ([]CommandOutput, error) {
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
