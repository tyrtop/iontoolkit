package ionterm

import "testing"

func TestAutologinSendsUserThenPassword(t *testing.T) {
	a := newAutologin(Credentials{User: "ops", Pass: "s3cret"})
	if a == nil {
		t.Fatal("want an autologin")
	}

	if _, ok := a.feed([]byte("__CGNX_REMOTE_ACCESS_SESSION_ID__ : 0000 \r\n")); ok {
		t.Error("sent something before the login prompt")
	}

	got, ok := a.feed([]byte("028201-011790-4565 login: "))
	if !ok || got != "ops\r" {
		t.Fatalf("login step sent %q ok=%v, want ops", got, ok)
	}

	got, ok = a.feed([]byte("\r\nPassword: "))
	if !ok || got != "s3cret\r" {
		t.Fatalf("password step sent %q ok=%v", got, ok)
	}

	if _, ok := a.feed([]byte("\r\nion1# ")); ok {
		t.Error("kept sending after login completed")
	}
	if !a.done() {
		t.Error("want done after both steps")
	}
}

func TestAutologinHandlesPromptsSplitAcrossFrames(t *testing.T) {
	a := newAutologin(Credentials{User: "u", Pass: "p"})

	if _, ok := a.feed([]byte("4565 log")); ok {
		t.Fatal("matched a partial prompt")
	}
	got, ok := a.feed([]byte("in: "))
	if !ok || got != "u\r" {
		t.Errorf("split prompt sent %q ok=%v", got, ok)
	}
}

func TestAutologinIgnoresAnsiAroundPrompts(t *testing.T) {
	a := newAutologin(Credentials{User: "u", Pass: "p"})
	got, ok := a.feed([]byte("\x1b[J\x1b[2K\r\x1b[31mlogin: \x1b[0m"))
	if !ok || got != "u\r" {
		t.Errorf("styled prompt sent %q ok=%v", got, ok)
	}
}

func TestAutologinDisabledWithoutBothCredentials(t *testing.T) {
	if newAutologin(Credentials{}) != nil {
		t.Error("no credentials should mean no autologin")
	}
	if newAutologin(Credentials{User: "u"}) != nil {
		t.Error("missing password should mean no autologin")
	}
	var a *autologin
	if _, ok := a.feed([]byte("login: ")); ok {
		t.Error("nil autologin sent something")
	}
	if !a.done() {
		t.Error("nil autologin should report done")
	}
}

func TestAutologinBufferStaysBounded(t *testing.T) {
	a := newAutologin(Credentials{User: "u", Pass: "p"})
	for i := 0; i < 50; i++ {
		a.feed([]byte("0123456789012345678901234567890123456789"))
	}
	if len(a.buf) > matchWindow {
		t.Errorf("buffer grew to %d, want at most %d", len(a.buf), matchWindow)
	}
	if got, ok := a.feed([]byte("login: ")); !ok || got != "u\r" {
		t.Errorf("still matches after churn: %q ok=%v", got, ok)
	}
}
