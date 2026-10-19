package config

import (
	"errors"
	"fmt"
	"os"

	"github.com/zalando/go-keyring"
)

// Reset deletes everything iontk has saved: keyring entries, element and tenant
// caches, the browser profile and the configuration itself.
//
// The order is the point. Keyring entries are per-key and can only be found
// through the tenant list in config.json, so deleting the config first would
// orphan every one of them with no way left to name them.
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

	// The whole directory rather than each tenant's file: a cache left behind
	// for a tenant that is no longer in the config would survive the reset.
	errs = append(errs, removePath(s.cacheDir()), removePath(s.tenantsPath()),
		removePath(s.commandsPath()))

	// Keys this store does not know about are still in the fallback file, and a
	// factory reset has to take them too.
	if p, ok := s.Secrets.(interface{ Path() string }); ok {
		errs = append(errs, removePath(p.Path()))
	}

	// The browser profile holds the identity provider's session cookie, so a
	// reset that kept it would not have signed the user out.
	errs = append(errs, removePath(s.BrowserDir()), removePath(s.configPath()))
	return errors.Join(errs...)
}

// A key that was never stored is not a failure; a keyring that will not answer
// is, and has to be said rather than swallowed.
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
