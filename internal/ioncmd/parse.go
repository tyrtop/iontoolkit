package ioncmd

import (
	"bufio"
	"sort"
	"strings"
)

var connectors = map[rune]bool{'┬': true, '├': true, '└': true, '─': true, '│': true}

type node struct {
	col  int
	text string
}

func ParseTree(r *strings.Reader) []string {
	var (
		seen  = map[string]bool{}
		out   []string
		stack []node
	)

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" || strings.Contains(line, "# help") {
			continue
		}

		runes := []rune(line)
		var toks []node
		for i := 0; i < len(runes); {
			if connectors[runes[i]] || runes[i] == ' ' {
				i++
				continue
			}
			start := i
			for i < len(runes) && !connectors[runes[i]] && runes[i] != ' ' {
				i++
			}
			toks = append(toks, node{col: start, text: string(runes[start:i])})
		}
		if len(toks) == 0 {
			continue
		}

		for _, t := range toks {
			kept := stack[:0]
			for _, e := range stack {
				if e.col < t.col {
					kept = append(kept, e)
				}
			}
			stack = append(kept, t)

			parts := make([]string, len(stack))
			for i, e := range stack {
				parts[i] = e.text
			}
			cmd := strings.Join(parts, " ")
			if !seen[cmd] {
				seen[cmd] = true
				out = append(out, cmd)
			}
		}
	}

	sort.Strings(out)
	return out
}
