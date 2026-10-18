package tui

import "tyrtop.com/iontk/internal/config"

// authMode is which kind of credential the menu is working with. It is a
// different axis from the element list's shell/batch mode: this one decides
// where tenants come from, that one decides what selecting an element does.
type authMode int

const (
	// authBrowser signs in as a person and reaches every tenant the account is
	// entitled to.
	authBrowser authMode = iota
	// authTenant works from a service account, which is one tenant each.
	authTenant
)

var authModes = []authMode{authBrowser, authTenant}

func (a authMode) String() string {
	if a == authTenant {
		return "tenant"
	}
	return "browser"
}

// Label is how the mode is named on screen.
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

// tenantAuthFor is the credential a tenant created under this mode should use.
func (a authMode) tenantAuthFor() string {
	if a == authTenant {
		return config.AuthServiceAccount
	}
	return config.AuthBrowser
}
