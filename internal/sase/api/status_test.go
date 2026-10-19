package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Trimmed from what an ion 1200-s actually answered on 2026-10-18, with the
// ids and addresses changed. The shape is the point: the channels are flat,
// config_and_events counts in 100ns ticks while the others count in
// milliseconds, and a dozen fields are null on an appliance without the
// hardware for them.
const elementStatusBody = `{
  "id": "1655828640836018896",
  "_etag": 50647,
  "_created_on_utc": 16558286408360188,
  "site_id": "16",
  "element_id": "15",
  "config_and_events_connected": true,
  "config_and_events_connected_on_utc": 17923299413050015,
  "config_and_events_disconnected_on_utc": 17923299316100154,
  "config_and_events_from": "203.0.113.9",
  "logs_backlog_connected": false,
  "logs_backlog_connected_on_utc": 1792307964995,
  "logs_backlog_disconnected_on_utc": 1792308036585,
  "logs_backlog_from": "198.51.100.24",
  "poe_state": null,
  "main_power_allocated": 0.0,
  "last_rebooted_time": 1784373284,
  "last_rebooted_info": "Requested by API",
  "last_disconnected_time": 17923299316100154,
  "device_mode": "l3-mode",
  "controller_connection_intf": "17",
  "application_sig_file_info": null,
  "hub_cluster_status": null
}`

func TestElementStatusGroupsTheFlatFieldsIntoChannels(t *testing.T) {
	st, err := decodeElementStatus([]byte(elementStatusBody))
	if err != nil {
		t.Fatalf("decodeElementStatus() error = %v", err)
	}
	if st.ElementID != "15" || st.SiteID != "16" {
		t.Errorf("element %q site %q, want 15 and 16", st.ElementID, st.SiteID)
	}
	if len(st.Channels) != 2 {
		t.Fatalf("got %d channels, want 2: %+v", len(st.Channels), st.Channels)
	}

	tests := []struct {
		name string
		want Channel
	}{
		{"config_and_events", Channel{
			Name: "config_and_events", Connected: true,
			On: 17923299413050015, Off: 17923299316100154, From: "203.0.113.9",
		}},
		// A backlog channel that is down still carries both times: it
		// connects, drains and disconnects, so "down" is normal for it.
		{"logs_backlog", Channel{
			Name: "logs_backlog",
			On:   1792307964995, Off: 1792308036585, From: "198.51.100.24",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := st.Channel(tt.name)
			if !ok {
				t.Fatalf("no channel %q in %+v", tt.name, st.Channels)
			}
			if got != tt.want {
				t.Errorf("channel = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// The device facts in the same body, which are the ones worth a hot stat:
// when it last rebooted and why, and which port the controller talks over.
func TestElementStatusKeepsTheDeviceFactsBesideTheChannels(t *testing.T) {
	st, err := decodeElementStatus([]byte(elementStatusBody))
	if err != nil {
		t.Fatalf("decodeElementStatus() error = %v", err)
	}
	if st.DeviceMode != "l3-mode" {
		t.Errorf("DeviceMode = %q", st.DeviceMode)
	}
	if st.ControllerInterface != "17" {
		t.Errorf("ControllerInterface = %q", st.ControllerInterface)
	}
	if st.LastRebooted != 1784373284 || st.LastRebootedInfo != "Requested by API" {
		t.Errorf("last reboot = %d %q", st.LastRebooted, st.LastRebootedInfo)
	}
	if st.LastDisconnected != 17923299316100154 {
		t.Errorf("LastDisconnected = %d", st.LastDisconnected)
	}
}

// A bypass pair answers with one status per member in the items envelope,
// where every other interface type answers with a bare object. Decoding only
// the bare form dropped both members.
func TestInterfaceStatusTakesEitherTheBareObjectOrTheEnvelope(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		want  int
		state string
	}{
		{
			name:  "bare object",
			body:  `{"id":"99","name":"3","operational_state":"up"}`,
			want:  1,
			state: "up",
		},
		{
			name: "bypass pair",
			body: `{"count":2,"items":[
				{"id":"99","name":"4","operational_state":"up"},
				{"id":"98","name":"5","operational_state":"up"}]}`,
			want:  2,
			state: "up",
		},
		{
			// Only a service link fills extended_state in, and it is the
			// difference between "down" and "down because the port under it
			// is".
			name:  "service link",
			body:  `{"id":"97","name":"tunnel","operational_state":"down","extended_state":"lowerlayer_down"}`,
			want:  1,
			state: "down",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeInterfaceStatus("17", []byte(tt.body))
			if err != nil {
				t.Fatalf("decodeInterfaceStatus() error = %v", err)
			}
			if len(got) != tt.want {
				t.Fatalf("got %d statuses, want %d: %+v", len(got), tt.want, got)
			}
			if got[0].OperationalState != tt.state {
				t.Errorf("operational_state = %q, want %q", got[0].OperationalState, tt.state)
			}
		})
	}
}

// One element-status body carries three scales at once, so these are every
// one of them, each from a record whose real date is known. A wrong unit
// comes out centuries adrift, which is why the dates are asserted rather than
// the arithmetic.
func TestUTCTimeWorksTheUnitOutFromTheMagnitude(t *testing.T) {
	tests := []struct {
		name string
		utc  int64
		want string
	}{
		{"seconds: last_rebooted_time", 1784373284, "2026-07-18T11:14:44Z"},
		{"milliseconds: analytics_live_connected_on_utc", 1792307867657, "2026-10-18T07:17:47Z"},
		{"milliseconds: last_state_change", 1784373427865, "2026-07-18T11:17:07Z"},
		{"100ns: config_and_events_connected_on_utc", 17923299413050015, "2026-10-18T13:25:41Z"},
		{"100ns: tenant created", 16059213383510196, "2020-11-21T01:15:38Z"},
		{"100ns: site updated", 16919103346290108, "2023-08-13T07:05:34Z"},
		{"never set", 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := UTCTime(tt.utc)
			if tt.want == "" {
				if !got.IsZero() {
					t.Errorf("UTCTime(0) = %s, want the zero time", got)
				}
				return
			}
			if s := got.Format(time.RFC3339); s != tt.want {
				t.Errorf("UTCTime(%d) = %s, want %s", tt.utc, s, tt.want)
			}
		})
	}
}

func TestPortalURLsCarryTheVersionEachResourceAnswersOn(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"element status", elementStatusURL("15"),
			"https://api.sase.paloaltonetworks.com/sdwan/v2.6/api/elements/15/status"},
		{"interfaces", interfacesURL("16", "15"),
			"https://api.sase.paloaltonetworks.com/sdwan/v4.21/api/sites/16/elements/15/interfaces"},
		{"interface status", interfaceStatusURL("16", "15", "17"),
			"https://api.sase.paloaltonetworks.com/sdwan/v3.9/api/sites/16/elements/15/interfaces/17/status"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("url = %q, want %q", tt.got, tt.want)
			}
		})
	}
}

