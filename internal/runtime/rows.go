package runtime

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"tyrtop.com/iontk/internal/column"
	"tyrtop.com/iontk/internal/search"
	"tyrtop.com/iontk/internal/tmpl"
)

type Field func(rt *Runtime, el Element) any

var ElementFields = map[string]Field{
	"id":               func(_ *Runtime, el Element) any { return el.ID },
	"name":             func(_ *Runtime, el Element) any { return el.Name },
	"label":            func(_ *Runtime, el Element) any { return el.Label() },
	"model_name":       func(_ *Runtime, el Element) any { return el.Model },
	"serial_number":    func(_ *Runtime, el Element) any { return el.Serial },
	"software_version": func(_ *Runtime, el Element) any { return el.Software },
	"hw_id":            func(_ *Runtime, el Element) any { return el.HWID },
	"site_id":          func(_ *Runtime, el Element) any { return el.SiteID },
	"state":            func(_ *Runtime, el Element) any { return el.State },
	"role":             func(_ *Runtime, el Element) any { return el.Role },
	"description":      func(_ *Runtime, el Element) any { return el.Description },
	"cluster_id":       func(_ *Runtime, el Element) any { return el.ClusterID() },
	"connected":        func(_ *Runtime, el Element) any { return el.Connected },
	"tenant":           func(_ *Runtime, el Element) any { return el.Tenant },
	"tsg":              func(_ *Runtime, el Element) any { return el.TSG },
	"site_name":        func(rt *Runtime, el Element) any { return rt.SiteName(el) },
}

var SiteFields = map[string]Field{
	"id":   func(_ *Runtime, el Element) any { return el.SiteID },
	"name": func(rt *Runtime, el Element) any { return rt.SiteName(el) },
}

func CheckJoins(set *column.Set) error {
	for _, c := range set.Columns {
		if !c.Fleet() || TableSubject(c.Fetch) != "" {
			continue
		}
		if _, ok := ElementFields[c.On]; !ok {
			names := make([]string, 0, len(ElementFields))
			for name := range ElementFields {
				names = append(names, name)
			}
			sort.Strings(names)
			return fmt.Errorf("column %q joins on %q, and an element has %s", c.Name, c.On, strings.Join(names, ", "))
		}
	}
	return nil
}

func FieldsFor(subject string) map[string]Field {
	if subject == column.FromSite {
		return SiteFields
	}
	return ElementFields
}

func (rt *Runtime) SiteName(el Element) string {
	return search.Of(el.Element, el.TSG, el.Tenant, rt.Sites()).SiteLabel()
}

func AsText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "yes"
		}
		return "no"
	}
	return fmt.Sprint(v)
}

var fieldRef = regexp.MustCompile(`\.([A-Za-z_][A-Za-z0-9_]*)`)

func flatEnough(text, subject string) bool {
	fields := FieldsFor(subject)
	for _, m := range fieldRef.FindAllStringSubmatch(text, -1) {
		if _, ok := fields[m[1]]; !ok {
			return false
		}
	}
	return true
}

func loneField(text string) (string, bool) {
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, "{{") || !strings.HasSuffix(t, "}}") {
		return "", false
	}
	t = strings.TrimSpace(t[2 : len(t)-2])
	if !strings.HasPrefix(t, ".") {
		return "", false
	}
	t = t[1:]
	if t == "" || strings.ContainsAny(t, " .|({") {
		return "", false
	}
	return t, true
}

type row struct {
	rt   *Runtime
	el   Element
	site map[string]any
	flat map[string]any
	full map[string]any
}

func (rt *Runtime) row(el Element) *row { return &row{rt: rt, el: el} }

func (r *row) context(subject string, flat bool) map[string]any {
	switch {
	case subject == column.FromSite:
		if r.site == nil {
			r.site = map[string]any{"id": r.el.SiteID, "name": r.rt.SiteName(r.el)}
		}
		return r.site
	case flat:
		if r.flat == nil {
			r.flat = make(map[string]any, len(ElementFields))
			for name, read := range ElementFields {
				r.flat[name] = read(r.rt, r.el)
			}
		}
		return r.flat
	}
	if r.full == nil {
		r.full = map[string]any{}
		if b, err := json.Marshal(r.el.Element); err == nil {
			_ = json.Unmarshal(b, &r.full)
		}
		r.full["site_name"] = r.rt.SiteName(r.el)
		r.full["label"] = r.el.Label()
		r.full["tenant"] = r.el.Tenant
		r.full["tsg"] = r.el.TSG
	}
	return r.full
}

func (r *row) render(t *tmpl.T, subject string, with map[string]any) string {
	if with != nil {
		return t.Render(with)
	}
	if name, ok := loneField(t.Text()); ok {
		if read, ok := FieldsFor(subject)[name]; ok {
			return AsText(read(r.rt, r.el))
		}
	}
	return t.Render(r.context(subject, flatEnough(t.Text(), subject)))
}

func (rt *Runtime) RowContext(el Element, subject string, text string) map[string]any {
	return rt.row(el).context(subject, flatEnough(text, subject))
}

func (rt *Runtime) Joined(spec column.Spec, el Element) (map[string]any, bool) {
	rows := rt.Table(spec.Fetch, el.TSG)
	read, ok := ElementFields[spec.On]
	if rows == nil || !ok {
		return nil, false
	}
	got, ok := rows[AsText(read(rt, el))]
	return got, ok
}

func (r *row) text(spec column.Spec) string {
	rt, el := r.rt, r.el
	switch {
	case spec.Go == "ha":
		word, cached := rt.HAState(el)
		if cached && word != "" {
			return word + "*"
		}
		return word
	case spec.Go != "":
		return ""
	case spec.PerElement():
		cell, ok := rt.Cell(spec.Name, el.Key())
		switch {
		case ok && cell.Failed:
			return "?"
		case ok && cell.Text != "" && cell.Cached:
			return cell.Text + "*"
		case ok && cell.Text != "":
			return cell.Text
		case !ok && rt.CellsWorking():
			return "loading"
		}
		return "-"
	case spec.Fleet() && TableSubject(spec.Fetch) == "":
		joined, ok := rt.Joined(spec, el)
		if !ok {
			if rt.TableWorking(spec.Fetch) {
				return "loading"
			}
			return "-"
		}
		return r.render(spec.Template, "", joined)
	case spec.Template != nil:
		subject := spec.Subject()
		if spec.Fleet() {
			subject = TableSubject(spec.Fetch)
		}
		return r.render(spec.Template, subject, nil)
	}
	return ""
}

func (rt *Runtime) RowText(specs []column.Spec, el Element) []string {
	r := rt.row(el)
	out := make([]string, len(specs))
	for i, s := range specs {
		out[i] = r.text(s)
	}
	return out
}

func (rt *Runtime) ListWhen() map[string]any {
	rows := rt.Elements()
	clustered, serials := false, false
	for _, r := range rows {
		clustered = clustered || r.ClusterID() != ""
		serials = serials || r.Serial != ""
	}
	n, _ := rt.Scope()
	return map[string]any{"elements": len(rows), "tenants": n, "serials": serials, "clustered": clustered}
}

func (rt *Runtime) Shown(when *tmpl.T) bool {
	return when == nil || tmpl.Truthy(when.Render(rt.ListWhen()))
}

func (rt *Runtime) CellText(spec column.Spec, el Element) string { return rt.row(el).text(spec) }
