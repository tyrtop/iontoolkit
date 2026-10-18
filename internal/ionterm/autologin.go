package ionterm

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

type Credentials struct {
	User string
	Pass string
}

const (
	loginPrompt    = "login: "
	passwordPrompt = "Password: "
	matchWindow    = 512
)

type autologin struct {
	creds Credentials
	buf   string
	step  int
}

func newAutologin(c Credentials) *autologin {
	if c.User == "" || c.Pass == "" {
		return nil
	}
	return &autologin{creds: c}
}

func (a *autologin) feed(data []byte) (string, bool) {
	if a == nil || a.step > 1 {
		return "", false
	}

	a.buf += ansi.Strip(string(data))
	if len(a.buf) > matchWindow {
		a.buf = a.buf[len(a.buf)-matchWindow:]
	}

	want := loginPrompt
	if a.step == 1 {
		want = passwordPrompt
	}
	if !strings.Contains(a.buf, want) {
		return "", false
	}

	a.buf = ""
	if a.step == 0 {
		a.step = 1
		return a.creds.User + "\r", true
	}
	a.step = 2
	return a.creds.Pass + "\r", true
}

func (a *autologin) done() bool { return a == nil || a.step > 1 }