// routes answers by path suffix, standing in for the SASE endpoints so a
// snapshot can be taken without a tenant. The longest matching suffix wins,
// since several of these paths end in /status.
type routes map[string]func() (int, string)

func (r routes) RoundTrip(req *http.Request) (*http.Response, error) {
	code, body := http.StatusNotFound, `{"error":"no route"}`
	best := ""
	for suffix := range r {
		if strings.HasSuffix(req.URL.Path, suffix) && len(suffix) > len(best) {
			best = suffix
		}
	}
	if best != "" {
		code, body = r[best]()
	}
	return &http.Response{
		StatusCode: code,
		Status:     http.StatusText(code),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(body))),
		Request:    req,
	}, nil
}

func snapshotClient(t *testing.T, r routes) *Client {
	t.Helper()
	c := &http.Client{Transport: r}
	return NewClient(c, c, "tok", false, 100, 100)
}

// A status endpoint that refuses must not cost the interfaces: an element
// whose portal half is half-answered is still worth a page.
func TestSnapshotKeepsTheHalfThatAnswered(t *testing.T) {
	c := snapshotClient(t, routes{
		"/elements/15/status": func() (int, string) {
			return http.StatusForbidden, `{"error":"no permission"}`
		},
		"/interfaces": func() (int, string) {
			return http.StatusOK, `{"items":[{"id":"17","name":"1","used_for":"public","admin_up":true}]}`
		},
		// port is an object, not a string, and the status record's own id is
		// not the interface's. Both cost a decode when they were guessed.
		"/interfaces/17/status": func() (int, string) {
			return http.StatusOK, `{"id":"99","name":"1","operational_state":"up",
				"extended_state":null,"negotiated_mtu":0,"device":"eth0",
				"ipv4_addresses":["192.0.2.1/24"],
				"port":{"connected":true,"speed":1000,"full_duplex":true},
				"routes":[{"via":"192.0.2.254","destination":"0.0.0.0/0"}],
				"vrf":{"vrf_context_id":"18","vrf_context_name":"Global"}}`
		},
	})

	snap := c.ElementSnapshot(t.Context(), Element{ID: "15", SiteID: "16"})

	if snap.StatusErr == nil {
		t.Error("StatusErr = nil, want the 403 reported")
	}
	if len(snap.Interfaces) != 1 {
		t.Fatalf("got %d interfaces, want 1: %+v", len(snap.Interfaces), snap.Interfaces)
	}
	in := snap.Interfaces[0]
	if in.StatusErr != nil {
		t.Errorf("interface StatusErr = %v", in.StatusErr)
	}
	if len(in.Status) != 1 {
		t.Fatalf("got %d statuses for a port, want 1: %+v", len(in.Status), in.Status)
	}
	st := in.Status[0]
	if in.Interface.UsedFor != "public" || st.OperationalState != "up" {
		t.Errorf("interface = %+v, status = %+v", in.Interface, st)
	}
	if got := len(st.IPv4Addresses); got != 1 {
		t.Errorf("%d addresses, want 1", got)
	}
	// The link detail is in port, since negotiated_mtu is 0 even when up.
	if st.Port == nil || !st.Port.Connected || st.Port.Speed != 1000 {
		t.Errorf("port = %+v, want the link up at 1000", st.Port)
	}
	if st.VRF == nil || st.VRF.Name != "Global" {
		t.Errorf("vrf = %+v", st.VRF)
	}
	if len(st.Routes) != 1 || st.Routes[0].Destination != "0.0.0.0/0" {
		t.Errorf("routes = %+v", st.Routes)
	}
	// The bodies that did arrive are kept, so a field that never decoded can
	// be told from one the server left empty.
	if len(snap.Raw) != 2 {
		t.Errorf("%d raw bodies, want the 2 that answered: %v", len(snap.Raw), snap.Raw)
	}
	if snap.Err() == nil {
		t.Error("Err() = nil, want the status failure joined into it")
	}
}

