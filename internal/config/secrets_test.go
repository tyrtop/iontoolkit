package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"
)

func statFile(path string) (os.FileInfo, error) { return os.Stat(path) }

func TestFileSecretsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.json")
	s := NewFileSecrets(path)

	if s.Backend() != "file" {
		t.Errorf("Backend() = %q", s.Backend())
	}

	if _, err := s.Get("missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(missing) err = %v, want ErrNotFound", err)
	}

	if err := s.Set(ClientSecretKey("1234"), "sekrit"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ClientSecretKey("1234"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "sekrit" {
		t.Errorf("Get() = %q, want sekrit", got)
	}

	if err := s.Set(IONPassKey("1234", "cisops"), "pw1"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ClientSecretKey("1234")); got != "sekrit" {
		t.Error("second Set clobbered the first key")
	}

	if err := s.Delete(ClientSecretKey("1234")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ClientSecretKey("1234")); !errors.Is(err, ErrNotFound) {
		t.Errorf("after Delete, err = %v, want ErrNotFound", err)
	}
	if got, _ := s.Get(IONPassKey("1234", "cisops")); got != "pw1" {
		t.Error("Delete removed the wrong key")
	}
}

func TestFileSecretsModeIsOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.json")
	s := NewFileSecrets(path)
	if err := s.Set("k", "v"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("secrets mode = %o, want 600", perm)
	}
}

func TestFileSecretsDeleteMissingIsNoError(t *testing.T) {
	s := NewFileSecrets(filepath.Join(t.TempDir(), "secrets.json"))
	if err := s.Delete("nope"); err != nil {
		t.Errorf("Delete(missing) = %v, want nil", err)
	}
}

func TestKeyringSecretsWithMock(t *testing.T) {
	keyring.MockInit()

	var s Secrets = keyringSecrets{}
	if s.Backend() != "keyring" {
		t.Errorf("Backend() = %q", s.Backend())
	}
	if err := (keyringSecrets{}).probe(); err != nil {
		t.Fatalf("probe against mock = %v", err)
	}

	key := ClientSecretKey("1234")
	if err := s.Set(key, "sekrit"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	if got != "sekrit" {
		t.Errorf("Get() = %q, want sekrit", got)
	}
	if err := s.Delete(key); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(key); err == nil {
		t.Error("Get after Delete returned no error")
	}
}

func TestOpenSecretsFallsBackToFileWhenKeyringFails(t *testing.T) {
	keyring.MockInitWithError(errors.New("no secret service"))

	root := t.TempDir()
	if got := OpenSecrets(root).Backend(); got != "file" {
		t.Errorf("Backend() = %q, want file", got)
	}
}

func TestOpenSecretsPrefersKeyringWhenAvailable(t *testing.T) {
	keyring.MockInit()

	if got := OpenSecrets(t.TempDir()).Backend(); got != "keyring" {
		t.Errorf("Backend() = %q, want keyring", got)
	}
}
