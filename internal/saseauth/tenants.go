package saseauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

const TenancyURL = "https://api.apps.paloaltonetworks.com/tenancy/v1/tenant_service_groups"

// Tenant is a tenant service group the signed-in account can reach.
type Tenant struct {
	ID       string
	Name     string
	ParentID string
	// Depth is how far down the hierarchy this sits, for indenting a list.
	Depth int
}

// tsgNode mirrors the tenancy response, which nests children when asked for a
// hierarchy.
type tsgNode struct {
	ID          string    `json:"id"`
	DisplayName string    `json:"display_name"`
	ParentID    string    `json:"parent_id"`
	Children    []tsgNode `json:"children"`
}

func flatten(nodes []tsgNode, parent string, depth int, out *[]Tenant, seen map[string]bool) {
	for _, n := range nodes {
		if n.ID == "" || seen[n.ID] {
			continue
		}
		seen[n.ID] = true
		name := strings.TrimSpace(n.DisplayName)
		if name == "" {
			name = n.ID
		}
		p := n.ParentID
		if p == "" {
			p = parent
		}
		*out = append(*out, Tenant{ID: n.ID, Name: name, ParentID: p, Depth: depth})
		flatten(n.Children, n.ID, depth+1, out, seen)
	}
}

// ListTenants asks which tenants the token can act for. A tenant-less token is
// enough: this is how the tenant to scope to is chosen in the first place.
func ListTenants(ctx context.Context, client *http.Client, baseURL, accessToken string) ([]Tenant, error) {
	if baseURL == "" {
		baseURL = TenancyURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"?hierarchy=true", nil)
	if err != nil {
		return nil, fmt.Errorf("build tenant request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tenants: %w", err)
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("tenants: status %s: %s", resp.Status, body)
	}

	var out struct {
		Items []tsgNode `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("tenants: json decode: %w", err)
	}

	var tenants []Tenant
	flatten(out.Items, "", 0, &tenants, map[string]bool{})
	if len(tenants) == 0 {
		return nil, fmt.Errorf("tenants: the account has access to none")
	}
	// Hierarchy order is what the portal shows, but a flat alphabetical list is
	// easier to pick from once it is more than a handful.
	sort.SliceStable(tenants, func(i, j int) bool {
		return strings.ToLower(tenants[i].Name) < strings.ToLower(tenants[j].Name)
	})
	return tenants, nil
}

// Discovery is the result of signing in without naming a tenant.
type Discovery struct {
	Identity Identity
	Tenants  []Tenant
	// RefreshToken is the tenant-less credential the sign-in produced. It is
	// the seed for every tenant: a refresh grant may narrow its scope to one
	// tenant, which works where a silent re-authorize does not.
	RefreshToken string
}

// Discover opens a browser, signs in with no tenant scope, and reports which
// tenants the account can use. Nothing has to be typed: the tenant ID and its
// name both come back from the portal.
//
// The session cookie this leaves in the browser profile is what lets every
// later tenant be scoped silently, with no second window.
func Discover(ctx context.Context, opt Options, tenancyURL string) (Discovery, error) {
	tok, err := loginToken(ctx, "", opt, false)
	if err != nil {
		return Discovery{}, err
	}
	tenants, err := ListTenants(ctx, opt.httpClient(), tenancyURL, tok.AccessToken)
	if err != nil {
		return Discovery{}, err
	}
	return Discovery{
		Identity:     identityFrom(tok),
		Tenants:      tenants,
		RefreshToken: tok.RefreshToken,
	}, nil
}
