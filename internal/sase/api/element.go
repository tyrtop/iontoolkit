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
}
