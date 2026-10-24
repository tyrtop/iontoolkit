package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

type ElementStatus struct {
	ElementID string
	SiteID    string
	Channels  []Channel

	DeviceMode          string
	ControllerInterface string
	LastRebooted        int64
	LastRebootedInfo    string
	LastDisconnected    int64
}

type Channel struct {
	Name      string
	Connected bool
	On        int64
	Off       int64
	From      string
}

// the unit cannot be assumed from the field name: one element-status body
// carries seconds (last_rebooted_time), milliseconds (analytics_live_...) and
// 100ns ticks (config_and_events_...), as do every record's _on_utc fields. So
// the magnitude decides, an epoch date being around 1.8e9 seconds. Zero is unset.
func UTCTime(v int64) time.Time {
	switch {
	case v == 0:
		return time.Time{}
	case v < 1e11:
		return time.Unix(v, 0).UTC()
	case v < 1e14:
		return time.UnixMilli(v).UTC()
	case v < 1e16:
		return time.UnixMicro(v).UTC()
	default:
		return time.Unix(v/1e7, (v%1e7)*100).UTC()
	}
}

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
		_ = json.Unmarshal(raw[field], into)
	}

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

type Interface struct {
	ID                  string      `json:"id"`
	Name                string      `json:"name"`
	Description         string      `json:"description"`
	Type                string      `json:"type"`
	UsedFor             string      `json:"used_for"`
	AdminUp             bool        `json:"admin_up"`
	MTU                 int         `json:"mtu"`
	MACAddress          string      `json:"mac_address"`
	Parent              string      `json:"parent"`
	Scope               string      `json:"scope"`
	Tags                []string    `json:"tags"`
	SiteWANInterfaceIDs []string    `json:"site_wan_interface_ids"`
	IPv4Config          *IPv4Config `json:"ipv4_config"`
}

// null on a port with no IPv4, and on the parent of a set of subinterfaces.
type IPv4Config struct {
	Type         string            `json:"type"`
	StaticConfig *IPv4StaticConfig `json:"static_config"`
}

type IPv4StaticConfig struct {
	Address string `json:"address"`
}

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

type InterfaceStatus struct {
	ID               string            `json:"id"`
	ElementID        string            `json:"element_id"`
	Name             string            `json:"name"`
	OperationalState string            `json:"operational_state"`
	ExtendedState    string            `json:"extended_state"`
	Device           string            `json:"device"`
	MACAddress       string            `json:"mac_address"`
	NegotiatedMTU    int               `json:"negotiated_mtu"`
	IPv4Addresses    []string          `json:"ipv4_addresses"`
	IPv6Addresses    []string          `json:"ipv6_addresses"`
	RemoteV4Addr     string            `json:"remote_v4_addr"`
	RemoteHostName   string            `json:"remote_host_name"`
	LastStateChange  int64             `json:"last_state_change"`
	Port             *PortState        `json:"port"`
	VRF              *VRF              `json:"vrf"`
	Routes           []Route           `json:"routes"`
	ServiceLink      *ServiceLinkState `json:"service_link"`
	LLDPEnabled      bool              `json:"lldp_enabled"`
}

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

type ServiceLinkState struct {
	IPsecAlgo         string `json:"ipsec_algo"`
	IKEAlgo           string `json:"ike_algo"`
	LocalTunnelV4Addr string `json:"local_tunnel_v4_addr"`
	IKELastRekeyed    int64  `json:"ike_last_rekeyed"`
	IKENextRekey      int64  `json:"ike_next_rekey"`
	IPsecLastRekeyed  int64  `json:"ipsec_last_rekeyed"`
	IPsecNextRekey    int64  `json:"ipsec_next_rekey"`
}

// a slice because a bypass pair answers with one status per member, wrapped in
// the usual items/count envelope where every other type sends a bare object
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

type SpokeClusterStatus struct {
	Members []ClusterMember `json:"cluster_members"`
	Events  []SwitchOver    `json:"switch_over_events"`
}

// Status is "active" on the one carrying traffic and "unknown" for a member the
// controller cannot see -- what a dead peer looks like, not a distinct "standby"
type ClusterMember struct {
	ElementID string `json:"element_id"`
	Status    string `json:"status"`
}

type SwitchOver struct {
	ElementID         string               `json:"element_id"`
	Active            bool                 `json:"active"`
	Time              int64                `json:"switch_over_time"`
	EffectivePriority int                  `json:"effective_priority"`
	PeerConnected     bool                 `json:"peer_connected"`
	Adjustments       []PriorityAdjustment `json:"adjustments"`
	ClusterEvents     []ClusterEvent       `json:"cluster_events"`
}

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

