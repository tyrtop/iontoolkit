package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// ElementStatus is the controller's view of an element: its back-channels and
// a handful of facts about the device itself. The API spells every channel out
// flat -- config_and_events_connected, analytics_live_connected_on_utc,
// logs_backlog_from and so on -- so they are grouped here by prefix instead,
// and a channel the server adds later needs no field.
type ElementStatus struct {
	ElementID string
	SiteID    string
	Channels  []Channel

	// DeviceMode is "l3-mode" on a router.
	DeviceMode string
	// ControllerInterface is the interface ID the controller session runs
	// over, which is not otherwise derivable from the interface list.
	ControllerInterface string
	LastRebooted        int64
	// LastRebootedInfo is free text: "Requested by API" on a reboot iontk's
	// own sort of tooling asked for.
	LastRebootedInfo string
	LastDisconnected int64
}

// Channel is one of those back-channels. On and Off are the API's "utc"
// integers, whose unit varies by field: see UTCTime.
type Channel struct {
	Name      string
	Connected bool
	On        int64
	Off       int64
	// From is the controller address the element is talking to.
	From string
}

// UTCTime converts one of the API's timestamp integers. The unit cannot be
// assumed from the field name, because one element-status body carries three
// of them: last_rebooted_time in seconds, analytics_live_connected_on_utc in
// milliseconds, and config_and_events_connected_on_utc and
// last_disconnected_time in 100ns ticks -- as do the _created_on_utc and
// _updated_on_utc fields on every record. So the magnitude decides: an epoch
// date is around 1.8e9 seconds, and each scale shifts that by three digits or
// seven. Zero means the field was never set.
func UTCTime(v int64) time.Time {
	switch {
	case v == 0:
		return time.Time{}
	case v < 1e11:
		return time.Unix(v, 0).UTC()
	case v < 1e14:
		return time.UnixMilli(v).UTC()
	case v < 1e16:
		// Not observed, unlike the other three. Guessing 100ns here would be a
		// date a century out, which is worse than guessing microseconds.
		return time.UnixMicro(v).UTC()
	default:
		return time.Unix(v/1e7, (v%1e7)*100).UTC()
	}
}

// Channel returns one channel by name, "config_and_events" being the one that
// decides whether the portal calls an element connected at all.
func (s ElementStatus) Channel(name string) (Channel, bool) {
	for _, c := range s.Channels {
		if c.Name == name {
			return c, true
		}
	}
	return Channel{}, false
}

func (s *Client) ElementStatus(ctx context.Context, eid string) (ElementStatus, error) {
	body, err := s.get(ctx, "element "+eid+" status", elementStatusURL(eid))
	if err != nil {
		return ElementStatus{}, err
	}
	return decodeElementStatus(body)
}

// Suffixes that make up one channel. The field name up to the suffix is the
// channel's name.
const (
	sufConnected    = "_connected"
	sufConnectedOn  = "_connected_on_utc"
	sufDisconnected = "_disconnected_on_utc"
	sufFrom         = "_from"
)

func decodeElementStatus(body []byte) (ElementStatus, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return ElementStatus{}, fmt.Errorf("element status: json decode: %w", err)
	}

	out := ElementStatus{}
	for field, into := range map[string]any{
		"element_id":                 &out.ElementID,
		"site_id":                    &out.SiteID,
		"device_mode":                &out.DeviceMode,
		"controller_connection_intf": &out.ControllerInterface,
		"last_rebooted_time":         &out.LastRebooted,
		"last_rebooted_info":         &out.LastRebootedInfo,
		"last_disconnected_time":     &out.LastDisconnected,
	} {
		// A field the server sends as null, or does not send at all, keeps the
		// zero value: this response has a dozen fields that are null on an
		// appliance without the hardware for them.
		_ = json.Unmarshal(raw[field], into)
	}

	// _connected_on_utc also ends in _connected's suffix, so the longer
	// suffixes are tested first or every timestamp names its own channel.
	chans := map[string]*Channel{}
	pick := func(name string) *Channel {
		c, ok := chans[name]
		if !ok {
			c = &Channel{Name: name}
			chans[name] = c
		}
		return c
	}
	for k, v := range raw {
		switch {
		case strings.HasSuffix(k, sufConnectedOn):
			_ = json.Unmarshal(v, &pick(strings.TrimSuffix(k, sufConnectedOn)).On)
		case strings.HasSuffix(k, sufDisconnected):
			_ = json.Unmarshal(v, &pick(strings.TrimSuffix(k, sufDisconnected)).Off)
		case strings.HasSuffix(k, sufConnected):
			_ = json.Unmarshal(v, &pick(strings.TrimSuffix(k, sufConnected)).Connected)
		case strings.HasSuffix(k, sufFrom):
			_ = json.Unmarshal(v, &pick(strings.TrimSuffix(k, sufFrom)).From)
		}
	}

	for _, c := range chans {
		out.Channels = append(out.Channels, *c)
	}
	sort.Slice(out.Channels, func(i, j int) bool { return out.Channels[i].Name < out.Channels[j].Name })
	return out, nil
}

