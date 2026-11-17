package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

type Snapshot struct {
	Element    Element
	Status     ElementStatus
	StatusErr  error
	Interfaces []InterfaceState
	IfacesErr  error
	Cluster    *SpokeClusterStatus
	ClusterErr error

	Raw map[string]json.RawMessage
}

type InterfaceState struct {
	Interface Interface
	Status    []InterfaceStatus
	StatusErr error
}

func (s Snapshot) Err() error {
	errs := []error{s.StatusErr, s.IfacesErr, s.ClusterErr}
	for _, i := range s.Interfaces {
		errs = append(errs, i.StatusErr)
	}
	return errors.Join(errs...)
}

func (s *Client) InspectElement(ctx context.Context, eid string) (Snapshot, error) {
	body, url, el, err := s.elementRecord(ctx, inspect, eid)
	if err != nil {
		return Snapshot{}, err
	}
	snap := s.ElementSnapshot(ctx, el)
	snap.Raw[url] = body
	return snap, nil
}

func (s *Client) ElementSnapshot(ctx context.Context, el Element) Snapshot {
	snap := Snapshot{Element: el, Raw: map[string]json.RawMessage{}}

	if body, url, err := s.getNamed(ctx, "element "+el.ID+" status", inspect, "element_status", vars{"element_id": el.ID}); err != nil {
		snap.StatusErr = err
	} else {
		snap.Raw[url] = body
		snap.Status, snap.StatusErr = decodeElementStatus(body)
	}

	if el.SiteID == "" {
		snap.IfacesErr = fmt.Errorf("element %s: interfaces need a site id", el.ID)
		return snap
	}

	if cid := el.ClusterID(); cid != "" {
		if body, url, err := s.getNamed(ctx, "spoke cluster "+cid+" status", inspect, "spokecluster_status",
			vars{"site_id": el.SiteID, "cluster_id": cid}); err != nil {
			snap.ClusterErr = err
		} else {
			snap.Raw[url] = body
			var st SpokeClusterStatus
			if err := json.Unmarshal(body, &st); err != nil {
				snap.ClusterErr = fmt.Errorf("spoke cluster %s status: json decode: %w", cid, err)
			} else {
				snap.Cluster = &st
			}
		}
	}

	body, url, err := s.getNamed(ctx, "element "+el.ID+" interfaces", inspect, "interfaces",
		vars{"site_id": el.SiteID, "element_id": el.ID})
	if err != nil {
		snap.IfacesErr = err
		return snap
	}
	snap.Raw[url] = body

	var wrapped struct {
		Items []Interface `json:"items"`
	}
	if err := json.Unmarshal(body, &wrapped); err != nil {
		snap.IfacesErr = fmt.Errorf("element %s interfaces: json decode: %w", el.ID, err)
		return snap
	}

	for _, in := range wrapped.Items {
		st := InterfaceState{Interface: in}
		if body, url, err := s.getNamed(ctx, "interface "+in.ID+" status", inspect, "interface_status",
			vars{"site_id": el.SiteID, "element_id": el.ID, "interface_id": in.ID}); err != nil {
			st.StatusErr = err
		} else {
			snap.Raw[url] = body
			st.Status, st.StatusErr = decodeInterfaceStatus(in.ID, body)
		}
		snap.Interfaces = append(snap.Interfaces, st)
	}
	return snap
}
