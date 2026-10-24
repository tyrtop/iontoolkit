package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"tyrtop.com/iontk/internal/clock"
	"tyrtop.com/iontk/internal/sase/api"
)

func (rt *Runtime) elementClient(ctx context.Context, eid string) (*api.Client, error) {
	for _, el := range rt.Elements() {
		if el.ID != eid {
			continue
		}
		if t, ok := rt.TenantOf(el.TSG); ok {
			c, err := rt.Connect(ctx, t)
			if err != nil {
				return nil, fmt.Errorf("%s: %s", t.Name, rt.explain(err))
			}
			return c, nil
		}
	}
	p, _, err := rt.Portal(ctx)
	if err != nil {
		return nil, err
	}
	c, ok := p.(*api.Client)
	if !ok {
		return nil, fmt.Errorf("no portal client to read %s with", eid)
	}
	return c, nil
}

func inspectCommands() []Command {
	return []Command{{
		Name: "element.inspect", Slow: true,
		Help: "what the portal knows about one element, as JSON: id= raw=yes for each response as it came",
		Args: []Arg{{Name: "id", Required: true}, {Name: "raw", Bool: true, Help: "each response as it came"}},
		Run: func(rt *Runtime, c *Call) error {
			eid := c.Arg("id")
			client, err := rt.elementClient(c.Ctx, eid)
			if err != nil {
				return err
			}
			body, el, err := client.ElementRecord(c.Ctx, eid)
			if err != nil {
				return err
			}
			snap := client.ElementSnapshot(c.Ctx, el)
			snap.Raw[api.ElementURL(eid)] = body
			var out any = InspectOut(snap)
			if c.Arg("raw") == "yes" {
				out = snap.Raw
			}
			c.Result = out
			b, err := json.MarshalIndent(out, "", "  ")
			if err != nil {
				return err
			}
			for _, l := range strings.Split(string(b), "\n") {
				c.Say("%s", l)
			}
			return nil
		},
	}}
}

type InspectReport struct {
	Element    api.Element             `json:"element"`
	Status     *statusReport           `json:"status"`
	StatusErr  string                  `json:"status_error,omitempty"`
	Interfaces []interfaceReport       `json:"interfaces"`
	IfacesErr  string                  `json:"interfaces_error,omitempty"`
	Cluster    *api.SpokeClusterStatus `json:"spoke_cluster_status,omitempty"`
	ClusterErr string                  `json:"spoke_cluster_error,omitempty"`
}

type statusReport struct {
	ElementID           string          `json:"element_id"`
	SiteID              string          `json:"site_id"`
	DeviceMode          string          `json:"device_mode,omitempty"`
	ControllerInterface string          `json:"controller_connection_intf,omitempty"`
	LastRebooted        string          `json:"last_rebooted,omitempty"`
	LastRebootedInfo    string          `json:"last_rebooted_info,omitempty"`
	LastDisconnected    string          `json:"last_disconnected,omitempty"`
	Channels            []channelReport `json:"channels"`
}

type channelReport struct {
	Name           string `json:"name"`
	Connected      bool   `json:"connected"`
	From           string `json:"from,omitempty"`
	ConnectedOn    int64  `json:"connected_on_utc,omitempty"`
	ConnectedAt    string `json:"connected_at,omitempty"`
	DisconnectedOn int64  `json:"disconnected_on_utc,omitempty"`
	DisconnectedAt string `json:"disconnected_at,omitempty"`
}

type interfaceReport struct {
	Interface api.Interface         `json:"interface"`
	Status    []api.InterfaceStatus `json:"status"`
	StatusErr string                `json:"status_error,omitempty"`
}

func InspectOut(snap api.Snapshot) InspectReport {
	out := InspectReport{Element: snap.Element}
	if snap.StatusErr != nil {
		out.StatusErr = snap.StatusErr.Error()
	} else {
		st := statusReport{
			ElementID:           snap.Status.ElementID,
			SiteID:              snap.Status.SiteID,
			DeviceMode:          snap.Status.DeviceMode,
			ControllerInterface: snap.Status.ControllerInterface,
			LastRebooted:        utcString(snap.Status.LastRebooted),
			LastRebootedInfo:    snap.Status.LastRebootedInfo,
			LastDisconnected:    utcString(snap.Status.LastDisconnected),
		}
		for _, c := range snap.Status.Channels {
			st.Channels = append(st.Channels, channelReport{
				Name: c.Name, Connected: c.Connected, From: c.From,
				ConnectedOn: c.On, ConnectedAt: utcString(c.On),
				DisconnectedOn: c.Off, DisconnectedAt: utcString(c.Off),
			})
		}
		out.Status = &st
	}

	if snap.IfacesErr != nil {
		out.IfacesErr = snap.IfacesErr.Error()
	}
	out.Cluster = snap.Cluster
	if snap.ClusterErr != nil {
		out.ClusterErr = snap.ClusterErr.Error()
	}
	out.Interfaces = make([]interfaceReport, 0, len(snap.Interfaces))
	for _, in := range snap.Interfaces {
		r := interfaceReport{Interface: in.Interface, Status: in.Status}
		if in.StatusErr != nil {
			r.StatusErr = in.StatusErr.Error()
		}
		out.Interfaces = append(out.Interfaces, r)
	}
	return out
}

func utcString(v int64) string {
	return clock.RFC3339(api.UTCTime(v))
}
