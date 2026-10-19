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
	FormatTable = "table"
	FormatCSV   = "csv"
)

var outputFormats = []string{FormatJSON, FormatJSONL, FormatText, FormatTable, FormatCSV}

func FormatHelp(f string) string {
	switch f {
	case FormatJSONL:
		return "one JSON object per line, written as each element finishes"
	case FormatText:
		return "plain text, grouped by element"
	case FormatTable:
		return "one row per command, output flattened to a single line"
	case FormatCSV:
		return "one row per command, quoted per RFC 4180"
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
	case FormatTable:
		return "txt"
	case FormatCSV:
		return "csv"
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
	case FormatTable, "tbl":
		return FormatTable, nil
	case FormatCSV:
		return FormatCSV, nil
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

// Tabular is asked before offering an orientation control: on the other
// formats it would change nothing.
func Tabular(f string) bool { return f == FormatTable || f == FormatCSV }

// Sink receives each element's result as it finishes and writes it out.
// The streaming formats write immediately; json and the tabular formats
// buffer, because an array has to keep the order the elements were given in
// and a row sort cannot start until every row exists.
type Sink interface {
	Add(index int, r sase.Result) error
	Close() error
}

// Only the tabular formats read these, so a caller can pass them without
// knowing which format it ended up with.
type Option func(*sinkOpts)

type sinkOpts struct {
	orient string
	width  int
}

func WithOrientation(o string) Option { return func(s *sinkOpts) { s.orient = o } }

// WithWidth fits table rows into n columns. Zero leaves every cell whole,
// which is what a file wants -- truncating one would lose the only copy.
func WithWidth(n int) Option { return func(s *sinkOpts) { s.width = n } }

func NewSink(w io.Writer, format string, opts ...Option) Sink {
	o := sinkOpts{orient: OrientION}
	for _, apply := range opts {
		apply(&o)
	}
	if orient, err := NormalizeOrientation(o.orient); err == nil {
		o.orient = orient
	}

	switch format {
	case FormatJSONL:
		return &jsonlSink{enc: json.NewEncoder(w)}
	case FormatText:
		return &textSink{w: w}
	case FormatTable:
		return &tableSink{w: w, opts: o}
	case FormatCSV:
		return &csvSink{w: w, opts: o}
	default:
		return &jsonSink{w: w}
	}
}

type jsonSink struct {
	w       io.Writer
	results []sase.Result
}

func (s *jsonSink) Add(index int, r sase.Result) error {
	s.results = addAt(s.results, index, r)
	return nil
}

// addAt exists because results arrive in completion order and have to be
// written in the order the elements were given.
func addAt(results []sase.Result, index int, r sase.Result) []sase.Result {
	for len(results) <= index {
		results = append(results, sase.Result{})
	}
	results[index] = r
	return results
}

func (s *jsonSink) Close() error {
	if s.results == nil {
		s.results = []sase.Result{}
	}
	enc := json.NewEncoder(s.w)
	// Command output stays \n-escaped inside the JSON strings whatever the
	// indentation, which is why text and table exist.
	enc.SetIndent("", "  ")
	return enc.Encode(s.results)
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
