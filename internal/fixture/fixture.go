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

type Set struct {
	Tenants         []Tenant `yaml:"tenants"`
	UnknownResponse string   `yaml:"unknown_response"`
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
	Name      string            `yaml:"name"`
	ID        string            `yaml:"id"`
	HWID      string            `yaml:"hw_id"`
	Model     string            `yaml:"model"`
	Software  string            `yaml:"software"`
	Site      string            `yaml:"site"`
	Connected bool              `yaml:"connected"`
	HA        bool              `yaml:"ha"`
	Pages     bool              `yaml:"pages"`
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

	pagerPrompt = "--More--(4%)[Press space to continue, 'q' to quit.]"
)

func Load(path string) (*Set, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read fixtures: %w", err)
	}
	var s Set
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
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

func syntheticID(seed string, digits int) string {
	sum := sha256.Sum256([]byte(seed))
	var b strings.Builder
	for i := 0; b.Len() < digits; i += 8 {
		fmt.Fprintf(&b, "%019d", binary.BigEndian.Uint64(sum[i%24:])%1e19)
	}
	out := b.String()[:digits]
	if out[0] == '0' {
		out = "1" + out[1:]
	}
	return out
}

func (s *Set) SavedTenants() []config.Tenant {
	out := make([]config.Tenant, 0, len(s.Tenants))
	for _, t := range s.Tenants {
		ct := config.Tenant{
			Name:     t.Name,
			TSGID:    t.TSGID,
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
	el := api.Element{
		ID:           e.ID,
		Name:         e.Name,
		HWID:         e.HWID,
		Model:        e.Model,
		Software:     e.Software,
		SiteID:       e.siteID(),
		Connected:    e.Connected,
		State:        state,
		Role:         "SPOKE",
		AllowedRoles: []string{"HUB", "SPOKE"},
		Serial:       "FAKE" + syntheticID("serial/"+e.ID, 10),
		FIPSMode:     "non_fips",
	}
	if e.HA {
		el.SpokeHA = &api.SpokeHA{
			ClusterID:       e.clusterID(),
			Enable:          true,
			Priority:        160,
			SourceInterface: e.interfaceID(3),
			Track: &api.HATrack{
				Interfaces: []api.HATrackItem{{InterfaceID: e.interfaceID(1), ReducePriority: 100}},
			},
		}
	}
	return el
}

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

func (t *Tenant) login(user, pass string) bool {
	for _, u := range t.Users {
		if u.Name == user {
			return u.Password == pass
		}
	}
	return false
}