func (s *Client) GetJSON(ctx context.Context, path string) ([]byte, error) {
	if err := checkRead(path); err != nil {
		return nil, err
	}
	return s.get(ctx, strings.TrimPrefix(path, "/"), apiHost+path)
}

func (s *Client) QueryJSON(ctx context.Context, path, field string, ids []string, limit, page int) ([]byte, error) {
	if err := checkRead(path); err != nil {
		return nil, err
	}
	if field == "" || len(ids) == 0 {
		return nil, fmt.Errorf("query %s: nothing to ask about", path)
	}
	filter := fmt.Sprintf(`{%q:{"in":[%s]}}`, field, quoted(ids))
	return s.post(ctx, strings.TrimPrefix(path, "/"), apiHost+path, queryBody(filter, limit, page))
}

func quoted(ids []string) string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, `"`+id+`"`)
	}
	return strings.Join(out, ",")
}

func checkRead(path string) error { return checkPath(path, "read") }

func checkPath(path, what string) error {
	if !strings.HasPrefix(path, "/sdwan/") {
		return fmt.Errorf("path %q is not a /sdwan %s", path, what)
	}
	if strings.Contains(path, "://") || strings.HasPrefix(path, "//") {
		return fmt.Errorf("path %q names a host of its own", path)
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == ".." {
			return fmt.Errorf("path %q walks up out of /sdwan", path)
		}
	}
	return nil
}

func (s *Client) get(ctx context.Context, what, url string) ([]byte, error) {
	return s.do(ctx, what, http.MethodGet, url, nil)
}

func (s *Client) post(ctx context.Context, what, url string, body []byte) ([]byte, error) {
	return s.do(ctx, what, http.MethodPost, url, body)
}

// the cloudgenix SDK's urllib3 retry: 8 more tries, sleeping 0, 1.41s, 2.82s ... 90.35s
const (
	retries     = 8
	backoff     = 705883 * time.Microsecond
	retryAtMost = 5 * time.Minute
)

const (
	noConnection = -2
	noAnswer     = 0
)

// urllib3 retries these only for methods safe to repeat, and a failed connect for any
func again(method string, status int) bool {
	if status == noConnection || status == http.StatusTooManyRequests {
		return true
	}
	if method == http.MethodPost || method == http.MethodPatch {
		return false
	}
	switch status {
	case noAnswer, http.StatusRequestEntityTooLarge, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// urllib3 counts a reset or a timeout in a TLS handshake or proxy CONNECT as a read error, the rest as "other"
func brokenOff(err error) bool {
	var ne net.Error
	return errors.Is(err, syscall.ECONNRESET) || (errors.As(err, &ne) && ne.Timeout())
}

// urllib3 counts an SSL error as "other", which the SDK never retries
func badCertificate(err error) bool {
	var verify *tls.CertificateVerificationError
	var authority x509.UnknownAuthorityError
	var host x509.HostnameError
	var invalid x509.CertificateInvalidError
	var header tls.RecordHeaderError
	return errors.As(err, &verify) || errors.As(err, &authority) || errors.As(err, &host) ||
		errors.As(err, &invalid) || errors.As(err, &header)
}

// urllib3 sleeps nothing after the first failure, then factor × 2^(failures-1)
func backoffBefore(retry int) time.Duration {
	if retry <= 1 {
		return 0
	}
	return backoff << (retry - 1)
}

func (s *Client) do(ctx context.Context, what, method, url string, body []byte) ([]byte, error) {
	return s.call(ctx, what, method, url, body, false)
}

func (s *Client) call(ctx context.Context, what, method, url string, body []byte, write bool) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		answer, status, after, err := s.attempt(ctx, what, method, url, body, write)
		if err == nil || ctx.Err() != nil || attempt >= retries || !again(method, status) {
			return answer, err
		}
		wait := after
		if wait <= 0 {
			wait = backoffBefore(attempt + 1)
		}
		if dl, ok := ctx.Deadline(); ok && time.Until(dl) < wait {
			return answer, err
		}
		if s.verbose {
			fmt.Fprintf(os.Stderr, "%s: %v; trying again in %s\n", what, err, wait)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%s: %w", what, ctx.Err())
		case <-time.After(wait):
		}
	}
}

