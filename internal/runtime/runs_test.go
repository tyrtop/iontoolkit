package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func savedRuns(t *testing.T, rt *Runtime, args map[string]string) (string, []string) {
	t.Helper()
	var said []string
	c := &Call{Args: args, Out: func(l string) { said = append(said, l) }}
	if err := rt.Exec(c, "runs.save"); err != nil {
		t.Fatalf("runs.save %v: %v", args, err)
	}
	where, _ := c.Result.(string)
	b, err := os.ReadFile(where)
	if err != nil {
		t.Fatal(err)
	}
	return string(b), said
}

func TestRunsSaveKeepsTheRunsInTheOrderTheyWereGiven(t *testing.T) {
	rt, st := withConfig(t)
	rt.BeginRuns(2)
	rt.EndRun(1, Run{Workflow: "ion-bw-test", Tenant: "Acme Co", Inputs: map[string]string{"router": "S5037-RTR-02"},
		Data: map[string]any{"mbps": 192.69}})
	rt.EndRun(0, Run{Workflow: "ion-bw-test", Tenant: "Acme Co", Inputs: map[string]string{"router": "S5037-RTR-01"},
		Error: "no router called S5037-RTR-01 in this tenant"})

	text, said := savedRuns(t, rt, nil)
	var runs []Run
	if err := json.Unmarshal([]byte(text), &runs); err != nil {
		t.Fatalf("the export does not read back as JSON: %v\n%s", err, text)
	}
	data, _ := runs[len(runs)-1].Data.(map[string]any)
	if len(runs) != 2 || runs[0].Inputs["router"] != "S5037-RTR-01" || runs[0].Error == "" || data["mbps"] != 192.69 {
		t.Errorf("runs read back as %+v, want RTR-01's failure first and RTR-02's numbers second", runs)
	}
	if len(said) != 1 || !strings.Contains(said[0], "saved 2 runs to "+st.ExportsDir()) || !strings.HasSuffix(said[0], ".json") {
		t.Errorf("runs.save said %q, want where the file went", said)
	}

	lines, _ := savedRuns(t, rt, map[string]string{"format": "jsonl", "to": filepath.Join(t.TempDir(), "runs.jsonl")})
	rows := strings.Split(strings.TrimSpace(lines), "\n")
	var first Run
	if len(rows) != 2 || json.Unmarshal([]byte(rows[0]), &first) != nil || first.Inputs["router"] != "S5037-RTR-01" {
		t.Errorf("jsonl came out as %q, want one run per line, RTR-01 first", lines)
	}
}

func TestRunsSaveSaysWhyItCannotSave(t *testing.T) {
	rt, _ := withConfig(t)
	for _, tt := range []struct {
		name string
		ran  bool
		args map[string]string
		want string
	}{
		{"before any run", false, nil, "no workflow run to save"},
		{"as csv", true, map[string]string{"format": "csv"}, "saved as json or jsonl, not csv"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rt.BeginRuns(0)
			if tt.ran {
				rt.BeginRuns(1)
				rt.EndRun(0, Run{Workflow: "ion-bw-test"})
			}
			err := rt.Exec(&Call{Args: tt.args}, "runs.save")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("runs.save = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestASessionScreenComesBackWithoutItsPadding(t *testing.T) {
	pad := strings.Repeat(" ", 160)
	screen := "028201-004589-4901 login: cis-superuser" + pad + "\n" +
		"\x1b[31mS5037-RTR-01#\x1b[m debug bw-test src-interface 3" + pad + "\n" +
		pad + "\n" +
		" Final Uplink capacity estimate: 191.15" + pad + "\n" +
		pad + "\n" + pad
	want := "028201-004589-4901 login: cis-superuser\n" +
		"\x1b[31mS5037-RTR-01#\x1b[m debug bw-test src-interface 3\n" +
		"\n" +
		" Final Uplink capacity estimate: 191.15"
	if got := screenText(screen); got != want {
		t.Errorf("screenText = %q, want %q", got, want)
	}
}
