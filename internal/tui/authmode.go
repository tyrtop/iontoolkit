package tui

import "tyrtop.com/iontk/internal/config"

type authMode int

const (
	authBrowser authMode = iota
	authTenant
)

var authModes = []authMode{authBrowser, authTenant}

func (a authMode) String() string {
	if a == authTenant {
		return "tenant"
	}
	return "browser"
}

func (a authMode) Label() string {
	if a == authTenant {
		return "SASE Service Account"
	}
	return "Browser Sign-In"
}

func (a authMode) next() authMode {
	for i, m := range authModes {
		if m == a {
			return authModes[(i+1)%len(authModes)]
		}
	}
	return authBrowser
}

func parseAuthMode(s string) (authMode, bool) {
	for _, m := range authModes {
		if m.String() == s {
			return m, true
		}
	}
	return authBrowser, false
}

func (a authMode) tenantAuthFor() string {
	if a == authTenant {
		return config.AuthServiceAccount
	}
	return config.AuthBrowser
}
