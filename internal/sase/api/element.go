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

	Description  string   `json:"description"`
	Serial       string   `json:"serial_number"`
	Role         string   `json:"role"`
	AllowedRoles []string `json:"allowed_roles"`
	Tags         []string `json:"tags"`
	// null on a spoke that is in a cluster -- read ClusterID() instead
	Cluster      string   `json:"cluster_id"`
	SpokeHA      *SpokeHA `json:"spoke_ha_config"`
	AdminSuspend string   `json:"admin_suspend_state"`
	FIPSMode     string   `json:"fips_mode"`
}

func (e Element) ClusterID() string {
	if e.SpokeHA != nil && e.SpokeHA.ClusterID != "" {
		return e.SpokeHA.ClusterID
	}
	return e.Cluster
}

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

type HATrackItem struct {
	InterfaceID    string `json:"interface_id"`
	WANInterfaceID string `json:"wan_interface_id"`
	ReducePriority int    `json:"reduce_priority"`
}
