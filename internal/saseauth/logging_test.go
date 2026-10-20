package saseauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"tyrtop.com/iontk/internal/logging"
)

func TestNoSecretReachesTheLogFile(t *testing.T) {
	const (
		stored = "rt1-c3VwZXItc2VjcmV0LXN0b3JlZC1yZWZyZXNoLXRva2Vu"
		seed   = "seed-YWNjb3VudC1zZWVkLXJlZnJlc2gtdG9rZW4tdmFsdWU"
		access = "at-ZXlKaGJHY2lPaUpTVXpJMU5pSjkuYWNjZXNz"
		rotate = "rt2-cm90YXRlZC1yZWZyZXNoLXRva2VuLXZhbHVl"
	)

	tests := []struct {
		name    string
		stored  string
		status  int
		wantSay []string
	}{
		{"stored token works", stored, http.StatusOK, []string{
			"trying the stored refresh token",
			"stored refresh token accepted",
			"refresh token saved for this tenant",
		}},
		{"stored token refused, the account credential narrows", stored, http.StatusBadRequest, []string{
			"stored refresh token rejected",
			"redeeming the account credential",
		}},
		{"no stored token at all", "", http.StatusBadRequest, []string{
			"no stored refresh token for this tenant",
			"redeeming the account credential",
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r.ParseForm()
				if r.PostForm.Get("refresh_token") == stored && tt.status != http.StatusOK {
					w.WriteHeader(tt.status)
					w.Write([]byte(`{"error":"invalid_grant","error_description":"token ` + stored + ` is dead"}`))
					return
				}
				w.Write([]byte(`{"access_token":"` + access + `","refresh_token":"` + rotate + `","expires_in":899}`))
			}))
			defer srv.Close()

			if _, err := logging.Start(logging.Options{Dir: t.TempDir()}); err != nil {
				t.Fatalf("logging.Start() error = %v", err)
			}
			defer logging.Close()

			var saved string
			src := NewSource("1436146912", tt.stored,
				Options{TokenURL: srv.URL, Client: srv.Client(), Seed: NewSeed(seed)},
				func(rt string) error { saved = rt; return nil })

			if _, _, err := src.Token(context.Background()); err != nil {
				t.Fatalf("Token() error = %v", err)
			}
			if saved != rotate {
				t.Fatalf("saved refresh token = %q, want the rotated one", saved)
			}

			b, err := os.ReadFile(logging.Path())
			if err != nil {
				t.Fatalf("read the log: %v", err)
			}
			body := string(b)

			for _, secret := range []string{stored, seed, access, rotate} {
				if tt.stored == "" && secret == stored {
					continue
				}
				if strings.Contains(body, secret) {
					t.Errorf("the log carries a secret ending %q:\n%s", logging.Fingerprint(secret), body)
				}
			}
			for _, say := range tt.wantSay {
				if !strings.Contains(body, say) {
					t.Errorf("the log never says %q, so the path it took is still unrecoverable:\n%s", say, body)
				}
			}
			if !strings.Contains(body, logging.Fingerprint(rotate)) {
				t.Errorf("the log names no fingerprint for the saved token, so it cannot be told apart:\n%s", body)
			}
		})
	}
}
