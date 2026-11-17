package endpoint

import (
	"strings"
	"testing"
)

func TestEveryMasterEndpointIsAnSDWANCall(t *testing.T) {
	all := Master().All()
	if len(all) < 1000 {
		t.Fatalf("the master list has %d endpoints, want the SDK's thousand or more", len(all))
	}
	for _, e := range all {
		if err := e.SDWAN(); err != nil {
			t.Errorf("%v", err)
		}
		if e.Method == "WS" {
			t.Errorf("%s dials a session; the master list only holds calls", e.Name)
		}
	}
}

func TestASetAnswersFromTheMasterListWhatItDoesNotDeclare(t *testing.T) {
	s, err := Parse("sample", []byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if s.Declares("sites") {
		t.Fatal("sample declares sites; the test needs a name it leaves out")
	}
	got, err := s.URL("sites.one", map[string]string{"site_id": "7"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://api.example.com/sdwan/v4.13/api/sites/7"; got != want {
		t.Errorf("sites.one = %s, want %s", got, want)
	}
	if _, err := s.Get("no_such_call"); err == nil || !strings.Contains(err.Error(), "sample has no endpoint no_such_call") {
		t.Errorf("a name neither list has: %v", err)
	}
}

func TestASetsOwnEndpointWinsOverTheMasterList(t *testing.T) {
	s, err := Parse("mine", []byte("endpoints:\n  - name: sites\n    path: /sdwan/v4.5/api/sites\n"))
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.Get("sites")
	if err != nil {
		t.Fatal(err)
	}
	if e.Path != "/sdwan/v4.5/api/sites" {
		t.Errorf("sites = %s, want the set's own v4.5", e.Path)
	}
}
