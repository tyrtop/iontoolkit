package logging

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"
)

var secretKeys = map[string]bool{
	"token":         true,
	"access_token":  true,
	"refresh_token": true,
	"id_token":      true,
	"refresh":       true,
	"secret":        true,
	"client_secret": true,
	"password":      true,
	"pass":          true,
	"code":          true,
	"code_verifier": true,
	"verifier":      true,
	"authorization": true,
	"cookie":        true,
	"seed":          true,
	"api_key":       true,
}

var secretNamed = regexp.MustCompile(
	`(?i)\b(access_token|refresh_token|id_token|client_secret|code_verifier|verifier|password)\b["']?\s*[:=]\s*["']?([A-Za-z0-9._~+/=-]{4,})`)

var secretParam = regexp.MustCompile(
	`(?i)\b(token|secret|pass|code)=["']?([A-Za-z0-9._~+/=-]{4,})`)

func Fingerprint(secret string) string {
	switch n := len(secret); {
	case n == 0:
		return "(empty)"
	case n < 8:
		return fmt.Sprintf("(%d chars)", n)
	default:
		return fmt.Sprintf("…%s (%d chars)", secret[n-4:], n)
	}
}

const maskMin = 8

var (
	maskMu   sync.RWMutex
	masked   = map[string]bool{}
	replacer *strings.Replacer
)

func Mask(secret string) {
	if len(secret) < maskMin {
		return
	}
	maskMu.Lock()
	defer maskMu.Unlock()
	if masked[secret] {
		return
	}
	masked[secret] = true

	keys := make([]string, 0, len(masked))
	for k := range masked {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })

	pairs := make([]string, 0, 2*len(keys))
	for _, k := range keys {
		pairs = append(pairs, k, Fingerprint(k))
	}
	replacer = strings.NewReplacer(pairs...)
}

func masker() *strings.Replacer {
	maskMu.RLock()
	defer maskMu.RUnlock()
	return replacer
}

func scrubText(s string) string {
	if s == "" {
		return s
	}
	if r := masker(); r != nil {
		s = r.Replace(s)
	}
	for _, re := range []*regexp.Regexp{secretNamed, secretParam} {
		s = re.ReplaceAllStringFunc(s, func(m string) string {
			g := re.FindStringSubmatch(m)
			return strings.TrimSuffix(m, g[2]) + Fingerprint(g[2])
		})
	}
	return s
}

func scrubAttr(a slog.Attr) slog.Attr {
	if a.Value.Kind() == slog.KindGroup {
		in := a.Value.Group()
		out := make([]slog.Attr, len(in))
		for i, g := range in {
			out[i] = scrubAttr(g)
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(out...)}
	}
	if secretKeys[strings.ToLower(a.Key)] {
		return slog.String(a.Key, Fingerprint(fmt.Sprint(a.Value.Any())))
	}
	if a.Value.Kind() == slog.KindString {
		return slog.String(a.Key, scrubText(a.Value.String()))
	}
	if a.Value.Kind() == slog.KindAny {
		if err, ok := a.Value.Any().(error); ok && err != nil {
			return slog.String(a.Key, scrubText(err.Error()))
		}
	}
	return a
}

func scrubAll(attrs []slog.Attr) []slog.Attr {
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		out[i] = scrubAttr(a)
	}
	return out
}

type redactor struct{ slog.Handler }

func redact(h slog.Handler) slog.Handler { return redactor{h} }

func (r redactor) Handle(ctx context.Context, rec slog.Record) error {
	safe := slog.NewRecord(rec.Time, rec.Level, scrubText(rec.Message), rec.PC)
	rec.Attrs(func(a slog.Attr) bool {
		safe.AddAttrs(scrubAttr(a))
		return true
	})
	return r.Handler.Handle(ctx, safe)
}

func (r redactor) WithAttrs(attrs []slog.Attr) slog.Handler {
	return redactor{r.Handler.WithAttrs(scrubAll(attrs))}
}

func (r redactor) WithGroup(name string) slog.Handler {
	return redactor{r.Handler.WithGroup(name)}
}