func (s *Client) attempt(ctx context.Context, what, method, url string, body []byte, write bool) ([]byte, int, time.Duration, error) {
	if err := s.limiter.Wait(ctx); err != nil {
		return nil, -1, 0, fmt.Errorf("%s: rate limit wait: %w", what, err)
	}

	if s.verbose {
		fmt.Fprintln(os.Stderr, method, url)
	}

	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	var dialing, connected, ready atomic.Bool
	trace := &httptrace.ClientTrace{
		DNSStart:     func(httptrace.DNSStartInfo) { dialing.Store(true) },
		ConnectStart: func(_, _ string) { dialing.Store(true) },
		ConnectDone: func(_, _ string, err error) {
			if err == nil {
				connected.Store(true)
			}
		},
		GotConn: func(httptrace.GotConnInfo) { dialing.Store(true); connected.Store(true); ready.Store(true) },
	}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), method, url, rdr)
	if err != nil {
		return nil, -1, 0, fmt.Errorf("%s: build request: %w", what, err)
	}
	req.Header.Set("Authorization", "Bearer "+s.getToken())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", browserUA)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := s.httpFor(write || patient(ctx)).Do(req)
	if err != nil {
		status := noAnswer
		switch {
		case badCertificate(err) || errors.Is(err, http.ErrSchemeMismatch) || !dialing.Load():
			status = -1
		case !connected.Load():
			status = noConnection
		case !ready.Load() && !brokenOff(err):
			status = -1
		}
		return nil, status, 0, fmt.Errorf("%s: response: %w", what, err)
	}

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

	answer, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, retryAfter(resp), fmt.Errorf("%s: read body: %w", what, err)
	}
	// the cloudgenix SDK takes a 204 as a good answer too
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return nil, resp.StatusCode, retryAfter(resp), fmt.Errorf("%s: status %s: %s", what, resp.Status, answer)
	}
	return answer, resp.StatusCode, 0, nil
}

// urllib3 sleeps for a Retry-After, in seconds or as a date, on any status it retries
func retryAfter(resp *http.Response) time.Duration {
	v := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return min(time.Duration(n)*time.Second, retryAtMost)
	}
	if at, err := http.ParseTime(v); err == nil && time.Until(at) > 0 {
		return min(time.Until(at), retryAtMost)
	}
	return 0
}

// The envelope the portal's query_* POSTs take, from its own calls.
// dest_page counts from one, not zero.
func queryBody(filter string, limit, page int) []byte {
	return []byte(fmt.Sprintf(
		`{"query_params":%s,"dest_page":%d,"limit":%d,"getDeleted":false,`+
			`"retrieved_fields_mask":false,"retrieved_fields":[]}`, filter, page, limit))
}

func elementFilter(ids []string) string {
	return `{"element_id":{"in":[` + quoted(ids) + `]}}`
}

const ClusterQueryLimit = 200

const clusterQueryPages = 20

type ClusterMemberStatus struct {
	ID        string `json:"id"`
	SiteID    string `json:"site_id"`
	ClusterID string `json:"spoke_cluster_id"`
	ElementID string `json:"element_id"`
	Status    string `json:"status"`
	Priority  int    `json:"priority"`
	UpdatedAt int64  `json:"last_status_update_ts"`
}

func (s *Client) SpokeClusterStatuses(ctx context.Context, elementIDs []string) ([]ClusterMemberStatus, error) {
	if len(elementIDs) == 0 {
		return nil, fmt.Errorf("spoke cluster status query: no elements to ask about")
	}

	out, err := s.clusterQuery(ctx, elementFilter(elementIDs))
	if err != nil {
		return nil, err
	}
	for _, st := range out {
		if st.ElementID != "" {
			return out, nil
		}
	}
	return nil, fmt.Errorf("spoke cluster status query: no members in %d item(s)", len(out))
}

func (s *Client) clusterQuery(ctx context.Context, filter string) ([]ClusterMemberStatus, error) {
	var out []ClusterMemberStatus
	for page := 1; page <= clusterQueryPages; page++ {
		body, err := s.post(ctx, "spoke cluster status query", clusterQueryURL(),
			queryBody(filter, ClusterQueryLimit, page))
		if err != nil {
			return nil, err
		}
		var env struct {
			Count int                   `json:"count"`
			Items []ClusterMemberStatus `json:"items"`
		}
		if err := json.Unmarshal(body, &env); err != nil {
			return nil, fmt.Errorf("spoke cluster status query: json decode: %w", err)
		}
		out = append(out, env.Items...)
		if len(env.Items) < ClusterQueryLimit {
			break
		}
	}
	return out, nil
}
