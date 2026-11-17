package tui

type authMode int

const (
	authBrowser authMode = iota
	authTenant
)

func (a authMode) Label() string {
	if a == authTenant {
		return "SASE Service Account"
	}
	return "Browser Sign-In"
}
