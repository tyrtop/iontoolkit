package runtime

import (
	"errors"
	"fmt"
	"testing"

	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/saseauth"
)

func TestWhichTenantsABrowserSignInWouldRescue(t *testing.T) {
	interactive := fmt.Errorf("%w: no existing browser session", saseauth.ErrInteractiveRequired)
	for _, tt := range []struct {
		name string
		l    Load
		want bool
	}{
		{"nothing wrong", Load{Note: "refreshed"}, false},
		{"no credential at all", Load{Err: interactive}, true},
		{"answered, but only from a stale cache", Load{Elements: []api.Element{{ID: "e1"}}, Stale: interactive}, true},
		{"unreachable for some other reason", Load{Err: errors.New("dial tcp: refused")}, false},
		{"stale for some other reason", Load{Stale: errors.New("dial tcp: refused")}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.l.WantsSignIn(); got != tt.want {
				t.Errorf("WantsSignIn() = %v, want %v", got, tt.want)
			}
		})
	}
}
