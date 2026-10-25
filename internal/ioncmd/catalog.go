package ioncmd

import (
	"embed"
	"strings"
	"sync"
)

//go:embed reference/ion1200.tree
var reference embed.FS

const ReferenceModel = "ion 1200"

var (
	once     sync.Once
	embedded []string
)

func Embedded() []string {
	once.Do(func() {
		b, err := reference.ReadFile("reference/ion1200.tree")
		if err != nil {
			return
		}
		embedded = ParseTree(strings.NewReader(string(b)))
	})
	return embedded
}

func Fuzzy(cmds []string, query string) []string {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return cmds
	}

	type scored struct {
		cmd   string
		score int
	}
	var hits []scored

	for _, c := range cmds {
		if s, ok := score(strings.ToLower(c), q); ok {
			hits = append(hits, scored{c, s})
		}
	}

	for i := 1; i < len(hits); i++ {
		for j := i; j > 0 && hits[j].score < hits[j-1].score; j-- {
			hits[j], hits[j-1] = hits[j-1], hits[j]
		}
	}

	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.cmd
	}
	return out
}

func score(cmd, query string) (int, bool) {
	if i := strings.Index(cmd, query); i >= 0 {
		return i, true
	}

	total := 0
	at := 0
	prev := -2
	for _, qr := range query {
		if qr == ' ' {
			continue
		}
		idx := strings.IndexRune(cmd[at:], qr)
		if idx < 0 {
			return 0, false
		}
		pos := at + idx
		gap := pos - prev - 1
		if gap < 0 {
			gap = 0
		}
		total += 100 + gap
		prev = pos
		at = pos + 1
	}
	return total, true
}

func ReadOnly(cmds []string, allowed func(string) bool) []string {
	out := make([]string, 0, len(cmds))
	for _, c := range cmds {
		if allowed(c) {
			out = append(out, c)
		}
	}
	return out
}
