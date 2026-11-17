package runtime

import "encoding/json"

func (rt *Runtime) ActionState(screen string) map[string]any {
	t, _ := rt.Tenant()
	state := map[string]any{
		"tenant":    t.Name,
		"tsg":       t.TSGID,
		"mode":      rt.Mode(),
		"selection": rt.SelectedCount(),
		"screen":    screen,
	}
	if el, ok := rt.Current(); ok {
		if b, err := json.Marshal(el.Element); err == nil {
			var rec map[string]any
			if json.Unmarshal(b, &rec) == nil {
				state["element"] = rec
			}
		}
		state["element_id"] = el.ID
		state["site_id"] = el.SiteID
		state["cluster_id"] = el.ClusterID()
		state["element_name"] = el.Label()
	}
	return state
}
