package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"tyrtop.com/iontk/internal/output"
	"tyrtop.com/iontk/internal/script"
)

type Run struct {
	Workflow string            `json:"workflow"`
	Tenant   string            `json:"tenant,omitempty"`
	TSG      string            `json:"tsg,omitempty"`
	Inputs   map[string]string `json:"inputs,omitempty"`
	DoIt     bool              `json:"doit"`
	Started  time.Time         `json:"started"`
	Finished time.Time         `json:"finished"`
	Status   []string          `json:"status"`
	Writes   []script.Write    `json:"writes,omitempty"`
	Data     any               `json:"data,omitempty"`
	Error    string            `json:"error,omitempty"`
}

type runsState struct {
	runs []Run
}

func (rt *Runtime) BeginRuns(n int) {
	rt.mu.Lock()
	rt.runs = runsState{runs: make([]Run, n)}
	rt.mu.Unlock()
}

func (rt *Runtime) EndRun(at int, r Run) {
	rt.mu.Lock()
	if at >= 0 && at < len(rt.runs.runs) {
		rt.runs.runs[at] = r
	}
	rt.mu.Unlock()
}

func (rt *Runtime) Runs() []Run {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	var out []Run
	for _, r := range rt.runs.runs {
		if r.Workflow != "" {
			out = append(out, r)
		}
	}
	return out
}

func RunFormat(f string) (string, error) {
	f, err := output.NormalizeFormat(f)
	if err != nil {
		return "", err
	}
	if f != output.FormatJSON && f != output.FormatJSONL {
		return "", fmt.Errorf("a workflow run is saved as json or jsonl, not %s", f)
	}
	return f, nil
}

func encodeRuns(runs []Run, format string) (string, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	if format == output.FormatJSONL {
		for _, r := range runs {
			if err := enc.Encode(r); err != nil {
				return "", err
			}
		}
		return b.String(), nil
	}
	enc.SetIndent("", "  ")
	if err := enc.Encode(runs); err != nil {
		return "", err
	}
	return b.String(), nil
}

func runsCommands() []Command {
	return []Command{{
		Name: "runs.save", Help: "save the last workflow run as JSON",
		Args: []Arg{{Name: "to", Help: "Write to `file` instead of a new file under exports/ (\"-\" for standard output)"},
			{Name: "format", Default: output.FormatJSON, Help: "Output format: {json|jsonl}"}},
		Run: func(rt *Runtime, c *Call) error {
			format, err := RunFormat(c.Arg("format"))
			if err != nil {
				return err
			}
			runs := rt.Runs()
			if len(runs) == 0 {
				return fmt.Errorf("no workflow run to save")
			}
			text, err := encodeRuns(runs, format)
			if err != nil {
				return err
			}
			what := "the run"
			if len(runs) > 1 {
				what = fmt.Sprintf("%d runs", len(runs))
			}
			if c.Arg("to") == "" {
				where, err := rt.Export(runs[0].Workflow, format, text)
				if err == nil {
					c.Say("saved %s to %s", what, where)
					c.Result = where
				}
				return err
			}
			out, where, err := output.Open(c.Arg("to"), "", format, time.Now())
			if err != nil {
				return err
			}
			if _, err := io.WriteString(out, text); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
			if where != "stdout" {
				c.Say("saved %s to %s", what, where)
			}
			c.Result = where
			return nil
		},
	}}
}
