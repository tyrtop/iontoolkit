package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func record(t *testing.T, log func(*slog.Logger)) (string, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	log(slog.New(redact(slog.NewJSONHandler(&buf, nil))))

	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("nothing was logged")
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(line), &out); err != nil {
		t.Fatalf("log line %q is not JSON: %v", line, err)
	}
	return line, out
}

func TestSecretAttributesBecomeFingerprints(t *testing.T) {
	const secret = "eyJhbGciOiJSUzI1NiJ9.verylongtokenbody.signature"

	tests := []string{
		"token", "access_token", "refresh_token", "id_token", "client_secret",
		"password", "code", "code_verifier", "authorization", "seed",
	}
	for _, key := range tests {
		t.Run(key, func(t *testing.T) {
			line, out := record(t, func(l *slog.Logger) {
				l.Info("auth", key, secret)
			})
			if strings.Contains(line, secret) {
				t.Fatalf("%s reached the log: %s", key, line)
			}
			got, _ := out[key].(string)
			if want := Fingerprint(secret); got != want {
				t.Errorf("%s = %q, want %q", key, got, want)
			}
		})
	}
}

func TestSecretsInsideGroupsAndWithAttrsAreScrubbed(t *testing.T) {
	const secret = "rt-abcdefghijklmnop"

	line, _ := record(t, func(l *slog.Logger) {
		l.With("refresh_token", secret).WithGroup("seed").Info("redeem", "token", secret)
	})
	if strings.Contains(line, secret) {
		t.Fatalf("a secret survived a group or With: %s", line)
	}
}

func TestSecretsInMessagesAndErrorsAreScrubbed(t *testing.T) {
	const secret = "4/0AY0e-g7xlongauthcode"

	tests := []struct {
		name string
		log  func(*slog.Logger)
	}{
		{"message", func(l *slog.Logger) {
			l.Info(`token: status 400 Bad Request: {"refresh_token":"` + secret + `"}`)
		}},
		{"error attribute", func(l *slog.Logger) {
			l.Info("refresh failed", "err", `token: status 400: {"access_token": "`+secret+`"}`)
		}},
		{"form body", func(l *slog.Logger) {
			l.Info("exchange", "body", "grant_type=refresh_token&refresh_token="+secret)
		}},
		{"device password", func(l *slog.Logger) {
			l.Info("login", "err", "password="+secret+" rejected")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line, _ := record(t, tt.log)
			if strings.Contains(line, secret) {
				t.Fatalf("a secret survived in free text: %s", line)
			}
			if !strings.Contains(line, Fingerprint(secret)) {
				t.Errorf("line %q names no fingerprint, so the length is lost", line)
			}
		})
	}
}

func TestCallbackURLKeepsEverythingButTheCode(t *testing.T) {
	const code = "pLkFQ3-authorizationcode-9ZxY"

	line, out := record(t, func(l *slog.Logger) {
		l.Info("callback", "url",
			"https://example.test/authentication/callback?code="+code+"&state=abc123")
	})
	if strings.Contains(line, code) {
		t.Fatalf("the callback code reached the log: %s", line)
	}
	got, _ := out["url"].(string)
	if !strings.Contains(got, "state=abc123") {
		t.Errorf("url = %q, want the state kept for matching up a leg", got)
	}
}

func TestANonSecretAttributeIsLeftAlone(t *testing.T) {
	_, out := record(t, func(l *slog.Logger) {
		l.Info("elements", "tsg", "1436146912", "count", 5613)
	})
	if got, _ := out["tsg"].(string); got != "1436146912" {
		t.Errorf("tsg = %q, want it untouched", got)
	}
	if got, _ := out["count"].(float64); got != 5613 {
		t.Errorf("count = %v, want 5613", got)
	}
}

func TestFingerprintKeepsTheLastFourAndTheLength(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"long token", strings.Repeat("x", 1440) + "9f3a", "…9f3a (1444 chars)"},
		{"short enough to guess", "secret", "(6 chars)"},
		{"empty", "", "(empty)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Fingerprint(tt.in); got != tt.want {
				t.Errorf("Fingerprint() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTheHandlerStillReportsWhatItWraps(t *testing.T) {
	h := redact(slog.NewJSONHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelWarn}))
	if h.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("Enabled(info) = true on a warn handler")
	}
	if !h.Enabled(context.Background(), slog.LevelError) {
		t.Error("Enabled(error) = false on a warn handler")
	}
}
