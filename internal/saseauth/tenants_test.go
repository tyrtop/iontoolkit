package saseauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// as the portal actually returns it, nested children and all
const tenancyBody = `{"items":[
  {"id":"1643707006","display_name":"CIS root","vertical":"Telecommunications","children":[
     {"id":"1750385018","display_name":"Reseller","children":[
        {"id":"1984021017","display_name":"Tractor Supply Co. 1984021017","parent_id":"1750385018"}
     ]}
  ]},
  {"id":"1538107912","display_name":"NIAGARA BOTTLING, LLC-145595"}
],"count":2}`

func tenancyServer(t *testing.T, status int, body string) (*httptest.Server, *string) {
	t.Helper()
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if got := r.URL.Query().Get("hierarchy"); got != "true" {
			t.Errorf("hierarchy = %q, want true so children come back", got)
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &auth
}

func TestListTenantsFlattensTheHierarchy(t *testing.T) {
	srv, auth := tenancyServer(t, http.StatusOK, tenancyBody)

	got, err := ListTenants(context.Background(), srv.Client(), srv.URL, "the-token")
	if err != nil {
		t.Fatalf("ListTenants() error = %v", err)
	}
	if *auth != "Bearer the-token" {
		t.Errorf("Authorization = %q, want the bearer token", *auth)
	}

	want := []struct{ name, id string }{
		{"CIS root", "1643707006"},
		{"NIAGARA BOTTLING, LLC-145595", "1538107912"},
		{"Reseller", "1750385018"},
		{"Tractor Supply Co. 1984021017", "1984021017"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d tenants, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Name != w.name || got[i].ID != w.id {
			t.Errorf("tenant %d = %q/%q, want %q/%q", i, got[i].Name, got[i].ID, w.name, w.id)
		}
	}

	byID := map[string]Tenant{}
	for _, tn := range got {
		byID[tn.ID] = tn
	}
	if p := byID["1984021017"].ParentID; p != "1750385018" {
		t.Errorf("ParentID = %q, want the nesting preserved", p)
	}
	if d := byID["1984021017"].Depth; d != 2 {
		t.Errorf("Depth = %d, want 2 for a grandchild", d)
	}
	if d := byID["CIS root"].Depth; d != 0 {
		t.Errorf("root Depth = %d, want 0", d)
	}
}

func TestListTenantsEdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr string
		wantLen int
	}{
		{
			name:    "a tenant with no display name falls back to its id",
			status:  http.StatusOK,
			body:    `{"items":[{"id":"123","display_name":"  "}]}`,
			wantLen: 1,
		},
		{
			name:    "a repeated id is listed once",
			status:  http.StatusOK,
			body:    `{"items":[{"id":"1","display_name":"A","children":[{"id":"1","display_name":"A"}]},{"id":"1","display_name":"A"}]}`,
			wantLen: 1,
		},
		{"an entry with no id is skipped", http.StatusOK, `{"items":[{"display_name":"ghost"},{"id":"2","display_name":"B"}]}`, "", 1},
		{"no tenants at all", http.StatusOK, `{"items":[]}`, "access to none", 0},
		{"rejected", http.StatusForbidden, `{"message":"nope"}`, "status 403", 0},
		{"unauthorized", http.StatusUnauthorized, ``, "status 401", 0},
		{"malformed", http.StatusOK, `{`, "json decode", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := tenancyServer(t, tt.status, tt.body)
			got, err := ListTenants(context.Background(), srv.Client(), srv.URL, "t")
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("ListTenants() error = nil, want one containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ListTenants() error = %v", err)
			}
			if len(got) != tt.wantLen {
				t.Errorf("got %d tenants, want %d: %+v", len(got), tt.wantLen, got)
			}
			if tt.name == "a tenant with no display name falls back to its id" && got[0].Name != "123" {
				t.Errorf("Name = %q, want the id used as a label", got[0].Name)
			}
		})
	}
}

func TestListTenantsDefaultsToTheRealEndpoint(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, (Options{}).url("tenant_service_groups"), nil)
	if err != nil {
		t.Fatalf("the default endpoint is not a usable URL: %v", err)
	}
	if req.URL.Host != "api.apps.paloaltonetworks.com" {
		t.Errorf("host = %q, want the tenancy service", req.URL.Host)
	}
}
