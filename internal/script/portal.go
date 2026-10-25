package script

type Write struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Body   any    `json:"body,omitempty"`
}

type Planned interface {
	PlannedWrites() []Write
}
