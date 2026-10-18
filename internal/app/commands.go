package app

import "strings"

func SplitCommands(raw []string) []string {
	var commands []string
	for _, entry := range raw {
		for _, c := range strings.Split(entry, ",") {
			if c = strings.TrimSpace(c); c != "" {
				commands = append(commands, c)
			}
		}
	}
	return commands
}
