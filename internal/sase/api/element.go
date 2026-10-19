package api

type Element struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	HWID      string `json:"hw_id"`
	Model     string `json:"model_name"`
	Software  string `json:"software_version"`
	SiteID    string `json:"site_id"`
	Connected bool   `json:"connected"`
	State     string `json:"state"`

	Description string `json:"description"`
	Serial      string `json:"serial_number"`
	Role        string `json:"role"`
	// AllowedRoles is what this element could be, where Role is what it is:
	// ["HUB","SPOKE"] on a spoke that could be promoted.
	AllowedRoles []string `json:"allowed_roles"`
	Tags         []string `json:"tags"`
	// Cluster is the record's own cluster_id, which was null on a spoke that
	// is in a cluster -- read ClusterID() rather than this.
	Cluster      string   `json:"cluster_id"`
	SpokeHA      *SpokeHA `json:"spoke_ha_config"`
	AdminSuspend string   `json:"admin_suspend_state"`
	FIPSMode     string   `json:"fips_mode"`
}

// ClusterID is the HA cluster the element is in, from whichever field carries
// it: a clustered spoke had spoke_ha_config.cluster_id set and the record's
// own cluster_id null.
func (e Element) ClusterID() string {
	if e.SpokeHA != nil && e.SpokeHA.ClusterID != "" {
		return e.SpokeHA.ClusterID
	}
	return e.Cluster
}

// The record also carries a device_mode, and it is not the one in
// ElementStatus: an element answering "none" here answered "l3-mode" there on
// the same request. Whatever this one means, the status is the live answer, so
// the field is deliberately not modelled on both.

// SpokeHA is the element's half of a spoke HA pair: which cluster it belongs
// to and what it watches to decide it should take over. Whether it is the
// active member is not in here -- that is the cluster's status, not the
// element's config.
type SpokeHA struct {
	ClusterID       string   `json:"cluster_id"`
	Enable          bool     `json:"enable"`
	Priority        int      `json:"priority"`
	SourceInterface string   `json:"source_interface"`
	Track           *HATrack `json:"track"`
}

type HATrack struct {
	Interfaces    []HATrackItem `json:"interfaces"`
	WANInterfaces []HATrackItem `json:"waninterfaces"`
}

// One type for both lists: the portal spells out the LAN entry
// ({interface_id, reduce_priority}) and not the WAN one, so the WAN id is
// carried as a second tag rather than guessed at in a type of its own.
type HATrackItem struct {
	InterfaceID    string `json:"interface_id"`
	WANInterfaceID string `json:"wan_interface_id"`
	ReducePriority int    `json:"reduce_priority"`
}
