package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/sase"
	"tyrtop.com/iontk/internal/sase/ioncli"
)

func sampleResult(id, name string) sase.Result {
	return sase.Result{
		ElementID: id,
		Name:      name,
		Model:     "ion1200",
		Software:  "6.5.1",
		Connected: true,
		Commands: []ioncli.CommandOutput{
			{Command: "dump interface status", Output: "eth0 up\neth1 down\n"},
		},
	}
}

func TestNormalizeFormat(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", formatJSON, false},
		{"json", formatJSON, false},
		{"JSON", formatJSON, false},
		{"  json  ", formatJSON, false},
		{"jsonl", formatJSONL, false},
		{"ndjson", formatJSONL, false},
		{"text", formatText, false},
		{"txt", formatText, false},
		{"plain", formatText, false},
		{"yaml", "", true},
		{"csv", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := normalizeFormat(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("normalizeFormat(%q) error = nil, want an error", tt.in)
				}
				if !strings.Contains(err.Error(), "json") {
					t.Errorf("error = %q, want it to list the valid formats", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeFormat(%q) error = %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("normalizeFormat(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNextFormatCyclesThroughEveryFormat(t *testing.T) {
	seen := map[string]bool{}
	f := formatJSON
	for range outputFormats {
		seen[f] = true
		f = nextFormat(f)
	}
	if f != formatJSON {
		t.Errorf("cycling returned to %q, want to wrap around to %q", f, formatJSON)
	}
	if len(seen) != len(outputFormats) {
		t.Errorf("cycle visited %d formats, want all %d", len(seen), len(outputFormats))
	}
	if got := nextFormat("nonsense"); got != formatJSON {
		t.Errorf("nextFormat(%q) = %q, want it to fall back to %q", "nonsense", got, formatJSON)
	}
}

func TestFormatExt(t *testing.T) {
	tests := map[string]string{
		formatJSON:  "json",
		formatJSONL: "jsonl",
		formatText:  "txt",
		"nonsense":  "json",
	}
	for in, want := range tests {
		if got := formatExt(in); got != want {
			t.Errorf("formatExt(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJSONSinkKeepsElementOrder(t *testing.T) {
	var buf bytes.Buffer
	sink := newResultSink(&buf, formatJSON)

	// Added out of order, the way elements actually finish.
	if err := sink.Add(2, sampleResult("c", "s0003")); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if err := sink.Add(0, sampleResult("a", "s0001")); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if err := sink.Add(1, sampleResult("b", "s0002")); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if buf.Len() != 0 {
		t.Error("the json array wrote something before Close")
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	var got []sase.Result
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not a json array: %v\n%s", err, buf.String())
	}
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %d results, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].ElementID != id {
			t.Errorf("result %d = %q, want %q", i, got[i].ElementID, id)
		}
	}
}

func TestJSONSinkEmptyRunIsAnEmptyArray(t *testing.T) {
	var buf bytes.Buffer
	sink := newResultSink(&buf, formatJSON)
	if err := sink.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "[]" {
		t.Errorf("output = %q, want %q so consumers never see null", got, "[]")
	}
}

func TestJSONLSinkWritesOneLinePerElementImmediately(t *testing.T) {
	var buf bytes.Buffer
	sink := newResultSink(&buf, formatJSONL)

	if err := sink.Add(0, sampleResult("a", "s0001")); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	first := buf.String()
	if first == "" {
		t.Fatal("nothing was written after the first element finished")
	}
	if strings.Count(strings.TrimSpace(first), "\n") != 0 {
		t.Errorf("first record spans several lines:\n%s", first)
	}

	if err := sink.Add(1, sampleResult("b", "s0002")); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2:\n%s", len(lines), buf.String())
	}
	for i, line := range lines {
		var r sase.Result
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Errorf("line %d is not a json object: %v", i, err)
		}
	}
}

func TestTextSink(t *testing.T) {
	var buf bytes.Buffer
	sink := newResultSink(&buf, formatText)

	if err := sink.Add(0, sampleResult("1234567890", "s0100-rtr-01")); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if err := sink.Add(1, sase.Result{ElementID: "999", Name: "s0200-rtr-01", Error: "login timed out"}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	out := buf.String()
	for _, want := range []string{
		"s0100-rtr-01", "1234567890", "ion1200", "6.5.1",
		"$ dump interface status", "eth0 up", "eth1 down",
		"s0200-rtr-01", "error: login timed out",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("text output is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "{") {
		t.Errorf("text output contains json punctuation:\n%s", out)
	}
	t.Log("\n" + out)
}

func TestTextSinkFallsBackToTheElementID(t *testing.T) {
	var buf bytes.Buffer
	sink := newResultSink(&buf, formatText)
	if err := sink.Add(0, sase.Result{ElementID: "1234567890"}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if !strings.Contains(buf.String(), "1234567890") {
		t.Errorf("output does not identify the element:\n%s", buf.String())
	}
}

func TestOpenOutput(t *testing.T) {
	now := time.Date(2026, 10, 18, 14, 30, 52, 0, time.UTC)

	t.Run("no folder means stdout", func(t *testing.T) {
		w, where, err := openOutput("", "", formatJSON, now)
		if err != nil {
			t.Fatalf("openOutput() error = %v", err)
		}
		defer w.Close()
		if where != "stdout" {
			t.Errorf("where = %q, want %q", where, "stdout")
		}
	})

	t.Run("dash means stdout even with a folder set", func(t *testing.T) {
		w, where, err := openOutput("-", t.TempDir(), formatJSON, now)
		if err != nil {
			t.Fatalf("openOutput() error = %v", err)
		}
		defer w.Close()
		if where != "stdout" {
			t.Errorf("where = %q, want %q", where, "stdout")
		}
	})

	t.Run("a folder gets a timestamped name", func(t *testing.T) {
		dir := t.TempDir()
		w, where, err := openOutput("", dir, formatJSONL, now)
		if err != nil {
			t.Fatalf("openOutput() error = %v", err)
		}
		w.Close()

		want := filepath.Join(dir, "iontk-20261018-143052.jsonl")
		if where != want {
			t.Errorf("where = %q, want %q", where, want)
		}
		if _, err := os.Stat(want); err != nil {
			t.Errorf("file was not created: %v", err)
		}
	})

	t.Run("an explicit path wins over the folder", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "nested", "report.txt")
		w, where, err := openOutput(path, filepath.Join(dir, "ignored"), formatText, now)
		if err != nil {
			t.Fatalf("openOutput() error = %v", err)
		}
		w.Close()
		if where != path {
			t.Errorf("where = %q, want %q", where, path)
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("missing parent directory was not created: %v", err)
		}
	})

	t.Run("the file is written with restrictive permissions", func(t *testing.T) {
		dir := t.TempDir()
		w, where, err := openOutput("", dir, formatJSON, now)
		if err != nil {
			t.Fatalf("openOutput() error = %v", err)
		}
		w.Close()
		fi, err := os.Stat(where)
		if err != nil {
			t.Fatalf("Stat() error = %v", err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("mode = %04o, want 0600: command output can contain device detail", perm)
		}
	})
}

func TestOutputNameUsesTheFormatExtension(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	tests := map[string]string{
		formatJSON:  "iontk-20260102-030405.json",
		formatJSONL: "iontk-20260102-030405.jsonl",
		formatText:  "iontk-20260102-030405.txt",
	}
	for format, want := range tests {
		if got := outputName(format, now); got != want {
			t.Errorf("outputName(%q) = %q, want %q", format, got, want)
		}
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"/tmp/reports", "/tmp/reports"},
		{"relative/path", "relative/path"},
		{"~", home},
		{"~/ion-reports", filepath.Join(home, "ion-reports")},
		{"  ~/ion-reports  ", filepath.Join(home, "ion-reports")},
		{"~not-a-home", "~not-a-home"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := expandHome(tt.in)
			if err != nil {
				t.Fatalf("expandHome(%q) error = %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("expandHome(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestBatchOutDirIsOnlyUsedWhenFileOutputIsOn(t *testing.T) {
	cfg := &config.Config{BatchDir: "/tmp/reports"}
	if got := cfg.BatchOutDir(); got != "" {
		t.Errorf("BatchOutDir() = %q, want empty while file output is off", got)
	}
	cfg.BatchToFile = true
	if got := cfg.BatchOutDir(); got != "/tmp/reports" {
		t.Errorf("BatchOutDir() = %q, want the folder once file output is on", got)
	}
	// Turning it back off must not lose the path the user typed.
	cfg.BatchToFile = false
	if cfg.BatchDir != "/tmp/reports" {
		t.Errorf("BatchDir = %q, want the path kept", cfg.BatchDir)
	}
	var nilCfg *config.Config
	if got := nilCfg.BatchOutDir(); got != "" {
		t.Errorf("BatchOutDir() on nil = %q, want empty", got)
	}
}

func TestSaveBatchResults(t *testing.T) {
	dir := t.TempDir()
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))

	m := tuiModel{
		cfg:   &config.Config{BatchToFile: true, BatchDir: dir, BatchFormat: formatText},
		store: st,
		results: []sase.Result{
			sampleResult("1", "s0001"),
			{}, // an element that never ran
			sampleResult("3", "s0003"),
		},
	}

	note := m.saveBatchResults()
	if !strings.HasPrefix(note, "saved to ") {
		t.Fatalf("note = %q, want it to report where the file went", note)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("wrote %d files, want 1", len(entries))
	}
	body, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	out := string(body)
	for _, want := range []string{"s0001", "s0003"} {
		if !strings.Contains(out, want) {
			t.Errorf("saved file is missing %q:\n%s", want, out)
		}
	}
	blocks := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "=== ") {
			blocks++
		}
	}
	if blocks != 2 {
		t.Errorf("got %d element blocks, want 2 so the element that never ran is skipped:\n%s", blocks, out)
	}
}

func TestSaveBatchResultsDoesNothingWithoutAFolder(t *testing.T) {
	m := tuiModel{
		cfg:     &config.Config{BatchToFile: false, BatchDir: t.TempDir()},
		results: []sase.Result{sampleResult("1", "s0001")},
	}
	if note := m.saveBatchResults(); note != "" {
		t.Errorf("saveBatchResults() = %q, want nothing written while output is stdout", note)
	}
}

func TestRenderBatchSettings(t *testing.T) {
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))

	m := tuiModel{
		cfg:        &config.Config{BatchToFile: true, BatchDir: "/home/me/ion-reports", BatchFormat: formatJSONL},
		store:      st,
		tenant:     config.Tenant{Name: "Acme Co", TSGID: "1"},
		hasTenant:  true,
		phase:      phaseSettings,
		setSection: secBatch,
		setFocus:   focusContent,
		width:      100,
		height:     30,
	}
	m.spinner = newSpinner("")

	if got := m.sectionRows(); got != 3 {
		t.Errorf("sectionRows() = %d, want 3", got)
	}
	out := m.viewSettings()
	for _, want := range []string{"Batch", "jsonl", "file", "ion-reports"} {
		if !strings.Contains(out, want) {
			t.Errorf("settings view is missing %q", want)
		}
	}
	t.Log("\n" + out)
}

func TestBatchSettingsCycleAndToggle(t *testing.T) {
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))
	m := tuiModel{
		cfg:        &config.Config{},
		store:      st,
		phase:      phaseSettings,
		setSection: secBatch,
		setFocus:   focusContent,
		width:      100,
		height:     30,
	}

	// Row 0 cycles the format and persists it.
	m.setCursor = 0
	mm, _ := m.onBatchKey("enter")
	m = mm.(tuiModel)
	if m.cfg.BatchFormat != formatJSONL {
		t.Errorf("BatchFormat = %q, want %q after one cycle from the default", m.cfg.BatchFormat, formatJSONL)
	}
	if saved, err := st.Config(); err != nil {
		t.Errorf("Config() error = %v", err)
	} else if saved.BatchFormat != formatJSONL {
		t.Errorf("saved BatchFormat = %q, want it persisted", saved.BatchFormat)
	}

	// Row 1 toggles the destination.
	m.setCursor = 1
	mm, _ = m.onBatchKey("enter")
	m = mm.(tuiModel)
	if !m.cfg.BatchToFile {
		t.Error("BatchToFile = false, want file output turned on")
	}
	mm, _ = m.onBatchKey("enter")
	m = mm.(tuiModel)
	if m.cfg.BatchToFile {
		t.Error("BatchToFile = true, want the toggle to go back to stdout")
	}

	// Row 2 opens the folder form.
	m.setCursor = 2
	mm, _ = m.onBatchKey("enter")
	m = mm.(tuiModel)
	if m.formKind != formBatchDir {
		t.Errorf("formKind = %v, want formBatchDir", m.formKind)
	}
	if len(m.form) != 1 {
		t.Fatalf("form has %d fields, want 1", len(m.form))
	}

	m.form[0].input.SetValue("~/ion-reports")
	mm, _ = m.submitForm()
	m = mm.(tuiModel)
	if strings.HasPrefix(m.cfg.BatchDir, "~") {
		t.Errorf("BatchDir = %q, want the home directory expanded", m.cfg.BatchDir)
	}
	if !strings.HasSuffix(m.cfg.BatchDir, "ion-reports") {
		t.Errorf("BatchDir = %q, want it to end in the typed folder", m.cfg.BatchDir)
	}
	if m.phase != phaseSettings {
		t.Errorf("phase = %v, want to land back in settings", m.phase)
	}
}
