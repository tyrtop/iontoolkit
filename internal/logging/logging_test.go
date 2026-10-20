package logging

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func start(t *testing.T, opt Options) string {
	t.Helper()
	if opt.Dir == "" && opt.File == "" {
		opt.Dir = t.TempDir()
	}
	log, err := Start(opt)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if log == nil {
		t.Fatal("Start() returned no logger")
	}
	t.Cleanup(func() { Close() })
	return Path()
}

func lines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func TestStartWritesOneFilePerRun(t *testing.T) {
	dir := t.TempDir()
	path := start(t, Options{Dir: dir, Now: time.Date(2026, 10, 20, 9, 14, 3, 0, time.UTC)})

	if want := filepath.Join(dir, "iontk-20261020-091403.log"); path != want {
		t.Errorf("Path() = %q, want %q", path, want)
	}
	L().Info("hello", "tsg", "1436146912")
	if got := lines(t, path); len(got) != 1 || !strings.Contains(got[0], `"msg":"hello"`) {
		t.Errorf("log holds %q, want one record saying hello", got)
	}
}

func TestEveryRecordCarriesTheRunID(t *testing.T) {
	path := start(t, Options{})
	L().Info("first")
	L().With("tsg", "123").Info("second")

	for _, line := range lines(t, path) {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("line %q is not JSON: %v", line, err)
		}
		if got, _ := rec["run"].(string); got != RunID() {
			t.Errorf("run = %q, want %q; interleaved tenants need it to sort by", got, RunID())
		}
	}
}

func TestTheLevelGatesTheFile(t *testing.T) {
	path := start(t, Options{Level: slog.LevelWarn})
	L().Info("quiet")
	L().Warn("loud")

	got := lines(t, path)
	if len(got) != 1 || !strings.Contains(got[0], "loud") {
		t.Errorf("log holds %q, want the warning alone", got)
	}
}

func TestStartKeepsTheLastFewFilesAndDropsTheRest(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"iontk-20261001-100000.log", "iontk-20261002-100000.log",
		"iontk-20261003-100000.log", "iontk-20261004-100000.log",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}

	start(t, Options{Dir: dir, Keep: 3, Now: time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)})

	var logs []string
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".log") {
			logs = append(logs, e.Name())
		}
	}
	want := []string{"iontk-20261003-100000.log", "iontk-20261004-100000.log", "iontk-20261005-100000.log"}
	if strings.Join(logs, ",") != strings.Join(want, ",") {
		t.Errorf("logs = %v, want %v", logs, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "notes.txt")); err != nil {
		t.Errorf("a file that is not a log was removed: %v", err)
	}
}

func TestADirectoryThatCannotBeMadeDoesNotStopTheRun(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Start(Options{Dir: filepath.Join(blocked, "logs")}); err == nil {
		t.Fatal("Start() error = nil, want the directory failure reported")
	}
	t.Cleanup(func() { Close() })

	if Problem() == nil {
		t.Error("Problem() = nil, want the reason kept for the Debug page")
	}
	if Path() != "" {
		t.Errorf("Path() = %q, want empty", Path())
	}
	L().Info("this must not panic")
}

func TestTailReturnsTheLastLines(t *testing.T) {
	start(t, Options{})
	for _, msg := range []string{"one", "two", "three"} {
		L().Info(msg)
	}

	got := Tail(2)
	if len(got) != 2 || !strings.Contains(got[0], "two") || !strings.Contains(got[1], "three") {
		t.Errorf("Tail(2) = %q, want the last two records", got)
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		in      string
		want    slog.Level
		wantErr bool
	}{
		{"", slog.LevelInfo, false},
		{"debug", slog.LevelDebug, false},
		{"INFO", slog.LevelInfo, false},
		{" warn ", slog.LevelWarn, false},
		{"warning", slog.LevelWarn, false},
		{"error", slog.LevelError, false},
		{"off", levelOff, false},
		{"chatty", 0, true},
	}
	for _, tt := range tests {
		got, err := ParseLevel(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseLevel(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			continue
		}
		if err == nil && got != tt.want {
			t.Errorf("ParseLevel(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestOffWritesNoFileAtAll(t *testing.T) {
	dir := t.TempDir()
	if _, err := Start(Options{Dir: dir, Level: levelOff}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { Close() })

	L().Error("not even this")
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("%q holds %d files, want none when logging is off", dir, len(entries))
	}
}

func TestStderrGetsTextWhileTheFileGetsJSON(t *testing.T) {
	var buf strings.Builder
	path := start(t, Options{Stderr: &buf})
	L().Info("dialed", "element", "store-0003-rtr-01")

	if got := buf.String(); !strings.Contains(got, "element=store-0003-rtr-01") {
		t.Errorf("stderr = %q, want a text record", got)
	}
	if got := lines(t, path); !strings.Contains(got[0], `"element":"store-0003-rtr-01"`) {
		t.Errorf("file = %q, want a JSON record", got)
	}
}
