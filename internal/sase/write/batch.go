package write

import (
	"context"
	"fmt"
	"strings"

	"tyrtop.com/iontk/internal/sase/api"
)

type Client interface {
	Getter
	Sender
}

type ElementPlans struct {
	Element string
	Plans   []Plan
	Err     string
}

func (w Spec) CheckSet(set map[string]string) error {
	if len(set) == 0 {
		return fmt.Errorf("%s: nothing to set", w.Name)
	}
	for k := range set {
		if _, ok := w.Field(k); !ok {
			return fmt.Errorf("%s has no editable field %q (it has %s)", w.Name, k, strings.Join(w.FieldKeys(), ", "))
		}
	}
	return nil
}

func (w Spec) Picked(rec map[string]any, match []string) bool {
	for _, m := range match {
		if m == "*" || w.Matches(rec, m) {
			return true
		}
	}
	return false
}

func ElementVars(el api.Element) map[string]string {
	return map[string]string{"element_id": el.ID, "site_id": el.SiteID, "el": el.ID, "site": el.SiteID}
}

func PlanElements(ctx context.Context, c Client, w Spec, els []api.Element, match []string, set map[string]string) []ElementPlans {
	out := make([]ElementPlans, 0, len(els))
	for _, el := range els {
		out = append(out, planElement(ctx, c, w, el, match, set))
	}
	return out
}

func planElement(ctx context.Context, c Client, w Spec, el api.Element, match []string, set map[string]string) ElementPlans {
	ep := ElementPlans{Element: el.ID}
	if el.Name != "" {
		ep.Element = el.Name
	}
	if el.SiteID == "" {
		ep.Err = "not on a site in the fleet list"
		return ep
	}
	vars := ElementVars(el)
	recs, err := w.Records(ctx, c, vars)
	if err != nil {
		ep.Err = err.Error()
		return ep
	}
	for _, rec := range recs {
		if !w.Picked(rec, match) {
			continue
		}
		p, err := w.Plan(vars, rec, set)
		if err != nil {
			ep.Err = err.Error()
			return ep
		}
		ep.Plans = append(ep.Plans, p)
	}
	if len(ep.Plans) == 0 {
		ep.Err = "no record matched " + strings.Join(match, ", ")
	}
	return ep
}

func Changes(all []ElementPlans) int {
	n := 0
	for _, ep := range all {
		for _, p := range ep.Plans {
			n += len(p.Changes)
		}
	}
	return n
}

func Unplanned(all []ElementPlans) error {
	n := 0
	for _, ep := range all {
		if ep.Err != "" {
			n++
		}
	}
	if n > 0 {
		return fmt.Errorf("%d of %d elements could not be planned", n, len(all))
	}
	return nil
}

func (g Guard) ApplyAll(ctx context.Context, s Sender, w Spec, all []ElementPlans, done func(element string, p Plan, err error)) (sent, failed int, err error) {
	for _, ep := range all {
		for _, p := range ep.Plans {
			if p.Empty() {
				continue
			}
			_, aerr := g.Apply(ctx, s, w, p)
			if done != nil {
				done(ep.Element, p, aerr)
			}
			if aerr == nil {
				sent++
				continue
			}
			failed++
		}
	}
	if failed > 0 {
		return sent, failed, fmt.Errorf("%d of %d writes failed", failed, sent+failed)
	}
	return sent, failed, Unplanned(all)
}

func PlanLines(all []ElementPlans) []string {
	var out []string
	for _, ep := range all {
		out = append(out, ep.Element)
		if ep.Err != "" {
			out = append(out, "    ! "+ep.Err)
			continue
		}
		for _, p := range ep.Plans {
			if p.Empty() {
				out = append(out, "  "+p.Label+"  (already so)")
				continue
			}
			out = append(out, "  "+p.Label)
			for _, c := range p.Changes {
				out = append(out, fmt.Sprintf("    %-14s %s → %s", c.Field, shownOrDash(c.From), shownOrDash(c.To)))
			}
		}
	}
	return out
}

func shownOrDash(v any) string {
	if s := Show(v); s != "" {
		return s
	}
	return "—"
}
