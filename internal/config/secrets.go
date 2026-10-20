package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/zalando/go-keyring"
)

const keyringService = "iontk"

type Secrets interface {
	Get(key string) (string, error)
	Set(key, value string) error
	Delete(key string) error
	Backend() string
}

func Plaintext(s Secrets) bool { return s.Backend() == "file" }

func SecretsWarning(s Secrets) string {
	switch s.Backend() {
	case "file":
		return "plaintext file — no OS keyring available"
	case "memory":
		return "in memory — nothing is kept past this run"
	}
	return "OS keyring"
}

func OpenSecrets(root string) Secrets {
	k := keyringSecrets{}
	if err := k.probe(); err == nil {
		return k
	}
	return NewFileSecrets(filepath.Join(root, "secrets.json"))
}

type keyringSecrets struct{}

func (keyringSecrets) Backend() string { return "keyring" }

func (keyringSecrets) probe() error {
	const k = "probe"
	if err := keyring.Set(keyringService, k, "1"); err != nil {
		return err
	}
	if _, err := keyring.Get(keyringService, k); err != nil {
		return err
	}
	return keyring.Delete(keyringService, k)
}

func (keyringSecrets) Get(key string) (string, error) {
	v, err := keyring.Get(keyringService, key)
	if err != nil {
		return "", err
	}
	return v, nil
}

func (keyringSecrets) Set(key, value string) error {
	return keyring.Set(keyringService, key, value)
}

func (keyringSecrets) Delete(key string) error {
	return keyring.Delete(keyringService, key)
}

type fileSecrets struct {
	path string
	mu   sync.Mutex
}

func NewFileSecrets(path string) Secrets { return &fileSecrets{path: path} }

func (f *fileSecrets) Backend() string { return "file" }

func (f *fileSecrets) Path() string { return f.path }

func (f *fileSecrets) load() (map[string]string, error) {
	b, err := os.ReadFile(f.path)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read secrets: %w", err)
	}
	m := map[string]string{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parse secrets: %w", err)
	}
	return m, nil
}

func (f *fileSecrets) save(m map[string]string) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encode secrets: %w", err)
	}
	return writeAtomic(f.path, append(b, '\n'), 0o600)
}

func (f *fileSecrets) Get(key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	m, err := f.load()
	if err != nil {
		return "", err
	}
	v, ok := m[key]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (f *fileSecrets) Set(key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	m, err := f.load()
	if err != nil {
		return err
	}
	m[key] = value
	return f.save(m)
}

func (f *fileSecrets) Delete(key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	m, err := f.load()
	if err != nil {
		return err
	}
	delete(m, key)
	return f.save(m)
}

var ErrNotFound = fmt.Errorf("secret not found")

type memSecrets struct {
	mu sync.Mutex
	m  map[string]string
}

func NewMemorySecrets() Secrets { return &memSecrets{m: map[string]string{}} }

func (*memSecrets) Backend() string { return "memory" }

func (s *memSecrets) Get(key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[key]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (s *memSecrets) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = value
	return nil
}

func (s *memSecrets) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
	return nil
}