// Trimmed from what the clustered ion 2000 answered on 2026-10-18. Every
// field is real, and the interesting part is why a member's effective
// priority is not its configured one.
const clusterStatusBody = `{
  "id": null,
  "cluster_members": [
    {"element_id": "15", "status": "active"},
    {"element_id": "16", "status": "unknown"}
  ],
  "switch_over_events": [
    {
      "element_id": "15",
      "active": true,
      "switch_over_time": 1791297999614,
      "effective_priority": 160,
      "peer_connected": false,
      "cluster_events": [
        {"time": 1791940759138, "effective_priority": 160, "peer_connected": false},
        {"time": 1791940759083, "effective_priority": 135, "peer_connected": false,
         "adjustments": [{"adjustment": 25, "kind": "process", "name": "radvd"}]}
      ]
    },
    {
      "element_id": "15",
      "active": false,
      "switch_over_time": 1791297992828,
      "effective_priority": 1,
      "peer_connected": false,
      "adjustments": [{"adjustment": 159, "kind": "force", "name": "slow start"}],
      "cluster_events": []
    }
  ]
}`

func TestSpokeClusterStatusCarriesWhoIsActiveAndWhy(t *testing.T) {
	var st SpokeClusterStatus
	if err := json.Unmarshal([]byte(clusterStatusBody), &st); err != nil {
		t.Fatalf("unmarshal cluster status: %v", err)
	}

	// "unknown" is what a peer the controller cannot see looks like; there is
	// no separate "standby".
	if len(st.Members) != 2 || st.Members[1].Status != "unknown" {
		t.Fatalf("members = %+v", st.Members)
	}
	active, ok := st.Active()
	if !ok || active.ElementID != "15" {
		t.Errorf("Active() = %+v, %v", active, ok)
	}

	if len(st.Events) != 2 {
		t.Fatalf("got %d switchovers, want 2", len(st.Events))
	}

	// The slow start after a boot is a forced adjustment on the event itself.
	boot := st.Events[1]
	if len(boot.Adjustments) != 1 || boot.Adjustments[0].Kind != "force" || boot.Adjustments[0].Adjustment != 159 {
		t.Errorf("boot adjustments = %+v", boot.Adjustments)
	}
	if got := UTCTime(boot.Time).Format("2006-01-02"); got != "2026-10-06" {
		t.Errorf("switch_over_time reads as %s, want 2026-10-06 — milliseconds", got)
	}

	// A process dying takes points off without changing who is active, and
	// that only shows up in the nested history.
	now := st.Events[0]
	if len(now.ClusterEvents) != 2 {
		t.Fatalf("got %d cluster events, want 2", len(now.ClusterEvents))
	}
	dip := now.ClusterEvents[1]
	if dip.EffectivePriority != 135 || len(dip.Adjustments) != 1 {
		t.Fatalf("dip = %+v", dip)
	}
	if a := dip.Adjustments[0]; a.Kind != "process" || a.Name != "radvd" || a.Adjustment != 25 {
		t.Errorf("adjustment = %+v, want radvd costing 25", a)
	}
}

