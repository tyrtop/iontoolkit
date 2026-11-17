package fixture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"tyrtop.com/iontk/internal/config"
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

func TestATenantCanAskForAFleetInsteadOfListingIt(t *testing.T) {
	set, err := Parse([]byte("tenants:\n  - name: Globex\n    fleet: 100\n"), "fleet.yaml")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	els := set.Tenants[0].Elements
	if len(els) != 100 {
		t.Fatalf("%d elements, want the hundred asked for", len(els))
	}

	sites, ids, ha, off := map[string]bool{}, map[string]bool{}, 0, 0
	for _, el := range els {
		if el.Name == "" || el.ID == "" || el.Model == "" || el.Site == "" {
			t.Fatalf("element came out half built: %+v", el)
		}
		if ids[el.ID] {
			t.Errorf("two elements share id %s", el.ID)
		}
		ids[el.ID] = true
		sites[el.Site] = true
		if el.HA {
			ha++
		}
		if !el.Connected {
			off++
		}
	}
	if len(sites) != 50 {
		t.Errorf("%d sites, want two elements each", len(sites))
	}
	if ha == 0 || ha == len(els) {
		t.Errorf("%d of %d clustered, want some of each", ha, len(els))
	}
	if off == 0 {
		t.Error("every element is connected, so the offline colour never shows")
	}

	again, err := Parse([]byte("tenants:\n  - name: Globex\n    fleet: 100\n"), "fleet.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if again.Tenants[0].Elements[7].ID != els[7].ID {
		t.Error("the ids move between runs, so a cached list would never match")
	}
}

func TestSlowMakesADialAndACommandTakeTime(t *testing.T) {
	set, err := Parse([]byte("slow: 300ms\ntenants:\n  - name: Acme Co\n    elements:\n      - name: rtr-01\n"), "slow.yaml")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	el := &set.Tenants[0].Elements[0]
	if got := el.wait(1); got < slowFloor {
		t.Errorf("a dial waits %s, want at least the floor", got)
	}
	if got := el.wait(3); got >= el.wait(1) {
		t.Errorf("a command waits %s and a dial %s, want the command quicker", got, el.wait(1))
	}

	fast, err := Parse([]byte("tenants:\n  - name: Acme Co\n    elements:\n      - name: rtr-01\n"), "fast.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got := fast.Tenants[0].Elements[0].wait(1); got != 0 {
		t.Errorf("a fleet that says nothing waits %s, want no wait at all", got)
	}

	if _, err := Parse([]byte("slow: soon\ntenants:\n  - name: A\n    elements:\n      - name: r\n"), "bad.yaml"); err == nil {
		t.Error("slow: soon was taken as a length of time")
	}
}

func TestEveryElementWaitsALittleDifferently(t *testing.T) {
	set, err := Parse([]byte("slow: 1s\ntenants:\n  - name: Globex\n    fleet: 20\n"), "spread.yaml")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[time.Duration]bool{}
	low, high := time.Hour, time.Duration(0)
	els := set.Tenants[0].Elements
	for i, el := range els {
		d := el.wait(1)
		seen[d] = true
		low, high = min(low, d), max(high, d)
		if i == 0 {
			continue
		}
		if gap := abs(d - els[i-1].wait(1)); gap < time.Second {
			t.Errorf("%s and %s wait %s apart, want a second between any two in a row",
				els[i-1].Name, el.Name, gap)
		}
	}
	if len(seen) < slowSteps {
		t.Errorf("%d different waits over 20 elements, want one per step", len(seen))
	}
	base := max(time.Second, slowJitter+slowFloor)
	if low < base-slowJitter || high > base+slowJitter {
		t.Errorf("waits run %s to %s, want %s give or take a second", low, high, base)
	}
}

func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func TestEveryFakeTenantIsItsOwnServiceAccount(t *testing.T) {
	set, err := Tutorial()
	if err != nil {
		t.Fatal(err)
	}
	saved := set.SavedTenants()
	if len(saved) != 2 {
		t.Fatalf("%d service accounts, want one per tenant", len(saved))
	}

	seen := map[string]bool{}
	for _, ct := range saved {
		if ct.Auth != config.AuthServiceAccount || !ct.Added {
			t.Errorf("%s is %+v, want an added service account", ct.Name, ct)
		}
		if seen[ct.ClientID] {
			t.Errorf("%s shares a client id with another account: %s", ct.Name, ct.ClientID)
		}
		seen[ct.ClientID] = true
		if !strings.Contains(ct.ClientID, ct.TSGID) {
			t.Errorf("%s has client id %q, want its own tsg in it", ct.Name, ct.ClientID)
		}
	}
	if got := saved[0].ClientID; got != "acme-co@999000999.iam.panserviceaccount.com" {
		t.Errorf("Acme Co's client id is %q", got)
	}
	if got := saved[1].ClientID; got != "globex@999000222.iam.panserviceaccount.com" {
		t.Errorf("Globex's client id is %q", got)
	}
}
