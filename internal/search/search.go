package search

import (
	"strings"

	"tyrtop.com/iontk/internal/sase/api"
)

type SiteNamer interface {
	SiteName(tsg, siteID string) (string, bool)
}

type SiteMap map[string]string

func (s SiteMap) SiteName(tsg, siteID string) (string, bool) {
	n, ok := s[tsg+"/"+siteID]
	return n, ok && n != ""
}

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
	Serial    string
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
		Serial:    el.Serial,
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

func (r Record) SiteLabel() string {
	if r.Site == "" {
		return "-"
	}
	return r.Site
}

type Field struct {
	Value string
	Exact bool
}

func (r Record) Fields() []Field {
	return []Field{
		{Value: strings.ToLower(r.Name)},
		{Value: strings.ToLower(r.ID), Exact: true},
		{Value: strings.ToLower(r.Site), Exact: !r.SiteKnown},
		{Value: strings.ToLower(r.Tenant)},
		{Value: strings.ToLower(r.Model)},
		{Value: strings.ToLower(r.HWID), Exact: true},
		{Value: strings.ToLower(r.Serial), Exact: true},
		{Value: strings.ToLower(r.Software)},
		{Value: strings.ToLower(r.TSG), Exact: true},
		{Value: r.State()},
	}
}

type Query []string

func Parse(s string) Query { return strings.Fields(strings.ToLower(s)) }

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