// Interface is one configured interface. The live address and state are not
// here but in InterfaceStatus: this is what somebody configured, which on a
// down port is a different thing from what the port is doing.
type Interface struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Type        string   `json:"type"`
	UsedFor     string   `json:"used_for"`
	AdminUp     bool     `json:"admin_up"`
	MTU         int      `json:"mtu"`
	MACAddress  string   `json:"mac_address"`
	Parent      string   `json:"parent"`
	Scope       string   `json:"scope"`
	Tags        []string `json:"tags"`
	// WAN interfaces the port is bound to, which is how a port maps to a
	// circuit.
	SiteWANInterfaceIDs []string `json:"site_wan_interface_ids"`
}

// ListInterfaces needs the site as well as the element: interfaces hang off
// the site in the path, and an element with no site has none to list.
func (s *Client) ListInterfaces(ctx context.Context, siteID, eid string) ([]Interface, error) {
	if siteID == "" {
		return nil, fmt.Errorf("element %s: interfaces need a site id", eid)
	}
	body, err := s.get(ctx, "element "+eid+" interfaces", interfacesURL(siteID, eid))
	if err != nil {
		return nil, err
	}
	var wrapped struct {
		Items []Interface `json:"items"`
	}
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return nil, fmt.Errorf("element %s interfaces: json decode: %w", eid, err)
	}
	return wrapped.Items, nil
}

// InterfaceStatus is the live state of one interface. Its ID is the status
// record's own, which is not the interface's: pair a status with its interface
// by the request that fetched it, or by Name.
//
// The fields an ion 1200-s leaves null on every interface -- cellular_state,
// stp_state, svi_state, poe_state, lacp_state, port_channel_member_state,
// fec_state, effective_vlans, secondary_ipv4_addresses, link_local_address,
// routes_v6 -- are left out rather than modelled from a shape nothing has
// seen filled in.
type InterfaceStatus struct {
	ID               string `json:"id"`
	ElementID        string `json:"element_id"`
	Name             string `json:"name"`
	OperationalState string `json:"operational_state"`
	// ExtendedState says why, and only a service link sets it: "tunnel_up",
	// "lowerlayer_down". A port that is simply up leaves it null.
	ExtendedState  string   `json:"extended_state"`
	Device         string   `json:"device"`
	MACAddress     string   `json:"mac_address"`
	NegotiatedMTU  int      `json:"negotiated_mtu"`
	IPv4Addresses  []string `json:"ipv4_addresses"`
	IPv6Addresses  []string `json:"ipv6_addresses"`
	RemoteV4Addr   string   `json:"remote_v4_addr"`
	RemoteHostName string   `json:"remote_host_name"`
	// LastStateChange is milliseconds, where the element status's own
	// timestamps are not. UTCTime works it out from the magnitude.
	LastStateChange int64 `json:"last_state_change"`
	// Port is nil on anything that is not a physical port -- a subinterface,
	// a service link, a bypass pair.
	Port        *PortState        `json:"port"`
	VRF         *VRF              `json:"vrf"`
	Routes      []Route           `json:"routes"`
	ServiceLink *ServiceLinkState `json:"service_link"`
	LLDPEnabled bool              `json:"lldp_enabled"`
}

// PortState is the physical link. Speed is Mbps, and is 0 on a port that is
// down -- negotiated_mtu is 0 even on one that is up, so this is where the
// link detail actually lives.
type PortState struct {
	Connected  bool `json:"connected"`
	Speed      int  `json:"speed"`
	FullDuplex bool `json:"full_duplex"`
}

type VRF struct {
	ContextID string `json:"vrf_context_id"`
	Name      string `json:"vrf_context_name"`
}

type Route struct {
	Destination string `json:"destination"`
	Via         string `json:"via"`
}

// ServiceLinkState is a Prisma Access tunnel's crypto and rekey schedule. The
// rekey timestamps are milliseconds.
type ServiceLinkState struct {
	IPsecAlgo         string `json:"ipsec_algo"`
	IKEAlgo           string `json:"ike_algo"`
	LocalTunnelV4Addr string `json:"local_tunnel_v4_addr"`
	IKELastRekeyed    int64  `json:"ike_last_rekeyed"`
	IKENextRekey      int64  `json:"ike_next_rekey"`
	IPsecLastRekeyed  int64  `json:"ipsec_last_rekeyed"`
	IPsecNextRekey    int64  `json:"ipsec_next_rekey"`
}

// InterfaceStatus returns a slice because a bypass pair answers with one
// status per member, wrapped in the usual items/count envelope, where every
// other interface type answers with a bare object.
func (s *Client) InterfaceStatus(ctx context.Context, siteID, eid, iid string) ([]InterfaceStatus, error) {
	body, err := s.get(ctx, "interface "+iid+" status", interfaceStatusURL(siteID, eid, iid))
	if err != nil {
		return nil, err
	}
	return decodeInterfaceStatus(iid, body)
}

