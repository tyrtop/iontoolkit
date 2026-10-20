package config

import (
	"errors"
	"fmt"
	"os"

	"github.com/zalando/go-keyring"
)

func (s *Store) Reset() error {
	cfg, err := s.Config()
	if err != nil {
		return err
	}

	var errs []error
	for _, t := range cfg.Tenants {
		for _, u := range t.Users {
			errs = append(errs, s.dropSecret(IONPassKey(t.TSGID, u.Name)))
		}
		errs = append(errs,
			s.dropSecret(ClientSecretKey(t.TSGID)),
			s.dropSecret(RefreshTokenKey(t.TSGID)),
		)
	}
	errs = append(errs, s.dropSecret(AccountRefreshTokenKey()))

	errs = append(errs, removePath(s.cacheDir()), removePath(s.tenantsPath()),
		removePath(s.commandsPath()))

	if p, ok := s.Secrets.(interface{ Path() string }); ok {
		errs = append(errs, removePath(p.Path()))
	}

	errs = append(errs, removePath(s.BrowserDir()), removePath(s.configPath()))
	return errors.Join(errs...)
}

func (s *Store) dropSecret(key string) error {
	err := s.Secrets.Delete(key)
	if err == nil || errors.Is(err, ErrNotFound) || errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return fmt.Errorf("delete secret %s: %w", key, err)
}

func removePath(path string) error {
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}
