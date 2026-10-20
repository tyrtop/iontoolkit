package clock

import (
	_ "embed"
	"strings"
	"sync"

	// windows ships no zoneinfo, so LoadLocation needs the embedded copy
	_ "time/tzdata"
)

// generated from the Go distribution's lib/time/zoneinfo.zip; the stdlib lists no zones
//
//go:embed zones.txt
var zoneList string

var zones = sync.OnceValue(func() []string {
	lines := strings.Split(zoneList, "\n")
	out := make([]string, 0, len(lines)+2)
	out = append(out, UTC, Local)
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
})

func Zones() []string { return zones() }
