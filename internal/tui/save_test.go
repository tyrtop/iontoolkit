package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/output"
	"tyrtop.com/iontk/internal/sase"
	"tyrtop.com/iontk/internal/sase/ioncli"
	"tyrtop.com/iontk/internal/ui"
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
		cfg:   &config.Config{BatchToFile: true, BatchDir: dir, BatchFormat: output.FormatText},
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
		cfg:        &config.Config{BatchToFile: true, BatchDir: "/home/me/ion-reports", BatchFormat: output.FormatJSONL},
		store:      st,
		tenant:     config.Tenant{Name: "Acme Co", TSGID: "1"},
		hasTenant:  true,
		phase:      phaseSettings,
		setSection: secBatch,
		setFocus:   focusContent,
		width:      100,
		height:     30,
	}
	m.spinner = ui.NewSpinner("")

	if got := m.sectionRows(); got != 4 {
		t.Errorf("sectionRows() = %d, want 4", got)
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
	if m.cfg.BatchFormat != output.FormatJSONL {
		t.Errorf("BatchFormat = %q, want %q after one cycle from the default", m.cfg.BatchFormat, output.FormatJSONL)
	}
	if saved, err := st.Config(); err != nil {
		t.Errorf("Config() error = %v", err)
	} else if saved.BatchFormat != output.FormatJSONL {
		t.Errorf("saved BatchFormat = %q, want it persisted", saved.BatchFormat)
	}

	// Row 1 cycles the row orientation.
	m.setCursor = 1
	mm, _ = m.onBatchKey("enter")
	m = mm.(tuiModel)
	if m.cfg.BatchOrient != output.OrientCommand {
		t.Errorf("BatchOrient = %q, want %q after one cycle from the default", m.cfg.BatchOrient, output.OrientCommand)
	}
	if saved, err := st.Config(); err != nil {
		t.Errorf("Config() error = %v", err)
	} else if saved.BatchOrient != output.OrientCommand {
		t.Errorf("saved BatchOrient = %q, want it persisted", saved.BatchOrient)
	}

	// Row 2 toggles the destination.
	m.setCursor = 2
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

	// Row 3 opens the folder form.
	m.setCursor = 3
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
