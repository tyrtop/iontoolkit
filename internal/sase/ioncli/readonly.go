package ioncli

import (
	"fmt"
	"sort"
	"strings"
)

var readOnlyVerbs = map[string]bool{
	"arping":      true,
	"dig":         true,
	"dig6":        true,
	"dump":        true,
	"help":        true,
	"inspect":     true,
	"nslookup":    true,
	"ping":        true,
	"ping6":       true,
	"tcpping":     true,
	"traceroute":  true,
	"traceroute6": true,
}

func Verb(cmd string) string {
	f := strings.Fields(strings.TrimSpace(cmd))
	if len(f) == 0 {
		return ""
	}
	return strings.ToLower(f[0])
}

func IsReadOnly(cmd string) bool { return readOnlyVerbs[Verb(cmd)] }

func ReadOnlyVerbs() []string {
	out := make([]string, 0, len(readOnlyVerbs))
	for v := range readOnlyVerbs {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func CheckReadOnly(cmds []string) error {
	var bad []string
	for _, c := range cmds {
		if !IsReadOnly(c) {
			bad = append(bad, c)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	plural := "command"
	if len(bad) > 1 {
		plural = "commands"
	}
	return fmt.Errorf("%s not read-only: %s (allowed: %s)",
		plural, strings.Join(bad, ", "), strings.Join(ReadOnlyVerbs(), " "))
}
