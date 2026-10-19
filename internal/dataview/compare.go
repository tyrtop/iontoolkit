package dataview

import (
	"context"
	"strings"
)

// DeviceOutput is what the appliance printed, keyed by the command itself, so
// two views that ask the same question share one run of it.
type DeviceOutput map[string]string

// Verdict is what the two sources said about one row.
type Verdict int

const (
	// Unknown is one side saying nothing at all: a field this firmware does
	// not print, or a record that has not got it. That is not a disagreement,
	// and marking it as one would cry wolf on every element.
	Unknown Verdict = iota
	Agree
	Differ
)

// Compared is one row: both answers and the verdict between them.
type Compared struct {
	Label   string
	Portal  string
	Device  string
	Verdict Verdict
}

// Comparison is one view's worth of comparison.
type Comparison struct {
	View string
	Rows []Compared
	// Commands is what the device was asked, so a row that reads wrong can be
	// run by hand.
	Commands []string
	// Errs is the portal fetches that failed, which is why a portal side may
	// be blank.
	Errs []string
}

// CompareViews are the views whose comparison applies to this element. The
// gate is checked before anything is dialled: asking an unclustered appliance
// about HA costs the same session as asking a clustered one.
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

// CompareCommands is every command those views ask for, deduplicated. The
// whole page's device half is one session: a dial and a login cost seconds and
// a session slot on the appliance.
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

// FetchCompare reads the portal side of a comparison.
func FetchCompare(ctx context.Context, g Getter, v View, vars Vars) (*Data, error) {
	spec := View{Name: v.Name}
	if v.Compare != nil {
		spec.Fetch = v.Compare.Fetch
	}
	return Fetch(ctx, g, spec, vars)
}

// RenderCompare puts the two sides of each row beside each other. The device
// output is in scope as .cli.<name>, as text: the appliance prints
// "Key : value" lines, which the kv helper reads.
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

// verdict folds case and runs of whitespace before comparing: a portal
// "Active" and a device "active" are the same answer. Anything beyond that is
// the row's own job, which is why both sides are templates.
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
