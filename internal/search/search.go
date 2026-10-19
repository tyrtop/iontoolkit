// Package search matches a typed query against elements. The TUI's filter and
// the CLI's --search share it so that the same query cannot find different
// elements depending on where it was typed.
package search

import (
	"strings"

	"tyrtop.com/iontk/internal/sase/api"
)

// SiteNamer resolves a site ID to its name. Two tenants can use the same site
// identifier for different places, so the tenant is part of the key.
type SiteNamer interface {
	SiteName(tsg, siteID string) (string, bool)
}

// SiteMap is the usual SiteNamer: names keyed by "tsg/siteID".
type SiteMap map[string]string

func (s SiteMap) SiteName(tsg, siteID string) (string, bool) {
	n, ok := s[tsg+"/"+siteID]
	return n, ok && n != ""
}

// Record is one element flattened into the values a query may match.
type Record struct {
	Name      string
	ID        string
	Site      string
	SiteKnown bool
	Tenant    string
	TSG       string
	Model     string
	Software  string
	HWID      string
	Connected bool
}

func Of(el api.Element, tsg, tenant string, sites SiteNamer) Record {
	r := Record{
		Name:      el.Name,
		ID:        el.ID,
		Tenant:    tenant,
		TSG:       tsg,
		Model:     el.Model,
		Software:  el.Software,
		HWID:      el.HWID,
		Connected: el.Connected,
	}
	if sites != nil {
		r.Site, r.SiteKnown = sites.SiteName(tsg, el.SiteID)
	}
	if !r.SiteKnown {
		r.Site = el.SiteID
	}
	return r
}

func (r Record) State() string {
	if r.Connected {
		return "connected"
	}
	return "offline"
}

// SiteLabel is the site column, which falls back to the raw ID and then to a
// placeholder so the column is never blank.
func (r Record) SiteLabel() string {
	if r.Site == "" {
		return "-"
	}
	return r.Site
}

// Field is one value a term can match.
type Field struct {
	Value string
	// Exact restricts this field to whole-value matches. Opaque identifiers
	// are long digit strings, so a partial match in one is almost always
	// coincidence: searching "1200" for a model found an ion 2000 whose site
	// ID happened to contain those digits.
	Exact bool
}

// Fields is everything a term may match against.
func (r Record) Fields() []Field {
	return []Field{
		{Value: strings.ToLower(r.Name)},
		{Value: strings.ToLower(r.ID), Exact: true},
		// A site that did not resolve shows its raw ID, which is as opaque as
		// any other identifier and must not be matched piecemeal.
		{Value: strings.ToLower(r.Site), Exact: !r.SiteKnown},
		{Value: strings.ToLower(r.Tenant)},
		{Value: strings.ToLower(r.Model)},
		{Value: strings.ToLower(r.HWID), Exact: true},
		{Value: strings.ToLower(r.Software)},
		{Value: strings.ToLower(r.TSG), Exact: true},
		{Value: r.State()},
	}
}

// Query is a parsed search string.
type Query []string

// Parse splits on whitespace, which is what makes a query like "s50 ion 1200"
// work: each word can land in a different field, so the store number and the
// model are found together without having to know which field holds what.
func Parse(s string) Query { return strings.Fields(strings.ToLower(s)) }

func (q Query) Empty() bool { return len(q) == 0 }

// Match requires every term to match the record somewhere.
func (q Query) Match(r Record) bool {
	if len(q) == 0 {
		return true
	}
	fields := r.Fields()
	for _, term := range q {
		if !matchesAnyField(term, fields) {
			return false
		}
	}
	return true
}

func matchesAnyField(term string, fields []Field) bool {
	for _, f := range fields {
		if f.Exact {
			if f.Value == term {
				return true
			}
			continue
		}
		if f.Value != "" && strings.Contains(f.Value, term) {
			return true
		}
	}
	return false
}
