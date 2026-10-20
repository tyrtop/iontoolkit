package clock

import (
	"strings"
	"testing"
	"time"
)

func pin(t *testing.T, spec string) error {
	t.Helper()
	t.Cleanup(func() { _ = Use(UTC) })
	return Use(spec)
}

func TestUseResolvesTheZonesTheSettingOffers(t *testing.T) {
	at := time.Date(2026, 10, 18, 13, 25, 0, 0, time.UTC)

	tests := []struct {
		name string
		spec string
		want string
	}{
		{"nothing saved is UTC", "", "2026-10-18 13:25 UTC"},
		{"UTC by name", "UTC", "2026-10-18 13:25 UTC"},
		{"a named zone shifts the reading", "America/Chicago", "2026-10-18 08:25 CDT"},
		{"local is whatever this machine is", Local, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := pin(t, tc.spec); err != nil {
				t.Fatalf("Use(%q) = %v", tc.spec, err)
			}
			got := Stamp(at)
			if tc.want == "" {
				if got == "" {
					t.Error("Stamp() is empty for a real time")
				}
				return
			}
			if got != tc.want {
				t.Errorf("Stamp() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAZoneThatWillNotLoadFallsBackToUTCAndSaysSo(t *testing.T) {
	err := pin(t, "Mars/Olympus_Mons")
	if err == nil {
		t.Fatal("Use() accepted a zone that does not exist")
	}
	if Name() != UTC {
		t.Errorf("Name() = %q, want UTC after a bad zone", Name())
	}
	if Problem() == "" {
		t.Error("Problem() is empty after a zone that would not load")
	}
	if got := Stamp(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); got != "2026-01-01 00:00 UTC" {
		t.Errorf("Stamp() = %q, want the UTC fallback", got)
	}
	if Valid("Mars/Olympus_Mons") == nil {
		t.Error("Valid() accepted a zone that does not exist")
	}
}

func TestStampNamesItsZone(t *testing.T) {
	if err := pin(t, "Europe/London"); err != nil {
		t.Fatal(err)
	}
	got := Stamp(time.Date(2026, 1, 15, 9, 0, 0, 0, time.UTC))
	if !strings.HasSuffix(got, " GMT") {
		t.Errorf("Stamp() = %q, want the zone named", got)
	}
}

func TestAZeroTimeIsBlank(t *testing.T) {
	if got := Stamp(time.Time{}); got != "" {
		t.Errorf("Stamp(zero) = %q, want empty", got)
	}
	if got := RFC3339(time.Time{}); got != "" {
		t.Errorf("RFC3339(zero) = %q, want empty", got)
	}
}

func TestRFC3339CarriesTheOffset(t *testing.T) {
	if err := pin(t, "America/Chicago"); err != nil {
		t.Fatal(err)
	}
	got := RFC3339(time.Date(2026, 10, 18, 13, 25, 0, 0, time.UTC))
	if got != "2026-10-18T08:25:00-05:00" {
		t.Errorf("RFC3339() = %q, want the Chicago offset", got)
	}
}