func decodeInterfaceStatus(iid string, body []byte) ([]InterfaceStatus, error) {
	var wrapped struct {
		Items []InterfaceStatus `json:"items"`
	}
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return nil, fmt.Errorf("interface %s status: json decode: %w", iid, err)
	}
	if wrapped.Items != nil {
		return wrapped.Items, nil
	}
	var st InterfaceStatus
	if err := json.Unmarshal(body, &st); err != nil {
		return nil, fmt.Errorf("interface %s status: json decode: %w", iid, err)
	}
	return []InterfaceStatus{st}, nil
}

// SpokeClusterStatus is HA state: who is in the cluster, and every switchover
// with the reason behind it. The element's own spoke_ha_config says only that
// it belongs to a cluster and at what priority; which member is carrying
// traffic is here.
type SpokeClusterStatus struct {
	Members []ClusterMember `json:"cluster_members"`
	Events  []SwitchOver    `json:"switch_over_events"`
}

// ClusterMember is one element in the cluster. Status is "active" on the one
// carrying traffic and "unknown" for a member the controller cannot see --
// which is what a dead peer looks like, not a distinct "standby".
type ClusterMember struct {
	ElementID string `json:"element_id"`
	Status    string `json:"status"`
}

// SwitchOver is one member becoming active or giving it up, newest first.
// Time is milliseconds.
type SwitchOver struct {
	ElementID         string `json:"element_id"`
	Active            bool   `json:"active"`
	Time              int64  `json:"switch_over_time"`
	EffectivePriority int    `json:"effective_priority"`
	PeerConnected     bool   `json:"peer_connected"`
	// Adjustments is why EffectivePriority is not the configured priority.
	Adjustments []PriorityAdjustment `json:"adjustments"`
	// ClusterEvents is the priority's history since this switchover, newest
	// first: the same adjustments arriving and clearing without the active
	// member changing hands.
	ClusterEvents []ClusterEvent `json:"cluster_events"`
}

// PriorityAdjustment is what took points off the configured priority. Seen on
// real hardware: kind "process" with the process name ("radvd", "adem", 25
// points), "interface" with the interface name ("3", 150), "force" for the
// slow start after a boot, and "system" while initialising.
type PriorityAdjustment struct {
	Adjustment int    `json:"adjustment"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
}

type ClusterEvent struct {
	Time              int64                `json:"time"`
	EffectivePriority int                  `json:"effective_priority"`
	PeerConnected     bool                 `json:"peer_connected"`
	Adjustments       []PriorityAdjustment `json:"adjustments"`
}

// Active is the member carrying traffic, if the cluster says.
func (s SpokeClusterStatus) Active() (ClusterMember, bool) {
	for _, m := range s.Members {
		if m.Status == "active" {
			return m, true
		}
	}
	return ClusterMember{}, false
}

func (s *Client) SpokeClusterStatus(ctx context.Context, siteID, cid string) (SpokeClusterStatus, error) {
	body, err := s.get(ctx, "spoke cluster "+cid+" status", clusterStatusURL(siteID, cid))
	if err != nil {
		return SpokeClusterStatus{}, err
	}
	var st SpokeClusterStatus
	if err := json.Unmarshal(body, &st); err != nil {
		return SpokeClusterStatus{}, fmt.Errorf("spoke cluster %s status: json decode: %w", cid, err)
	}
	return st, nil
}

// GetJSON is a GET of one SASE path, for a caller whose paths come from
// configuration rather than from this package: rich-data mode's views. The
// host is fixed here, and the path has to be a /sdwan read -- a view is
// user-editable data, and this token can write to production appliances.
func (s *Client) GetJSON(ctx context.Context, path string) ([]byte, error) {
	if !strings.HasPrefix(path, "/sdwan/") {
		return nil, fmt.Errorf("path %q is not a /sdwan read", path)
	}
	if strings.Contains(path, "://") || strings.HasPrefix(path, "//") {
		return nil, fmt.Errorf("path %q names a host of its own", path)
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == ".." {
			return nil, fmt.Errorf("path %q walks up out of /sdwan", path)
		}
	}
	return s.get(ctx, strings.TrimPrefix(path, "/"), apiHost+path)
}

// get is one rate-limited authenticated GET returning the whole body. The
// callers here keep the raw bytes: the response shapes of the status
// endpoints are modelled from the portal's own field names rather than from a
// published schema, so a snapshot can show what was actually sent.
func (s *Client) get(ctx context.Context, what, url string) ([]byte, error) {
	if err := s.limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("%s: rate limit wait: %w", what, err)
	}

	if s.verbose {
		fmt.Fprintln(os.Stderr, "GET", url)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: build request: %w", what, err)
	}
	req.Header.Set("Authorization", "Bearer "+s.getToken())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", browserUA)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: response: %w", what, err)
	}

	// The rate limit is the thing a fleet-wide run hits first, and the headers
	// are the only warning before a 429.
	if s.verbose {
		for k, v := range resp.Header {
			if strings.HasPrefix(strings.ToLower(k), "x-ratelimit") || strings.EqualFold(k, "retry-after") {
				fmt.Fprintln(os.Stderr, k+":", v)
			}
		}
	}

	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s: read body: %w", what, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: status %s: %s", what, resp.Status, body)
	}
	return body, nil
}
