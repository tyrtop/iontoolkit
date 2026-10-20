package fixture

import (
	"net/http"
	"strings"
	"testing"

	"tyrtop.com/iontk/internal/sase/api"
)

func fixtureClient(s *Set, tsg string) *api.Client {
	c := &http.Client{Transport: s.Transport(tsg)}
	return api.NewClient(c, c, "fixture", false, 100, 100)
}

func TestTheRealClientListsFixtureElements(t *testing.T) {
	s := load(t, demo)
	c := fixtureClient(s, s.Tenants[0].TSGID)

	els, err := c.ListElements(t.Context())
	if err != nil {
		t.Fatalf("ListElements() error = %v", err)
	}
	if len(els) != 2 {
		t.Fatalf("got %d elements, want 2: %+v", len(els), els)
	}
	if els[0].Name != "store-0003-rtr-01" || !els[0].Connected || els[0].Model != "ion 1200" {
		t.Errorf("first element = %+v", els[0])
	}

	sites, err := c.ListSites(t.Context())
	if err != nil {
		t.Fatalf("ListSites() error = %v", err)
	}
	if len(sites) != 2 {
		t.Errorf("got %d sites, want 2: %+v", len(sites), sites)
	}

	if _, err := c.Profile(t.Context()); err != nil {
		t.Errorf("Profile() error = %v", err)
	}
}

func TestLookupAcrossTenantsIsA404(t *testing.T) {
	s := load(t, demo)
	acme, lab := s.Tenants[0], s.Tenants[1]
	c := fixtureClient(s, acme.TSGID)

	if _, err := c.LookupElement(t.Context(), acme.Elements[0].ID); err != nil {
		t.Fatalf("LookupElement() on its own tenant error = %v", err)
	}

	_, err := c.LookupElement(t.Context(), lab.Elements[0].ID)
	if err == nil {
		t.Fatal("an element from another tenant resolved")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error = %q, want a 404", err)
	}
}

func TestAnUnservedEndpointSaysSoRatherThanHanging(t *testing.T) {
	s := load(t, demo)
	c := &http.Client{Transport: s.Transport(s.Tenants[0].TSGID)}

	resp, err := c.Get("https://api.sase.paloaltonetworks.com/sdwan/monitor/v2.7/api/monitor/metrics")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d for an endpoint fixture mode does not serve, want 404", resp.StatusCode)
	}
}
