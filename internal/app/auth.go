package app

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/fixture"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/saseauth"
)

var _ api.TokenSource = (*saseauth.Source)(nil)

func BrowserOptions(httpClient *http.Client, st *config.Store) saseauth.Options {
	opt := saseauth.Options{Client: httpClient}
	if st != nil {
		opt.DataDir = st.BrowserDir()
	}
	return opt
}

func BrowserSource(f Flags, httpClient *http.Client, st *config.Store) *saseauth.Source {
	opt := BrowserOptions(httpClient, st)
	if st == nil {
		return saseauth.NewSource(f.TSGID, "", opt, nil)
	}

	stored, _ := st.Secrets.Get(config.RefreshTokenKey(f.TSGID))
	if acct, _ := st.Secrets.Get(config.AccountRefreshTokenKey()); acct != "" {
		opt.Seed = accountSeed(acct)
	}
	save := func(rt string) error {
		return st.Secrets.Set(config.RefreshTokenKey(f.TSGID), rt)
	}
	return saseauth.NewSource(f.TSGID, stored, opt, save)
}

var seeds sync.Map

func accountSeed(token string) *saseauth.Seed {
	v, _ := seeds.LoadOrStore(token, saseauth.NewSeed(token))
	return v.(*saseauth.Seed)
}

var Debug bool

var Fixture *fixture.Set

func Store() (*config.Store, error) {
	if Fixture != nil {
		st, err := config.OpenDebug()
		if err != nil {
			return nil, err
		}
		return st, seed(st, Fixture)
	}
	if Debug {
		return config.OpenDebug()
	}
	return config.Open()
}

var (
	seedMu   sync.Mutex
	seededAs *fixture.Set
)

func seed(st *config.Store, f *fixture.Set) error {
	seedMu.Lock()
	defer seedMu.Unlock()
	if seededAs == f {
		return nil
	}

	tenants := f.SavedTenants()
	cfg := &config.Config{Tenants: tenants}
	if len(tenants) > 0 {
		cfg.LastTenant = tenants[0].Name
	}
	for _, t := range tenants {
		if err := st.Secrets.Set(config.ClientSecretKey(t.TSGID), "fixture"); err != nil {
			return fmt.Errorf("seed fixture tenant %s: %w", t.Name, err)
		}
	}
	for key, pass := range f.Passwords() {
		if err := st.Secrets.Set(key, pass); err != nil {
			return fmt.Errorf("seed fixture secret: %w", err)
		}
	}
	if err := st.SaveConfig(cfg); err != nil {
		return err
	}
	seededAs = f
	return nil
}

func OpenStore() *config.Store {
	st, err := Store()
	if err != nil {
		return nil
	}
	return st
}

func ShortAuthError(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	switch {
	case strings.Contains(text, "did not complete within"):
		return "sign-in timed out"
	case strings.Contains(text, "invalid_grant"),
		strings.Contains(text, "stored refresh token rejected"):
		return "sign-in expired"
	case errors.Is(err, saseauth.ErrInteractiveRequired),
		strings.Contains(text, "interaction_required"),
		strings.Contains(text, "login_required"):
		return "not accessible with this sign-in"
	case IsAuthFailure(text):
		return "credential rejected"
	}
	if i := strings.Index(text, "\n"); i > 0 {
		text = text[:i]
	}
	if code := apiErrorCode(text); code != "" {
		return code
	}
	return text
}

var apiErrCode = regexp.MustCompile(`"code"\s*:\s*"([A-Z0-9_]+)"`)

func apiErrorCode(text string) string {
	m := apiErrCode.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	prefix, _, _ := strings.Cut(text, "{")
	return strings.TrimRight(strings.TrimSpace(prefix), ":") + ": " + m[1]
}

func IsAuthFailure(msg string) bool {
	low := strings.ToLower(msg)
	for _, s := range []string{
		"but got 401", "but got 403",
		"status 401", "status 403",
		"unauthorized", "forbidden", "token expired", "invalid_grant",
	} {
		if strings.Contains(low, s) {
			return true
		}
	}
	return false
}
