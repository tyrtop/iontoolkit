package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/clock"
	"tyrtop.com/iontk/internal/sase/api"
)

var flagInspectRaw bool

var inspectCmd = &cobra.Command{
	Use:   "inspect [element-id]",
	Short: "Print what the portal knows about one element",
	Long: `Fetches one element's record, its back-channel status and every interface
with its live state, and writes them to stdout as JSON. No device login and
no CLI session: this is the portal's view, so it works before ION credentials
exist.

--raw prints each response body exactly as it arrived, keyed by URL. The
status endpoints are modelled from the portal's own field names rather than a
published schema, so --raw is how a field that never decodes is told apart
from one the server left empty.`,
	Example: `  iontk inspect 1234567890
  iontk inspect --element 1234567890 --raw
  iontk inspect --fixtures examples/fixtures.yaml 1234567890`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		eid := flagElement
		if len(args) == 1 {
			eid = args[0]
		}
		return runInspect(cmd.Context(), eid)
	},
}

func runInspect(ctx context.Context, eid string) error {
	if eid == "" {
		return fmt.Errorf("an element ID is required: iontk inspect <element-id>")
	}

	f, err := resolveFlags(os.Stderr)
	if err != nil {
		return err
	}
	if err := f.ValidateCreds(); err != nil {
		return err
	}

	client, err := app.NewClient(ctx, app.OpenStore(), f, os.Stderr)
	if err != nil {
		return err
	}

	body, el, err := client.ElementRecord(ctx, eid)
	if err != nil {
		return err
	}

	snap := client.ElementSnapshot(ctx, el)
	snap.Raw[api.ElementURL(eid)] = body

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if flagInspectRaw {
		return enc.Encode(snap.Raw)
	}
	if err := snap.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	return enc.Encode(inspectOut(snap))
}

type inspectReport struct {
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

func inspectOut(snap api.Snapshot) inspectReport {
	out := inspectReport{Element: snap.Element}
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
