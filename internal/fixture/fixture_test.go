package fixture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const demo = `
tenants:
  - name: Acme Co
    tsg_id: "000000000"
    users:
      - name: admin
        password: ion
    elements:
      - name: store-0003-rtr-01
        model: ion 1200
        software: 6.5.1
        site: Store 0003
        connected: true
        responses:
          "dump device status": |
            Serial     : FAKE0001
            Uptime     : 4 days
      - name: store-0100-rtr-01
        site: Store 0100
  - name: Lab
    elements:
      - name: lab-rtr-01
        connected: true
`

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixtures.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func load(t *testing.T, body string) *Set {
	t.Helper()
	s, err := Load(write(t, body))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return s
}

func TestLoadFillsInWhatTheFileLeavesOut(t *testing.T) {
	s := load(t, demo)

	lab := s.tenant(s.Tenants[1].TSGID)
	if lab == nil {
		t.Fatal("the tenant with no tsg_id got no generated one")
	}
	if len(lab.Users) != 1 || lab.Users[0].Name != defaultUser || lab.Users[0].Password != defaultPassword {
		t.Errorf("Lab users = %+v, want one default login so a fixture needs no credentials", lab.Users)
	}

	el := s.Tenants[0].Elements[1]
	if len(el.ID) != 19 {
		// A shorter ID would not exercise the filter's deliberate refusal to
		// substring-match 19-digit identifiers.
		t.Errorf("generated element id %q is %d digits, want 19", el.ID, len(el.ID))
	}
	if el.Model != defaultModel || el.Software != defaultSoftware || el.PageLines != defaultPageLines {
		t.Errorf("element defaults not applied: %+v", el)
	}
	if s.UnknownResponse != defaultUnknown {
		t.Errorf("UnknownResponse = %q, want the default", s.UnknownResponse)
	}
}

func TestGeneratedIDsAreStableAcrossLoads(t *testing.T) {
	// A fixture whose IDs moved every run would invalidate the element cache and
	// break any --element someone wrote down.
	first, second := load(t, demo), load(t, demo)
	for i := range first.Tenants {
		for j := range first.Tenants[i].Elements {
			a, b := first.Tenants[i].Elements[j], second.Tenants[i].Elements[j]
			if a.ID != b.ID {
				t.Errorf("%s got id %s then %s", a.Name, a.ID, b.ID)
			}
		}
	}
}

func TestLoadRejectsFixturesThatWouldLookLikeABug(t *testing.T) {
	cases := map[string]struct{ body, want string }{
		"no tenants":     {"tenants: []", "no tenants"},
		"unnamed tenant": {"tenants:\n  - tsg_id: \"1\"\n", "no name"},
		"unnamed element": {
			"tenants:\n  - name: T\n    elements:\n      - model: ion 1200\n", "no name"},
		"duplicate element id": {
			"tenants:\n  - name: T\n    elements:\n      - name: a\n        id: \"7\"\n      - name: b\n        id: \"7\"\n",
			"used by both"},
		// A typo in a key is the whole failure mode of a hand-written fixture.
		"misspelled key": {
			"tenants:\n  - name: T\n    elementz:\n      - name: a\n", "field elementz"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(write(t, tc.body))
			if err == nil {
				t.Fatalf("Load() accepted %q", tc.body)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestSavedTenantsAreUsableWithoutASignIn(t *testing.T) {
	s := load(t, demo)
	saved := s.SavedTenants()
	if len(saved) != 2 {
		t.Fatalf("got %d tenants, want 2", len(saved))
	}
	for _, t2 := range saved {
		if !t2.Added {
			t.Errorf("%s is not in the working set, so its elements would not list", t2.Name)
		}
		if t2.UsesBrowser() {
			t.Errorf("%s wants a browser, which a fixture run cannot open", t2.Name)
		}
		if t2.DefaultUser() == "" {
			t.Errorf("%s has no default device login", t2.Name)
		}
	}

	pw := s.Passwords()
	if len(pw) != 2 {
		t.Errorf("got %d device passwords, want one per tenant: %v", len(pw), pw)
	}
}

func TestSitesResolveFromElementSiteNames(t *testing.T) {
	s := load(t, demo)
	tsg := s.Tenants[0].TSGID

	sites := s.apiSites(tsg)
	if len(sites) != 2 {
		t.Fatalf("got %d sites, want one per distinct name: %+v", len(sites), sites)
	}
	byID := map[string]string{}
	for _, site := range sites {
		byID[site.ID] = site.Name
	}
	for _, el := range s.apiElements(tsg) {
		if byID[el.SiteID] == "" {
			t.Errorf("%s has site id %q, which no site answers — the list would show a raw id", el.Name, el.SiteID)
		}
	}
}