// The cluster is only fetched for an element whose record says it is in one,
// and the cluster id comes from spoke_ha_config rather than the record's own
// cluster_id, which a real clustered spoke left null.
func TestSnapshotFetchesTheClusterOnlyForAClusteredElement(t *testing.T) {
	const body = `{"cluster_members":[{"element_id":"15","status":"active"}],
		"switch_over_events":[{"element_id":"15","active":true,
			"switch_over_time":1791297999614,"effective_priority":160}]}`

	tests := []struct {
		name     string
		element  Element
		wantCall bool
	}{
		{
			name: "clustered",
			element: Element{ID: "15", SiteID: "16", SpokeHA: &SpokeHA{
				ClusterID: "20", Enable: true, Priority: 160,
			}},
			wantCall: true,
		},
		{
			name:    "not clustered",
			element: Element{ID: "15", SiteID: "16"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			asked := false
			c := snapshotClient(t, routes{
				"/status": func() (int, string) { return http.StatusOK, `{"element_id":"15"}` },
				"/interfaces": func() (int, string) {
					return http.StatusOK, `{"items":[]}`
				},
				"/spokeclusters/20/status": func() (int, string) {
					asked = true
					return http.StatusOK, body
				},
			})

			snap := c.ElementSnapshot(t.Context(), tt.element)

			if asked != tt.wantCall {
				t.Fatalf("cluster status asked = %v, want %v", asked, tt.wantCall)
			}
			if !tt.wantCall {
				if snap.Cluster != nil || snap.ClusterErr != nil {
					t.Errorf("cluster = %+v, err = %v, want neither", snap.Cluster, snap.ClusterErr)
				}
				return
			}
			if snap.ClusterErr != nil {
				t.Fatalf("ClusterErr = %v", snap.ClusterErr)
			}
			if snap.Cluster == nil || len(snap.Cluster.Members) != 1 || len(snap.Cluster.Events) != 1 {
				t.Fatalf("cluster = %+v, want one member and one event", snap.Cluster)
			}
			if m, ok := snap.Cluster.Active(); !ok || m.ElementID != "15" {
				t.Errorf("Active() = %+v, %v", m, ok)
			}
		})
	}
}

// Interfaces are site-scoped in the path, so an element with no site cannot
// have them fetched. Saying so beats a request to .../sites//elements/15.
func TestSnapshotWithNoSiteDoesNotAskForInterfaces(t *testing.T) {
	asked := false
	c := snapshotClient(t, routes{
		"/status": func() (int, string) { return http.StatusOK, `{"element_id":"15"}` },
		"/interfaces": func() (int, string) {
			asked = true
			return http.StatusOK, `{"items":[]}`
		},
	})

	snap := c.ElementSnapshot(t.Context(), Element{ID: "15"})

	if asked {
		t.Error("the interfaces endpoint was called for an element with no site")
	}
	if snap.IfacesErr == nil || !strings.Contains(snap.IfacesErr.Error(), "site id") {
		t.Errorf("IfacesErr = %v, want it to name the missing site id", snap.IfacesErr)
	}
	if snap.StatusErr != nil {
		t.Errorf("StatusErr = %v, want the status still fetched", snap.StatusErr)
	}
}
