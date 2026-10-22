package tmpl

import (
	"fmt"
	"strconv"
	"strings"
	"text/template"
	"time"

	"tyrtop.com/iontk/internal/clock"
	"tyrtop.com/iontk/internal/sase/api"
)

type T struct {
	text string
	t    *template.Template
}

func Compile(text string) (*T, error) {
	t, err := template.New("t").Funcs(Funcs).Option("missingkey=zero").Parse(text)
	if err != nil {
		return nil, err
	}
	return &T{text: text, t: t}, nil
}

func (t *T) Text() string { return t.text }

func (t *T) Render(ctx map[string]any) string {
	var b strings.Builder
	if err := t.t.Execute(&b, ctx); err != nil {
		if missingField(err) {
			return ""
		}
		return "!" + err.Error()
	}
	return strings.TrimSpace(strings.ReplaceAll(b.String(), "<no value>", ""))
}

// a field the element has not got comes out empty, not "<no value>": half of
// what a file asks for is optional on a given appliance. A file's own mistakes
// do show, prefixed "!", because nothing else would tell whoever wrote it.
func Eval(text string, ctx map[string]any) string {
	t, err := Compile(text)
	if err != nil {
		return "!" + err.Error()
	}
	return t.Render(ctx)
}

func missingField(err error) bool {
	s := err.Error()
	return strings.Contains(s, "nil pointer evaluating") ||
		strings.Contains(s, "can't evaluate field")
}

func Truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "false", "0", "<no value>", "null", "[]", "map[]":
		return false
	}
	return true
}

// a read of the SASE SD-WAN API and nothing else: no host of its own, no
// method, no escaping /sdwan with ".."
func CheckPath(p string) error {
	if p == "" {
		return fmt.Errorf("is empty")
	}
	if strings.Contains(p, "://") || strings.HasPrefix(p, "//") {
		return fmt.Errorf("%q names a host; a file gives a path like /sdwan/v3.2/api/elements/{{ .element_id }}", p)
	}
	if !strings.HasPrefix(p, "/sdwan/") {
		return fmt.Errorf("%q does not start with /sdwan/", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return fmt.Errorf("%q walks up out of /sdwan", p)
		}
	}
	return nil
}

var Funcs = template.FuncMap{
	"utc": func(v any) string {
		n := toInt64(v)
		if n == 0 {
			return ""
		}
		return clock.Stamp(api.UTCTime(n))
	},
	"ago": func(v any) string {
		n := toInt64(v)
		if n == 0 {
			return ""
		}
		t := api.UTCTime(n)
		if t.IsZero() {
			return ""
		}
		return ageString(time.Since(t))
	},
	"unwrap": func(v any) any {
		m, ok := v.(map[string]any)
		if !ok {
			return v
		}
		items, ok := m["items"].([]any)
		if !ok || len(items) == 0 {
			return v
		}
		return items[0]
	},
	"count": func(v any) int {
		switch t := v.(type) {
		case []any:
			return len(t)
		case map[string]any:
			return len(t)
		case string:
			return len(t)
		}
		return 0
	},
	"join": func(v any, sep string) string {
		list, ok := v.([]any)
		if !ok {
			return ""
		}
		parts := make([]string, 0, len(list))
		for _, e := range list {
			parts = append(parts, fmt.Sprint(e))
		}
		return strings.Join(parts, sep)
	},
	"yesno": func(v any) string {
		if b, ok := v.(bool); ok && b {
			return "yes"
		}
		return "no"
	},
	"dash": func(v any) string {
		s := strings.TrimSpace(fmt.Sprint(v))
		if s == "" || s == "<nil>" {
			return "-"
		}
		return s
	},
	"lookup": func(list any, field string, want any) any {
		items, ok := list.([]any)
		if !ok || want == nil || want == "" {
			return nil
		}
		for _, it := range items {
			rec, ok := it.(map[string]any)
			if !ok {
				continue
			}
			switch have := rec[field].(type) {
			case []any:
				for _, h := range have {
					if sameValue(h, want) {
						return rec
					}
				}
			default:
				if sameValue(have, want) {
					return rec
				}
			}
		}
		return nil
	},
	"first": func(v any) any {
		list, ok := v.([]any)
		if !ok || len(list) == 0 {
			return ""
		}
		return list[0]
	},

	"lines": func(v any) []any {
		return textLines(v, "")
	},
	"grep": func(v any, pat string) []any {
		return textLines(v, pat)
	},
	"kv": func(v any, key string) string {
		k := strings.ToLower(strings.TrimSpace(key))
		for _, line := range strings.Split(fmt.Sprint(v), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(strings.ToLower(line), k) {
				continue
			}
			return strings.TrimSpace(strings.TrimLeft(line[len(k):], " \t:="))
		}
		return ""
	},
	"field": func(v any, n int) string {
		f := strings.Fields(fmt.Sprint(v))
		if n < 1 || n > len(f) {
			return ""
		}
		return f[n-1]
	},
}

func textLines(v any, want string) []any {
	want = strings.ToLower(want)
	var out []any
	for _, line := range strings.Split(fmt.Sprint(v), "\n") {
		line = strings.TrimRight(line, " \t\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if want != "" && !strings.Contains(strings.ToLower(line), want) {
			continue
		}
		out = append(out, line)
	}
	return out
}

func sameValue(a, b any) bool {
	if a == nil || b == nil {
		return false
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

func toInt64(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case int64:
		return t
	case int:
		return int64(t)
	case string:
		n, _ := strconv.ParseInt(t, 10, 64)
		return n
	}
	return 0
}

func ageString(d time.Duration) string {
	switch {
	case d < 0:
		return "in the future"
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
