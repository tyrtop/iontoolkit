// Package fixture is iontk's testing mode: fake tenants, elements and CLI
// sessions read from a YAML file, so the TUI and batch runs can be exercised
// with no appliance and no credentials.
//
// It is deliberately not a mock. The REST calls go through the real client over
// a transport that answers them, and a session goes through the real autologin
// and pager code against a device that plays back the login exchange. What is
// fake is the appliance, not iontk.
package fixture

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/sase/api"
)

// Set is a loaded fixture file.
type Set struct {
	Tenants []Tenant `yaml:"tenants"`
	// UnknownResponse is what the device answers a command it has no response
	// for. "{command}" is replaced with what was typed. The default text is
	// invented, not observed on an appliance.
	UnknownResponse string `yaml:"unknown_response"`
}

type Tenant struct {
	Name     string    `yaml:"name"`
	TSGID    string    `yaml:"tsg_id"`
	Users    []User    `yaml:"users"`
	Elements []Element `yaml:"elements"`
}

type User struct {
	Name     string `yaml:"name"`
	Password string `yaml:"password"`
}

type Element struct {
	Name      string `yaml:"name"`
	ID        string `yaml:"id"`
	HWID      string `yaml:"hw_id"`
	Model     string `yaml:"model"`
	Software  string `yaml:"software"`
	Site      string `yaml:"site"`
	Connected bool   `yaml:"connected"`
	// Pages makes long output stop on a pager prompt and wait for a space,
	// which is the one device behaviour that cost a hardware round trip. The
	// prompt is the one an ion 1200-s running 6.3.4-b2 printed.
	Pages bool `yaml:"pages"`
	// PageLines is how many lines go by before it pages. Zero means the default.
	PageLines int               `yaml:"page_lines"`
	Responses map[string]string `yaml:"responses"`

	tsg string
}

const (
	defaultUser      = "admin"
	defaultPassword  = "ion"
	defaultModel     = "ion 1200"
	defaultSoftware  = "6.3.4-b2"
	defaultPageLines = 20
	defaultUnknown   = "% unknown command: {command}"

	// Verified on an ion 1200-s running 6.3.4-b2. Two of ioncli.DefaultPagers
	// match it, which is why the scraper cuts the earliest match in the line.
	pagerPrompt = "--More--(4%)[Press space to continue, 'q' to quit.]"
)

func Load(path string) (*Set, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read fixtures: %w", err)
	}
	var s Set
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	// A misspelled key is the whole failure mode of a hand-written fixture, and
	// silently ignoring it looks like the fixture not working.
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := s.normalize(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &s, nil
}

func (s *Set) normalize() error {
	if len(s.Tenants) == 0 {
		return fmt.Errorf("no tenants: a fixture with nothing in it would look like a failed sign-in")
	}
	if s.UnknownResponse == "" {
		s.UnknownResponse = defaultUnknown
	}

	seen := map[string]string{}
	for i := range s.Tenants {
		t := &s.Tenants[i]
		if t.Name == "" {
			return fmt.Errorf("tenant %d has no name", i+1)
		}
		if t.TSGID == "" {
			t.TSGID = syntheticID("tsg/"+t.Name, 9)
		}
		if len(t.Users) == 0 {
			t.Users = []User{{Name: defaultUser, Password: defaultPassword}}
		}
		for j := range t.Users {
			if t.Users[j].Name == "" {
				return fmt.Errorf("tenant %s: user %d has no name", t.Name, j+1)
			}
			if t.Users[j].Password == "" {
				t.Users[j].Password = defaultPassword
			}
		}
		for j := range t.Elements {
			el := &t.Elements[j]
			if el.Name == "" {
				return fmt.Errorf("tenant %s: element %d has no name", t.Name, j+1)
			}
			if el.ID == "" {
				// 19 digits, because that is what the API returns and what the
				// filter's own tests are built around.
				el.ID = syntheticID("element/"+t.TSGID+"/"+el.Name, 19)
			}
			if where, dup := seen[el.ID]; dup {
				return fmt.Errorf("element id %s is used by both %s and %s", el.ID, where, el.Name)
			}
			seen[el.ID] = el.Name
			if el.Model == "" {
				el.Model = defaultModel
			}
			if el.Software == "" {
				el.Software = defaultSoftware
			}
			if el.HWID == "" {
				el.HWID = "FAKE" + syntheticID("hw/"+el.ID, 8)
			}
			if el.PageLines <= 0 {
				el.PageLines = defaultPageLines
			}
			el.tsg = t.TSGID
		}
	}
	return nil
}

