package dataview

import (
	"context"
	"strings"
)

type DeviceOutput map[string]string

type Verdict int

const (
	Unknown Verdict = iota
	Agree
	Differ
)

type Compared struct {
	Label   string
	Portal  string
	Device  string
	Verdict Verdict
}

type Comparison struct {
	View     string
	Rows     []Compared
	Commands []string
	Errs     []string
}

func CompareViews(views []View, vars Vars) []View {
	var out []View
	for _, v := range views {
		if v.Compare == nil {
			continue
		}
		if v.Compare.When != "" && !truthy(eval(v.Compare.When, varsCtx(vars))) {
			continue
		}
		out = append(out, v)
	}
	return out
}

func CompareCommands(views []View) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range views {
		if v.Compare == nil {
			continue
		}
		for _, name := range sortedKeys(v.Compare.Commands) {
			cmd := v.Compare.Commands[name]
			if seen[cmd] {
				continue
			}
			seen[cmd] = true
			out = append(out, cmd)
		}
	}
	return out
}

func FetchCompare(ctx context.Context, g Getter, v View, vars Vars) (*Data, error) {
	spec := View{Name: v.Name}
	if v.Compare != nil {
		spec.Fetch = v.Compare.Fetch
	}
	return Fetch(ctx, g, spec, vars)
}

func RenderCompare(v View, d *Data, out DeviceOutput) Comparison {
	c := Comparison{View: v.Name}
	if v.Compare == nil {
		return c
	}
	for _, name := range sortedKeys(d.Errs) {
		c.Errs = append(c.Errs, name+": "+d.Errs[name])
	}

	ctx := varsCtx(d.Vars)
	for k, val := range d.Bodies {
		ctx[k] = val
	}
	cli := map[string]string{}
	for name, cmd := range v.Compare.Commands {
		cli[name] = out[cmd]
	}
	ctx["cli"] = cli
	c.Commands = CompareCommands([]View{v})

	for _, r := range v.Compare.Rows {
		row := Compared{Label: r.Label, Portal: eval(r.Portal, ctx), Device: eval(r.Device, ctx)}
		row.Verdict = verdict(row.Portal, row.Device)
		c.Rows = append(c.Rows, row)
	}
	return c
}

func verdict(portal, device string) Verdict {
	if portal == "" || device == "" {
		return Unknown
	}
	if fold(portal) == fold(device) {
		return Agree
	}
	return Differ
}

func fold(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}
