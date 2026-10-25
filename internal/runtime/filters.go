package runtime

import (
	"fmt"
	"strings"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/sase"
)

func has(q string, fields ...string) bool {
	if q = strings.ToLower(strings.TrimSpace(q)); q == "" {
		return true
	}
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), q) {
			return true
		}
	}
	return false
}

func (rt *Runtime) SetResultsFilter(q string) {
	rt.mu.Lock()
	rt.resultsFilter = q
	rt.mu.Unlock()
	rt.changed(Event{Area: AreaBatch})
}

func (rt *Runtime) ResultsFilter() string {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.resultsFilter
}

func (rt *Runtime) ResultMatches() []int {
	q := rt.ResultsFilter()
	var out []int
	for i, r := range rt.Batch().Results {
		if r.ElementID != "" && has(q, ResultName(r), r.ElementID, r.Model) {
			out = append(out, i)
		}
	}
	return out
}

func ResultName(r sase.Result) string {
	if r.Name != "" {
		return r.Name
	}
	return r.ElementID
}

func ResultText(r sase.Result) []string {
	var lines []string
	if r.Error != "" {
		lines = append(lines, r.Error)
	}
	if r.HWID != "" || r.Model != "" {
		lines = append(lines, fmt.Sprintf("%s  %s  %s  connected=%v", r.HWID, r.Model, r.Software, r.Connected))
	}
	for _, c := range r.Commands {
		lines = append(lines, "", c.Command)
		for _, out := range strings.Split(strings.TrimRight(c.Output, "\n"), "\n") {
			lines = append(lines, "  "+out)
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "no output")
	}
	return lines
}

func (rt *Runtime) MacrosMatching(q string) []config.Macro {
	var out []config.Macro
	for _, m := range rt.Macros() {
		if has(q, m.Name, strings.Join(m.Commands, " ")) {
			out = append(out, m)
		}
	}
	return out
}

func (rt *Runtime) CommandsMatching(prefix, q string) []Command {
	var out []Command
	for _, c := range rt.CommandsNamed(prefix) {
		if has(q, c.Name, c.Help, c.Long) {
			out = append(out, c)
		}
	}
	return out
}

func resultCommands() []Command {
	return []Command{{
		Name: "batch.export", Help: "write one element's output from the last run to a file under exports/: element=ID",
		Args: []Arg{{Name: "element", Required: true}},
		Run: func(rt *Runtime, c *Call) error {
			for _, r := range rt.Batch().Results {
				if r.ElementID != c.Arg("element") {
					continue
				}
				where, err := rt.Export(ResultName(r), "txt", strings.Join(ResultText(r), "\n"))
				if err == nil {
					c.Say("exported %s output to %s", ResultName(r), where)
					c.Result = where
				}
				return err
			}
			return fmt.Errorf("the last run has no element %s", c.Arg("element"))
		},
	}}
}
