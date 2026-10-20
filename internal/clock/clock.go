package clock

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

const Local = "local"

const UTC = "UTC"

const layout = "2006-01-02 15:04 MST"

var (
	mu      sync.RWMutex
	loc     = time.UTC
	name    = UTC
	problem string
)

func Use(spec string) error {
	l, resolved, err := load(spec)

	mu.Lock()
	defer mu.Unlock()
	if err != nil {
		loc, name, problem = time.UTC, UTC, err.Error()
		return err
	}
	loc, name, problem = l, resolved, ""
	return nil
}

func load(spec string) (*time.Location, string, error) {
	switch s := strings.TrimSpace(spec); {
	case s == "", strings.EqualFold(s, UTC):
		return time.UTC, UTC, nil
	case strings.EqualFold(s, Local):
		return time.Local, Local, nil
	default:
		l, err := time.LoadLocation(s)
		if err != nil {
			return nil, "", fmt.Errorf("zone %q: %w", s, err)
		}
		return l, s, nil
	}
}

func Valid(spec string) error {
	_, _, err := load(spec)
	return err
}

func Zone() *time.Location {
	mu.RLock()
	defer mu.RUnlock()
	return loc
}

func Name() string {
	mu.RLock()
	defer mu.RUnlock()
	return name
}

func Problem() string {
	mu.RLock()
	defer mu.RUnlock()
	return problem
}

func Stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(Zone()).Format(layout)
}

func RFC3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(Zone()).Format(time.RFC3339)
}
