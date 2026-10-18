package ionterm

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/vt"
)

func replay(t *testing.T, cols, rows int) *vt.Emulator {
	t.Helper()

	f, err := os.Open("testdata/ion-session.raw")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	emu := vt.NewEmulator(cols, rows)

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		frame, err := strconv.Unquote(line)
		if err != nil {
			t.Fatalf("bad fixture line %q: %v", line, err)
		}
		if _, err := emu.WriteString(frame); err != nil {
			t.Fatal(err)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return emu
}

func TestRepaintsCollapse(t *testing.T) {
	emu := replay(t, 69, 24)
	screen := emu.String()
	t.Log("\n" + screen)

	if n := strings.Count(screen, "S5032-RTR-01#"); n > 2 {
		t.Errorf("repaints did not collapse: %d prompt occurrences on screen", n)
	}
	if n := strings.Count(screen, "dump interface status 4"); n > 1 {
		t.Errorf("echoed command appears %d times, want at most 1", n)
	}
}

func TestNoEscapeBytesLeak(t *testing.T) {
	screen := replay(t, 69, 24).String()

	if strings.Contains(screen, "\x1b") {
		t.Error("ESC byte present in rendered screen")
	}
	for _, frag := range []string{"[J", "[2K", "[31m", "[0m"} {
		if strings.Contains(screen, frag) {
			t.Errorf("unhandled sequence %q visible in rendered screen", frag)
		}
	}
	if strings.Contains(screen, "\b") {
		t.Error("backspace byte present in rendered screen")
	}
}

func TestOutputAndTabAlignment(t *testing.T) {
	screen := replay(t, 69, 24).String()

	for _, want := range []string{"Interface", "DPDK Controlled", "04:47:2a:18:0e:05", "Not connected"} {
		if !strings.Contains(screen, want) {
			t.Errorf("output missing %q", want)
		}
	}

	if strings.Contains(screen, "\t") {
		t.Error("raw tab survived into the screen; emulator did not expand tab stops")
	}

	var iface, device string
	for _, ln := range strings.Split(screen, "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "Interface") {
			iface = ln
		}
		if strings.HasPrefix(strings.TrimSpace(ln), "Device") {
			device = ln
		}
	}
	if iface == "" || device == "" {
		t.Fatalf("could not find both rows\niface=%q\ndevice=%q", iface, device)
	}
	if strings.Index(iface, ":") != strings.Index(device, ":") {
		t.Errorf("tab stops misaligned:\n%q (colon at %d)\n%q (colon at %d)",
			iface, strings.Index(iface, ":"), device, strings.Index(device, ":"))
	}
}

func TestNarrowWidthWrapsDifferently(t *testing.T) {
	wide := replay(t, 200, 24).String()
	narrow := replay(t, 40, 24).String()

	if wide == narrow {
		t.Skip("fixture has no lines long enough to wrap at 40 cols")
	}
	t.Logf("40-col render:\n%s", narrow)
}
