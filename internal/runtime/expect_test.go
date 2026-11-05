package runtime

import (
	"context"
	"strings"
	"testing"
	"time"
)

func openBoth(t *testing.T) (*Runtime, string) {
	t.Helper()
	rt := deviceRuntime(t)
	run(t, rt, "list.select-matches")
	if err := rt.Exec(&Call{Ctx: context.Background()}, "session.open"); err != nil {
		t.Fatalf("session.open: %v", err)
	}
	t.Cleanup(rt.Sessions().CloseAll)
	for _, el := range rt.Elements() {
		if el.Name == "acme-rtr-01" {
			return rt, el.ID
		}
	}
	t.Fatal("the fleet has no acme-rtr-01")
	return nil, ""
}

func send(t *testing.T, rt *Runtime, args map[string]string) (map[string]any, time.Duration) {
	t.Helper()
	c := &Call{Ctx: context.Background(), Args: args}
	start := time.Now()
	if err := rt.Exec(c, "session.send"); err != nil {
		t.Fatalf("session.send %v: %v", args, err)
	}
	got, _ := c.Result.(map[string]any)
	return got, time.Since(start)
}

func TestASendThatExpectsThePromptReturnsWhenTheCommandIsDone(t *testing.T) {
	rt, first := openBoth(t)
	got, took := send(t, rt, map[string]string{"element": first, "text": "dump device status", "expect": "acme-rtr-01#", "wait": "10s"})
	out, _ := got["output"].(string)
	if got["found"] != true || !strings.Contains(out, "FAKE0001") || strings.Contains(out, "acme-rtr-01#") || took > 5*time.Second {
		t.Errorf("sent to acme-rtr-01 while acme-rtr-02 was active: %v after %s, want acme-rtr-01's own answer well inside the 10s", got, took)
	}
	if w := rt.Waits(); len(w) != 0 {
		t.Errorf("the finished send is still listed as waiting: %+v", w)
	}

	got, took = send(t, rt, map[string]string{"element": first, "text": "dump device status", "expect": "never comes back", "wait": "300ms"})
	if got["found"] != false || took < 300*time.Millisecond {
		t.Errorf("expecting text that never comes = %v after %s, want found false at the wait", got, took)
	}
}

func TestASessionIsClosedByItsElement(t *testing.T) {
	rt, first := openBoth(t)
	if err := rt.Exec(&Call{Args: map[string]string{"element": first}}, "session.close"); err != nil {
		t.Fatal(err)
	}
	if all := rt.Sessions().All(); len(all) != 1 || all[0].Name != "acme-rtr-02" {
		t.Errorf("after closing acme-rtr-01 the sessions are %d, want acme-rtr-02 alone", len(all))
	}
	err := rt.Exec(&Call{Args: map[string]string{"element": first}}, "session.close")
	if err == nil || !strings.Contains(err.Error(), "no session is open on element "+first) {
		t.Errorf("closing it twice = %v", err)
	}
}

func TestAWaitSaysWhatAndHowLong(t *testing.T) {
	start := time.Now()
	for _, tt := range []struct {
		wait Wait
		at   time.Duration
		want string
	}{
		{Wait{What: "S5037-RTR-01: debug bw-test", Since: start, Until: start.Add(3 * time.Minute)}, 65 * time.Second,
			"S5037-RTR-01: debug bw-test · 1m5s, up to 3m"},
		{Wait{What: "dump vpn summary on 2 element(s)", Since: start}, 2 * time.Second,
			"dump vpn summary on 2 element(s) · 2s"},
	} {
		if got := tt.wait.Line(start.Add(tt.at)); got != tt.want {
			t.Errorf("Line = %q, want %q", got, tt.want)
		}
	}
}
