package logging

import (
	"strings"
	"testing"
)

func TestRecentReadsAsALineOfText(t *testing.T) {
	start(t, Options{})
	L().Info("stored refresh token accepted", "tsg", "1436146912", "rotated", true)

	got := Recent(1)
	if len(got) != 1 {
		t.Fatalf("Recent(1) = %q, want one line", got)
	}
	if !strings.Contains(got[0], "info  stored refresh token accepted") {
		t.Errorf("Recent(1)[0] = %q, want the level and the message", got[0])
	}
	if strings.Contains(got[0], `"msg"`) {
		t.Errorf("Recent(1)[0] = %q, want the JSON unwrapped for the Debug page", got[0])
	}
}

func TestRecentPassesThroughWhatItCannotParse(t *testing.T) {
	if got := summarize("not json at all"); got != "not json at all" {
		t.Errorf("summarize() = %q, want the line as it stands", got)
	}
}
