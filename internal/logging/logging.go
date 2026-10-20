package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"tyrtop.com/iontk/internal/clock"
)

const (
	DirName     = "logs"
	DefaultKeep = 10

	levelOff = slog.Level(32)
)

type Options struct {
	Dir    string
	File   string
	Level  slog.Level
	Keep   int
	Stderr io.Writer
	Now    time.Time
	RunID  string
}

var (
	mu      sync.Mutex
	current = slog.New(slog.DiscardHandler)
	file    *os.File
	path    string
	level   = slog.LevelInfo
	runID   string
	problem error
)

func L() *slog.Logger {
	mu.Lock()
	defer mu.Unlock()
	return current
}

func Path() string {
	mu.Lock()
	defer mu.Unlock()
	return path
}

func Level() slog.Level {
	mu.Lock()
	defer mu.Unlock()
	return level
}

func LevelName() string { return levelName(Level()) }

func RunID() string {
	mu.Lock()
	defer mu.Unlock()
	return runID
}

func Problem() error {
	mu.Lock()
	defer mu.Unlock()
	return problem
}

func On() bool { return Path() != "" }

func Start(opt Options) (*slog.Logger, error) {
	if opt.Now.IsZero() {
		opt.Now = time.Now()
	}
	if opt.Keep <= 0 {
		opt.Keep = DefaultKeep
	}
	if opt.RunID == "" {
		opt.RunID = newRunID()
	}

	if opt.Level == levelOff {
		set(slog.New(slog.DiscardHandler), nil, "", opt, nil)
		return L(), nil
	}

	f, where, err := openFile(opt)
	if err != nil {
		set(slog.New(slog.DiscardHandler), nil, "", opt, err)
		return L(), err
	}

	hs := []slog.Handler{redact(slog.NewJSONHandler(f, &slog.HandlerOptions{Level: opt.Level}))}
	if opt.Stderr != nil {
		hs = append(hs, redact(slog.NewTextHandler(opt.Stderr, &slog.HandlerOptions{Level: opt.Level})))
	}

	log := slog.New(fanout(hs)).With("run", opt.RunID)
	set(log, f, where, opt, nil)
	return log, nil
}

func set(log *slog.Logger, f *os.File, where string, opt Options, err error) {
	mu.Lock()
	defer mu.Unlock()
	if file != nil {
		file.Close()
	}
	current, file, path, level, runID, problem = log, f, where, opt.Level, opt.RunID, err
	slog.SetDefault(log)
}

func openFile(opt Options) (*os.File, string, error) {
	where := opt.File
	if where == "" {
		if opt.Dir == "" {
			return nil, "", fmt.Errorf("no directory to log to")
		}
		where = filepath.Join(opt.Dir, "iontk-"+opt.Now.Format("20060102-150405")+".log")
	}
	dir := filepath.Dir(where)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", fmt.Errorf("create %s: %w", dir, err)
	}
	prune(dir, opt.Keep-1)

	f, err := os.OpenFile(where, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, "", fmt.Errorf("open %s: %w", where, err)
	}
	return f, where, nil
}

func prune(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "iontk-") && strings.HasSuffix(e.Name(), ".log") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if keep < 0 {
		keep = 0
	}
	for i := 0; i < len(names)-keep; i++ {
		os.Remove(filepath.Join(dir, names[i]))
	}
}

func Close() error {
	mu.Lock()
	defer mu.Unlock()
	err := error(nil)
	if file != nil {
		err = file.Close()
	}
	current, file, path, problem = slog.New(slog.DiscardHandler), nil, "", nil
	level, runID = slog.LevelInfo, ""
	slog.SetDefault(current)
	return err
}

const tailWindow = 64 << 10

func Tail(n int) []string {
	where := Path()
	if where == "" || n <= 0 {
		return nil
	}
	f, err := os.Open(where)
	if err != nil {
		return nil
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil
	}
	at := int64(0)
	if fi.Size() > tailWindow {
		at = fi.Size() - tailWindow
	}
	b, err := io.ReadAll(io.NewSectionReader(f, at, fi.Size()-at))
	if err != nil {
		return nil
	}

	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error", "err":
		return slog.LevelError, nil
	case "off", "none":
		return levelOff, nil
	}
	return 0, fmt.Errorf("unknown log level %q: want one of %s", s, strings.Join(LevelNames(), ", "))
}

func LevelNames() []string { return []string{"debug", "info", "warn", "error", "off"} }

func levelName(l slog.Level) string {
	if l == levelOff {
		return "off"
	}
	return strings.ToLower(l.String())
}

func newRunID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%08x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

type fanout []slog.Handler

func (f fanout) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range f {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (f fanout) Handle(ctx context.Context, rec slog.Record) error {
	var err error
	for _, h := range f {
		if !h.Enabled(ctx, rec.Level) {
			continue
		}
		if e := h.Handle(ctx, rec.Clone()); e != nil && err == nil {
			err = e
		}
	}
	return err
}

func (f fanout) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make(fanout, len(f))
	for i, h := range f {
		out[i] = h.WithAttrs(attrs)
	}
	return out
}

func (f fanout) WithGroup(name string) slog.Handler {
	out := make(fanout, len(f))
	for i, h := range f {
		out[i] = h.WithGroup(name)
	}
	return out
}

func Recent(n int) []string {
	lines := Tail(n)
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, summarize(line))
	}
	return out
}

func summarize(line string) string {
	var rec struct {
		Time  time.Time `json:"time"`
		Level string    `json:"level"`
		Msg   string    `json:"msg"`
	}
	if err := json.Unmarshal([]byte(line), &rec); err != nil || rec.Msg == "" {
		return line
	}
	return rec.Time.In(clock.Zone()).Format("15:04:05 MST") + "  " +
		strings.ToLower(rec.Level) + "  " + rec.Msg
}