// syntheticID is a stable digit string for a name, so an element keeps its ID
// across runs and a cached fixture still resolves.
func syntheticID(seed string, digits int) string {
	sum := sha256.Sum256([]byte(seed))
	var b strings.Builder
	for i := 0; b.Len() < digits; i += 8 {
		fmt.Fprintf(&b, "%019d", binary.BigEndian.Uint64(sum[i%24:])%1e19)
	}
	out := b.String()[:digits]
	// A leading zero would read as a shorter number than the API ever returns.
	if out[0] == '0' {
		out = "1" + out[1:]
	}
	return out
}

// SavedTenants are the tenants to write into the config store, so the TUI's
// ordinary tenant flow is what runs.
func (s *Set) SavedTenants() []config.Tenant {
	out := make([]config.Tenant, 0, len(s.Tenants))
	for _, t := range s.Tenants {
		ct := config.Tenant{
			Name:  t.Name,
			TSGID: t.TSGID,
			// A service account, because the browser legs would need a browser.
			Auth:     config.AuthServiceAccount,
			ClientID: "fixture",
			Added:    true,
		}
		for i, u := range t.Users {
			ct.Users = append(ct.Users, config.IONUser{Name: u.Name, Default: i == 0})
		}
		out = append(out, ct)
	}
	return out
}

// Passwords are the device logins to put in the store's secrets, keyed the way
// the rest of iontk keys them.
func (s *Set) Passwords() map[string]string {
	out := map[string]string{}
	for _, t := range s.Tenants {
		for _, u := range t.Users {
			out[config.IONPassKey(t.TSGID, u.Name)] = u.Password
		}
	}
	return out
}

func (s *Set) tenant(tsg string) *Tenant {
	for i := range s.Tenants {
		if s.Tenants[i].TSGID == tsg {
			return &s.Tenants[i]
		}
	}
	return nil
}

// element finds an element by ID in any tenant: a session is opened with an ID
// and nothing else.
func (s *Set) element(eid string) *Element {
	for i := range s.Tenants {
		for j := range s.Tenants[i].Elements {
			if s.Tenants[i].Elements[j].ID == eid {
				return &s.Tenants[i].Elements[j]
			}
		}
	}
	return nil
}

func (s *Set) apiElements(tsg string) []api.Element {
	t := s.tenant(tsg)
	if t == nil {
		return nil
	}
	out := make([]api.Element, 0, len(t.Elements))
	for _, el := range t.Elements {
		out = append(out, el.apiElement())
	}
	return out
}

func (e Element) apiElement() api.Element {
	state := "declared"
	if e.Connected {
		state = "bound"
	}
	return api.Element{
		ID:        e.ID,
		Name:      e.Name,
		HWID:      e.HWID,
		Model:     e.Model,
		Software:  e.Software,
		SiteID:    e.siteID(),
		Connected: e.Connected,
		State:     state,
	}
}

// siteID is derived from the site name, because the portal gives elements a
// site ID and names are only resolvable through the sites endpoint. An element
// with no site keeps the API's own "unassigned" marker.
func (e Element) siteID() string {
	if e.Site == "" {
		return ""
	}
	return syntheticID("site/"+e.tsg+"/"+e.Site, 19)
}

func (s *Set) apiSites(tsg string) []api.Site {
	t := s.tenant(tsg)
	if t == nil {
		return nil
	}
	var out []api.Site
	seen := map[string]bool{}
	for _, el := range t.Elements {
		id := el.siteID()
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, api.Site{ID: id, Name: el.Site})
	}
	return out
}

// login reports whether a user and password pair is one of the tenant's.
func (t *Tenant) login(user, pass string) bool {
	for _, u := range t.Users {
		if u.Name == user {
			return u.Password == pass
		}
	}
	return false
}
