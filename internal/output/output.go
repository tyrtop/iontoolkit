package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tyrtop.com/iontk/internal/sase"
)

const (
	FormatJSON  = "json"
	FormatJSONL = "jsonl"
	FormatText  = "text"
)

var outputFormats = []string{FormatJSON, FormatJSONL, FormatText}

func FormatHelp(f string) string {
	switch f {
	case FormatJSONL:
		return "one JSON object per line, written as each element finishes"
	case FormatText:
		return "plain text, grouped by element"
	default:
		return "a single JSON array, written when every element is done"
	}
}

func formatExt(f string) string {
	switch f {
	case FormatJSONL:
		return "jsonl"
	case FormatText:
		return "txt"
	default:
		return "json"
	}
}

func NormalizeFormat(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", FormatJSON:
		return FormatJSON, nil
	case FormatJSONL, "ndjson":
		return FormatJSONL, nil
	case FormatText, "txt", "plain":
		return FormatText, nil
	}
	return "", fmt.Errorf("unknown output format %q: want one of %s", s, strings.Join(outputFormats, ", "))
}

func NextFormat(f string) string {
	for i, name := range outputFormats {
		if name == f {
			return outputFormats[(i+1)%len(outputFormats)]
		}
	}
	return FormatJSON
}

// Sink receives each element's result as it finishes and writes it out.
// The streaming formats write immediately; json buffers, because an array has
// to keep the order the elements were given in.
type Sink interface {
	Add(index int, r sase.Result) error
	Close() error
}

func NewSink(w io.Writer, format string) Sink {
	switch format {
	case FormatJSONL:
		return &jsonlSink{enc: json.NewEncoder(w)}
	case FormatText:
		return &textSink{w: w}
	default:
		return &jsonSink{w: w}
	}
}

type jsonSink struct {
	w       io.Writer
	results []sase.Result
}

func (s *jsonSink) Add(index int, r sase.Result) error {
	for len(s.results) <= index {
		s.results = append(s.results, sase.Result{})
	}
	s.results[index] = r
	return nil
}

func (s *jsonSink) Close() error {
	if s.results == nil {
		s.results = []sase.Result{}
	}
	return json.NewEncoder(s.w).Encode(s.results)
}

type jsonlSink struct{ enc *json.Encoder }

// Encode already ends each value with a newline, which is the whole format.
func (s *jsonlSink) Add(_ int, r sase.Result) error { return s.enc.Encode(r) }
func (s *jsonlSink) Close() error                   { return nil }

type textSink struct {
	w     io.Writer
	wrote bool
}

func (s *textSink) Add(_ int, r sase.Result) error {
	var b strings.Builder
	if s.wrote {
		b.WriteString("\n")
	}
	s.wrote = true

	name := r.Name
	if name == "" {
		name = r.ElementID
	}
	b.WriteString("=== " + name + " (" + r.ElementID + ") ===\n")

	var meta []string
	if r.Model != "" {
		meta = append(meta, "model "+r.Model)
	}
	if r.Software != "" {
		meta = append(meta, "software "+r.Software)
	}
	if len(meta) > 0 {
		b.WriteString(strings.Join(meta, "  ") + "\n")
	}

	if r.Error != "" {
		b.WriteString("error: " + r.Error + "\n")
		_, err := io.WriteString(s.w, b.String())
		return err
	}

	for _, c := range r.Commands {
		b.WriteString("\n$ " + c.Command + "\n")
		if c.Error != "" {
			b.WriteString("error: " + c.Error + "\n")
			continue
		}
		out := strings.TrimRight(c.Output, "\n")
		if out != "" {
			b.WriteString(out + "\n")
		}
	}

	_, err := io.WriteString(s.w, b.String())
	return err
}

func (s *textSink) Close() error { return nil }

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

// outputName is the file a run writes to when output goes to a folder. The
// timestamp keeps repeated runs from overwriting each other.
func outputName(format string, now time.Time) string {
	return "iontk-" + now.Format("20060102-150405") + "." + formatExt(format)
}

// Open decides where results go. An explicit path wins; "-" and an empty
// folder both mean stdout. It returns the destination and a label to report.
func Open(explicit, dir, format string, now time.Time) (io.WriteCloser, string, error) {
	path := strings.TrimSpace(explicit)
	switch {
	case path == "-":
		return nopCloser{os.Stdout}, "stdout", nil
	case path == "" && strings.TrimSpace(dir) == "":
		return nopCloser{os.Stdout}, "stdout", nil
	case path == "":
		path = filepath.Join(strings.TrimSpace(dir), outputName(format, now))
	}

	if parent := filepath.Dir(path); parent != "" && parent != "." {
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return nil, "", fmt.Errorf("create %s: %w", parent, err)
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, "", fmt.Errorf("open %s: %w", path, err)
	}
	return f, path, nil
}

// ExpandHome resolves a leading ~ so a typed-in folder behaves the way it looks.
func ExpandHome(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", path, err)
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, path[2:]), nil
}
