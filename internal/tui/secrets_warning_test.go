package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/ui"
)

// fakeKeyring stands in for a working OS keyring, which a test machine may not have.
type fakeKeyring struct{ m map[string]string }

func (f fakeKeyring) Backend() string { return "keyring" }
func (f fakeKeyring) Get(k string) (string, error) {
	return f.m[k], nil
}
func (f fakeKeyring) Set(k, v string) error { f.m[k] = v; return nil }
func (f fakeKeyring) Delete(k string) error { delete(f.m, k); return nil }

func TestPlaintextDetection(t *testing.T) {
	file := config.NewFileSecrets(filepath.Join(t.TempDir(), "secrets.json"))
	if !config.Plaintext(file) {
		t.Error("Plaintext() = false for the file store, want true")
	}
	if got := config.SecretsWarning(file); !strings.Contains(got, "plaintext") {
		t.Errorf("SecretsWarning() = %q, want it to say plaintext outright", got)
	}
	if got := config.SecretsWarning(file); !strings.Contains(got, "no OS keyring") {
		t.Errorf("SecretsWarning() = %q, want it to say why the fallback happened", got)
	}

	kr := fakeKeyring{m: map[string]string{}}
	if config.Plaintext(kr) {
		t.Error("Plaintext() = true for a keyring, want false")
	}
	if got := config.SecretsWarning(kr); strings.Contains(got, "plaintext") {
		t.Errorf("SecretsWarning() = %q, must not call a keyring plaintext", got)
	}
}

func warnModel(t *testing.T, secrets config.Secrets) tuiModel {
	t.Helper()
	st := config.OpenAt(t.TempDir(), secrets)
	return tuiModel{
		cfg:       &config.Config{},
		store:     st,
		tenant:    config.Tenant{Name: "Acme Co", TSGID: "1"},
		hasTenant: true,
		width:     100,
		height:    30,
	}
}

func TestFormsWarnBeforeASecretIsTyped(t *testing.T) {
	plain := config.NewFileSecrets(filepath.Join(t.TempDir(), "secrets.json"))

	t.Run("a masked field on a plaintext store warns", func(t *testing.T) {
		m := warnModel(t, plain)
		mm, _ := m.openUserFormFor(config.Tenant{Name: "Acme Co", TSGID: "1"})
		m = mm.(tuiModel)

		if got := m.plaintextWarning(); got == "" {
			t.Fatal("plaintextWarning() is empty on a form that takes a password")
		}
		out := m.viewForm()
		if !strings.Contains(out, "plaintext") {
			t.Errorf("the password form does not warn about plaintext:\n%s", out)
		}
		t.Log("\n" + out)
	})

	t.Run("a form with no secret stays quiet", func(t *testing.T) {
		m := warnModel(t, plain)
		mm, _ := m.openTenantForm(config.AuthBrowser) // name and tsg only
		m = mm.(tuiModel)
		if got := m.plaintextWarning(); got != "" {
			t.Errorf("plaintextWarning() = %q, want nothing on a form with no secret", got)
		}
	})

	t.Run("a keyring needs no warning", func(t *testing.T) {
		m := warnModel(t, fakeKeyring{m: map[string]string{}})
		mm, _ := m.openUserFormFor(config.Tenant{Name: "Acme Co", TSGID: "1"})
		m = mm.(tuiModel)
		if got := m.plaintextWarning(); got != "" {
			t.Errorf("plaintextWarning() = %q, want nothing when a keyring is in use", got)
		}
	})
}

func TestSettingsNamesThePlaintextFallbackAndItsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.json")
	m := warnModel(t, config.NewFileSecrets(path))
	m.phase = phaseSettings
	m.setSection = secGeneral
	m.setFocus = focusContent
	m.spinner = ui.NewSpinner("")

	if got := m.secretsLine(); !strings.Contains(got, "plaintext") {
		t.Errorf("secretsLine() = %q, want it to say plaintext", got)
	}

	out := m.viewSettings()
	for _, want := range []string{"plaintext", "secrets.json", "readable by anything"} {
		if !strings.Contains(out, want) {
			t.Errorf("the settings pane is missing %q:\n%s", want, out)
		}
	}
	t.Log("\n" + out)
}

func TestSettingsDoesNotWarnWithAKeyring(t *testing.T) {
	m := warnModel(t, fakeKeyring{m: map[string]string{}})
	m.phase = phaseSettings
	m.setSection = secGeneral
	m.setFocus = focusContent
	m.spinner = ui.NewSpinner("")

	out := m.viewSettings()
	if strings.Contains(out, "plaintext") {
		t.Errorf("the settings pane warns about plaintext while using a keyring:\n%s", out)
	}
	if !strings.Contains(out, "keyring") {
		t.Errorf("the settings pane does not name the keyring:\n%s", out)
	}
}

func TestGeneralSectionHasARefreshRow(t *testing.T) {
	root := t.TempDir()
	st := config.OpenAt(root, config.NewFileSecrets(filepath.Join(root, "secrets.json")))
	if err := st.Secrets.Set(config.ClientSecretKey("1"), "s"); err != nil {
		t.Fatalf("seed secret: %v", err)
	}
	m := tuiModel{
		cfg:        &config.Config{},
		store:      st,
		tenant:     config.Tenant{Name: "Acme Co", TSGID: "1", ClientID: "svc"},
		hasTenant:  true,
		phase:      phaseSettings,
		setSection: secGeneral,
		setFocus:   focusContent,
		cacheState: cacheCached,
		width:      100,
		height:     30,
	}
	m.spinner = ui.NewSpinner("")

	if got := m.sectionRows(); got != generalRows {
		t.Fatalf("sectionRows() = %d, want %d so the refresh row is reachable", got, generalRows)
	}
	if out := m.viewSettings(); !strings.Contains(out, "elements") {
		t.Errorf("the General section has no elements row:\n%s", out)
	}

	// The last row is the refresh; it moved when the jump setting was added.
	m.setCursor = m.sectionRows() - 1
	mm, cmd := m.onGeneralKey("enter")
	got := mm.(tuiModel)
	if got.cacheState != cacheUpdating {
		t.Errorf("cacheState = %v, want the settings entry to start a refresh", got.cacheState)
	}
	if cmd == nil {
		t.Error("the settings refresh entry issued no command")
	}
}

func TestShortenPathKeepsTheFileName(t *testing.T) {
	long := "/home/benhall/.config/iontk/with/a/very/deep/nesting/that/keeps/going/secrets.json"
	got := shortenPath(long, 40)
	if !strings.HasSuffix(got, "secrets.json") {
		t.Errorf("shortenPath() = %q, want the file name kept", got)
	}
	if n := len([]rune(got)); n > 40 {
		t.Errorf("shortenPath() returned %d runes, want at most 40", n)
	}
	if !strings.HasPrefix(got, "…") {
		t.Errorf("shortenPath() = %q, want it to mark the dropped prefix", got)
	}
	short := "/tmp/secrets.json"
	if got := shortenPath(short, 40); got != short {
		t.Errorf("shortenPath() = %q, want a short path untouched", got)
	}
}
