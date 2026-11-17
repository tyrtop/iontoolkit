package output

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		{"", FormatJSON, false},
		{"json", FormatJSON, false},
		{"JSON", FormatJSON, false},
		{"  json  ", FormatJSON, false},
		{"jsonl", FormatJSONL, false},
		{"ndjson", FormatJSONL, false},
		{"text", FormatText, false},
		{"txt", FormatText, false},
		{"plain", FormatText, false},
		{"table", FormatTable, false},
		{"tbl", FormatTable, false},
		{"csv", FormatCSV, false},
		{"CSV", FormatCSV, false},
		{"yaml", "", true},
		{"xml", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := NormalizeFormat(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("NormalizeFormat(%q) error = nil, want an error", tt.in)
				}
				if !strings.Contains(err.Error(), "json") {
					t.Errorf("error = %q, want it to list the valid formats", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeFormat(%q) error = %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("NormalizeFormat(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestFormatExt(t *testing.T) {
	tests := map[string]string{
		FormatJSON:  "json",
		FormatJSONL: "jsonl",
		FormatText:  "txt",
		FormatTable: "txt",
		FormatCSV:   "csv",
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
	sink := NewSink(&buf, FormatJSON)

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

func TestJSONSinkIndents(t *testing.T) {
	var buf bytes.Buffer
	sink := NewSink(&buf, FormatJSON)
	if err := sink.Add(0, sampleResult("a", "s0001")); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	got := buf.String()
	if !strings.Contains(got, "[\n  {\n    \"element_id\"") {
		t.Errorf("json array is not indented two spaces:\n%s", got)
	}
}

func TestJSONSinkEmptyRunIsAnEmptyArray(t *testing.T) {
	var buf bytes.Buffer
	sink := NewSink(&buf, FormatJSON)
	if err := sink.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "[]" {
		t.Errorf("output = %q, want %q so consumers never see null", got, "[]")
	}
}

func TestJSONLSinkWritesOneLinePerElementImmediately(t *testing.T) {
	var buf bytes.Buffer
	sink := NewSink(&buf, FormatJSONL)

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
	sink := NewSink(&buf, FormatText)

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
	sink := NewSink(&buf, FormatText)
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
		w, where, err := Open("", "", FormatJSON, now)
		if err != nil {
			t.Fatalf("Open() error = %v", err)
		}
		defer w.Close()
		if where != "stdout" {
			t.Errorf("where = %q, want %q", where, "stdout")
		}
	})

	t.Run("dash means stdout even with a folder set", func(t *testing.T) {
		w, where, err := Open("-", t.TempDir(), FormatJSON, now)
		if err != nil {
			t.Fatalf("Open() error = %v", err)
		}
		defer w.Close()
		if where != "stdout" {
			t.Errorf("where = %q, want %q", where, "stdout")
		}
	})

	t.Run("a folder gets a timestamped name", func(t *testing.T) {
		dir := t.TempDir()
		w, where, err := Open("", dir, FormatJSONL, now)
		if err != nil {
			t.Fatalf("Open() error = %v", err)
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
		w, where, err := Open(path, filepath.Join(dir, "ignored"), FormatText, now)
		if err != nil {
			t.Fatalf("Open() error = %v", err)
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
		w, where, err := Open("", dir, FormatJSON, now)
		if err != nil {
			t.Fatalf("Open() error = %v", err)
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
		FormatJSON:  "iontk-20260102-030405.json",
		FormatJSONL: "iontk-20260102-030405.jsonl",
		FormatText:  "iontk-20260102-030405.txt",
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
			got, err := ExpandHome(tt.in)
			if err != nil {
				t.Fatalf("ExpandHome(%q) error = %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ExpandHome(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestExportNameNamesThePageAndTheSecond(t *testing.T) {
	at := time.Date(2026, 10, 20, 9, 14, 3, 0, time.UTC)

	tests := []struct {
		name, slug, ext, want string
	}{
		{"a session", "S0100-RTR-01", "txt", "iontk-20261020-091403-s0100-rtr-01.txt"},
		{"a data tab", "store-0003-rtr-01 · Overview", "txt", "iontk-20261020-091403-store-0003-rtr-01-overview.txt"},
		{"a run", "run", "json", "iontk-20261020-091403-run.json"},
		{"nothing to name it after", "", "txt", "iontk-20261020-091403.txt"},
		{"punctuation only", "···", "txt", "iontk-20261020-091403.txt"},
		{"no extension given", "run", "", "iontk-20261020-091403-run.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExportName(tt.slug, tt.ext, at); got != tt.want {
				t.Errorf("ExportName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSlugStaysShortEnoughToReadInAFilename(t *testing.T) {
	got := Slug(strings.Repeat("element-name-", 10))
	if len(got) > slugMax {
		t.Errorf("Slug() = %q, %d chars, want at most %d", got, len(got), slugMax)
	}
	if strings.HasSuffix(got, "-") {
		t.Errorf("Slug() = %q, want no trailing dash", got)
	}
}
