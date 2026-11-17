package runtime

import (
	"fmt"
	"strings"
)

func Words(line string) ([]string, error) {
	var out []string
	var w strings.Builder
	in, quote := false, rune(0)
	for _, r := range line {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			w.WriteRune(r)
		case r == '"' || r == '\'':
			quote, in = r, true
		case r == ' ' || r == '\t':
			if in {
				out = append(out, w.String())
				w.Reset()
				in = false
			}
		default:
			w.WriteRune(r)
			in = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("a %c is not closed", quote)
	}
	if in {
		out = append(out, w.String())
	}
	return out, nil
}

func SplitWords(cmd Command, words []string) (map[string]string, []string) {
	declared := map[string]bool{}
	for _, a := range cmd.Args {
		declared[a.Name] = true
	}
	args := map[string]string{}
	var rest []string
	for _, w := range words {
		if k, v, ok := strings.Cut(w, "="); ok && declared[k] {
			args[k] = v
			continue
		}
		rest = append(rest, w)
	}
	return args, rest
}

func (rt *Runtime) Parse(line string) (Command, map[string]string, []string, error) {
	words, err := Words(line)
	if err != nil {
		return Command{}, nil, nil, err
	}
	if len(words) == 0 {
		return Command{}, nil, nil, fmt.Errorf("no command")
	}
	cmd, ok := rt.Command(words[0])
	if !ok {
		return Command{}, nil, nil, fmt.Errorf("no command is called %q", words[0])
	}
	args, rest := SplitWords(cmd, words[1:])
	return cmd, args, rest, nil
}

func (rt *Runtime) Complete(prefix string) []string {
	var out []string
	for _, c := range rt.Commands() {
		if strings.HasPrefix(c.Name, prefix) {
			out = append(out, c.Name)
		}
	}
	return out
}
