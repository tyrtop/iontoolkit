package runtime

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/fixture"
	"tyrtop.com/iontk/internal/ionterm"
	"tyrtop.com/iontk/internal/sase"
	"tyrtop.com/iontk/internal/sase/api"
)

const twoTenants = `
tenants:
  - name: Acme Co
    tsg_id: "111"
    elements:
      - {name: acme-rtr-01, site: Store 0001, connected: true}
      - {name: acme-rtr-02, site: Store 0001, connected: true}
  - name: Lab
    tsg_id: "222"
    elements:
      - {name: lab-rtr-01, site: Lab, connected: true}
`

func fleetRuntime(t *testing.T, broken ...string) (*Runtime, *config.Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixtures.yaml")
	os.WriteFile(path, []byte(twoTenants), 0o600)
	set, err := fixture.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	rt, st := withConfig(t)
	st.Config()
	cfg := rt.Config()
	cfg.Tenants = []config.Tenant{
		{Name: "Acme Co", TSGID: "111", ClientID: "a", Added: true},
		{Name: "Lab", TSGID: "222", ClientID: "b", Added: true},
		{Name: "No creds", TSGID: "", Added: true},
	}
	rt.SetElements(nil)
	bad := map[string]bool{}
	for _, b := range broken {
		bad[b] = true
	}
	rt.UseConnector(Connector{
		Check: func(_ *config.Store, t config.Tenant) error {
			if t.TSGID == "" {
				return errors.New("no tsg")
			}
			return nil
		},
		Connect: func(_ context.Context, _ *config.Store, t config.Tenant) (*api.Client, error) {
			if bad[t.TSGID] {
				return nil, errors.New("401 unauthorized")
			}
			c := &http.Client{Transport: set.Transport(t.TSGID)}
			return api.NewClient(c, c, "fixture", false, 100, 100), nil
		},
		Explain: func(err error) string { return "explained: " + err.Error() },
	})
	return rt, st
}

func TestRefreshLoadsEveryUsableTenantAndSkipsTheRest(t *testing.T) {
	rt, st := fleetRuntime(t)
	var said []string
	err := rt.Exec(&Call{Out: func(s string) { said = append(said, s) }}, "fleet.refresh")
	if err != nil {
		t.Fatal(err)
	}
	out := strings.Join(said, "\n")
	if !strings.Contains(out, "skipped No creds") || !strings.Contains(out, "Acme Co: 2 elements") || !strings.Contains(out, "Lab: 1 elements") {
		t.Errorf("said:\n%s", out)
	}
	if got := names(rt.Elements()); got != "acme-rtr-01 acme-rtr-02 lab-rtr-01" {
		t.Errorf("rows = %s", got)
	}
	f := rt.Fleet()
	if f.State != FleetUpdated || f.Count != 3 || f.Pending != 0 || time.Since(f.At) > time.Minute {
		t.Errorf("fleet = %+v", f)
	}
	if c, err := st.Cache("222"); err != nil || len(c.Elements) != 1 {
		t.Errorf("Lab's cache = %+v, %v", c, err)
	}
	if rt.Clients().Count() != 2 {
		t.Errorf("%d clients kept, want one per tenant signed in", rt.Clients().Count())
	}
}

func TestAFailedTenantIsItsIssueAndTheOthersStillLoad(t *testing.T) {
	rt, _ := fleetRuntime(t, "222")
	err := rt.Exec(&Call{}, "fleet.refresh")
	if err == nil || !strings.Contains(err.Error(), "1 of 2 tenants failed") {
		t.Errorf("refresh = %v", err)
	}
	if got := names(rt.Elements()); got != "acme-rtr-01 acme-rtr-02" {
		t.Errorf("rows = %s", got)
	}
	if got := rt.Fleet().Issues["222"]; got != "explained: 401 unauthorized" {
		t.Errorf("Lab's issue = %q", got)
	}
}

func TestLoadsApplyInTheOrderTheyLand(t *testing.T) {
	rt, _ := fleetRuntime(t)
	tenants, _, err := rt.BeginRefresh()
	if err != nil || len(tenants) != 2 || rt.Fleet().State != FleetUpdating || rt.Fleet().Pending != 2 {
		t.Fatalf("begin: %v %v %+v", tenants, err, rt.Fleet())
	}
	lab := rt.FetchTenant(context.Background(), tenants[1])
	if left := rt.ApplyLoad(lab); left != 1 || names(rt.Elements()) != "lab-rtr-01" || rt.Fleet().State != FleetUpdating {
		t.Errorf("after Lab: left %d, rows %s, %+v", left, names(rt.Elements()), rt.Fleet())
	}
	acme := rt.FetchTenant(context.Background(), tenants[0])
	if left := rt.ApplyLoad(acme); left != 0 || rt.Fleet().State != FleetUpdated || rt.Fleet().Count != 3 {
		t.Errorf("after Acme: left %d, %+v", left, rt.Fleet())
	}

	acme.Elements = acme.Elements[:1]
	rt.SetPending(1)
	rt.ApplyLoad(acme)
	if got := names(rt.Elements()); got != "acme-rtr-01 lab-rtr-01" {
		t.Errorf("after Acme shrank: %s", got)
	}
}

func TestNothingUsableFailsTheFleetAndSaysWhy(t *testing.T) {
	rt, _ := fleetRuntime(t)
	cfg := rt.Config()
	cfg.Tenants = cfg.Tenants[2:]
	_, skipped, err := rt.BeginRefresh()
	if err == nil || !strings.Contains(err.Error(), "no usable credentials for No creds") || len(skipped) != 1 || rt.Fleet().State != FleetFailed {
		t.Errorf("begin = %v %v %+v", skipped, err, rt.Fleet())
	}
}

func TestTheCacheFillsTheListWithoutThePortal(t *testing.T) {
	rt, st := fleetRuntime(t)
	if err := rt.Exec(&Call{}, "fleet.refresh"); err != nil {
		t.Fatal(err)
	}
	cfg := rt.Config()
	fresh := New()
	fresh.UseConfig(st, cfg)
	var said []string
	if err := fresh.Exec(&Call{Out: func(s string) { said = append(said, s) }}, "fleet.cache"); err != nil {
		t.Fatal(err)
	}
	if got := names(fresh.Elements()); got != "acme-rtr-01 acme-rtr-02 lab-rtr-01" || fresh.Fleet().State != FleetUpdating {
		t.Errorf("from cache: %s %+v", got, fresh.Fleet())
	}
	if !strings.Contains(strings.Join(said, ""), "3 elements") || !strings.Contains(strings.Join(said, ""), "some tenants have none") {
		t.Errorf("said %v", said)
	}

	cfg.Tenants = cfg.Tenants[:2]
	if n, _, missing := fresh.LoadCache(); n != 3 || missing || fresh.Fleet().State != FleetCached {
		t.Errorf("every tenant cached: %d %v %+v", n, missing, fresh.Fleet())
	}
	if fresh.Clients().Count() != 0 {
		t.Error("reading the cache signed in")
	}
}

func TestTheFleetIsTheAddedTenantsOrTheOneInUse(t *testing.T) {
	rt, _ := withConfig(t)
	cfg := rt.Config()
	acme := config.Tenant{Name: "Acme Co", TSGID: "111"}
	if len(rt.FleetTenants()) != 0 {
		t.Error("a fleet with no tenant at all")
	}
	rt.UseTenant(acme)
	if got := rt.FleetTenants(); len(got) != 1 || got[0].Name != "Acme Co" {
		t.Errorf("with one in use: %v", got)
	}
	cfg.Tenants = []config.Tenant{{Name: "Lab", TSGID: "222", Added: true}}
	if got := rt.FleetTenants(); len(got) != 1 || got[0].Name != "Lab" {
		t.Errorf("with one added: %v", got)
	}
	rt.SetAuthMode(AuthTenant)
	if got := rt.FleetTenants(); len(got) != 1 || got[0].Name != "Acme Co" {
		t.Errorf("as a service account: %v", got)
	}
	rt.PreferAdded(true)
	if got := rt.FleetTenants(); len(got) != 1 || got[0].Name != "Lab" {
		t.Errorf("the fake fleet: %v", got)
	}
}

const devices = `
tenants:
  - name: Acme Co
    tsg_id: "111"
    users:
      - {name: admin, password: ion}
    elements:
      - name: acme-rtr-01
        connected: true
        responses:
          "dump device status": "Serial : FAKE0001"
      - name: acme-rtr-02
        connected: true
        responses:
          "dump device status": "Serial : FAKE0002"
`

func deviceRuntime(t *testing.T) *Runtime {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixtures.yaml")
	os.WriteFile(path, []byte(devices), 0o600)
	set, err := fixture.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	rt, _ := withConfig(t)
	rt.Config().Tenants = []config.Tenant{{Name: "Acme Co", TSGID: "111", ClientID: "a", Added: true,
		Users: []config.IONUser{{Name: "admin", Default: true}}}}
	rt.UseTenant(rt.Config().Tenants[0])
	rt.SetElements(nil)
	rt.UseConnector(Connector{
		Connect: func(_ context.Context, _ *config.Store, t config.Tenant) (*api.Client, error) {
			c := &http.Client{Transport: set.Transport(t.TSGID)}
			return api.NewClient(c, c, "fixture", false, 100, 100), nil
		},
	})
	rt.UseDevice(Device{
		Login: func(_ *config.Store, _ config.Tenant, user string) (string, string) { return user, "ion" },
		Options: func(_ *config.Store, _ config.Tenant, user string, cmds []string) (sase.Options, error) {
			return sase.Options{Commands: cmds, Username: user, Password: "ion", Concurrency: 2,
				ElementTimeout: 5 * time.Second, SessionTimeout: 5 * time.Second}, nil
		},
		Session: func(*api.Client) sase.Client { return set.SessionClient("", nil) },
		Dialer:  func(*api.Client) ionterm.Dialer { return set.Dialer() },
	})
	if err := rt.Exec(&Call{}, "fleet.refresh"); err != nil {
		t.Fatal(err)
	}
	return rt
}

func TestABatchRunsOnTheSelectionAndKeepsTheRecord(t *testing.T) {
	rt := deviceRuntime(t)
	run(t, rt, "list.select-matches")
	var said []string
	err := rt.Exec(&Call{Ctx: context.Background(), Args: map[string]string{"cmd": "dump device status"},
		Out: func(s string) { said = append(said, s) }}, "batch.run")
	if err != nil {
		t.Fatalf("batch.run: %v\n%s", err, strings.Join(said, "\n"))
	}
	b := rt.Batch()
	if b.Total() != 2 || b.Finished != 2 || b.Failed != 0 || len(b.Running) != 0 {
		t.Errorf("batch = %+v", b)
	}
	out := strings.Join(said, "\n")
	if !strings.Contains(out, "FAKE0001") || !strings.Contains(out, "FAKE0002") || !strings.Contains(out, "2 of 2 elements done") {
		t.Errorf("said:\n%s", out)
	}
}

func TestABatchRefusesAWriteUnlessWritesAreAllowed(t *testing.T) {
	rt := deviceRuntime(t)
	err := rt.Exec(&Call{Args: map[string]string{"cmd": "config system reboot"}}, "batch.run")
	if err == nil || !strings.Contains(err.Error(), "not read-only") {
		t.Errorf("a write ran: %v", err)
	}
	if rt.Batch().Total() != 0 {
		t.Error("a refused run was recorded")
	}
}

func TestSessionsOpenSendAndClose(t *testing.T) {
	rt := deviceRuntime(t)
	run(t, rt, "list.select-matches")
	var said []string
	if err := rt.Exec(&Call{Ctx: context.Background(), Out: func(s string) { said = append(said, s) }}, "session.open"); err != nil {
		t.Fatalf("session.open: %v (%v)", err, said)
	}
	if rt.Sessions().Count() != 2 || rt.Connecting() {
		t.Fatalf("%d sessions, connecting %v", rt.Sessions().Count(), rt.Connecting())
	}
	said = nil
	if err := rt.Exec(&Call{Out: func(s string) { said = append(said, s) }}, "session.list"); err != nil {
		t.Fatal(err)
	}
	if len(said) != 2 || !strings.HasPrefix(said[1], "> 2") {
		t.Errorf("list = %v, want the last opened active", said)
	}
	said = nil
	if err := rt.Exec(&Call{Ctx: context.Background(), Args: map[string]string{"text": "dump device status", "wait": "300ms"},
		Out: func(s string) { said = append(said, s) }}, "session.send"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(said, ""), "FAKE0002") {
		t.Errorf("the session did not answer:\n%s", strings.Join(said, "\n"))
	}
	run(t, rt, "session.close")
	if rt.Sessions().Count() != 1 {
		t.Errorf("%d sessions after closing one", rt.Sessions().Count())
	}
	rt.Sessions().CloseAll()
}

func TestASessionWithNothingToDialWithFailsItsDial(t *testing.T) {
	rt := deviceRuntime(t)
	rt.UseDevice(Device{Dialer: func(*api.Client) ionterm.Dialer { return nil }})
	el := rt.Elements()[0]
	if _, err := rt.OpenSession(context.Background(), el.ID, el.Name, el.TSG); err == nil {
		t.Fatal("a session opened with nothing to dial it")
	}
	if d, ok := rt.Dialing(el.ID); !ok || !d.Failed {
		t.Errorf("dial = %+v %v", d, ok)
	}
}

func TestATenantWithNoSitesKeepsTheOthersNames(t *testing.T) {
	rt := New()
	rt.MergeSites(map[string]string{"111/s1": "Store 0001"})
	rt.MergeSites(nil)
	rt.MergeSites(map[string]string{})
	if got := rt.Sites()["111/s1"]; got != "Store 0001" {
		t.Errorf("site name = %q after a tenant with no sites, want the other tenant's kept", got)
	}
	rt.ClearSites()
	if len(rt.Sites()) != 0 {
		t.Errorf("sites = %v after a clear", rt.Sites())
	}
}

func TestALineTypedIntoASessionIsRanked(t *testing.T) {
	rt := deviceRuntime(t)
	run(t, rt, "list.select-matches")
	if err := rt.Exec(&Call{Ctx: context.Background()}, "session.open"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Sessions().CloseAll)
	if err := rt.Exec(&Call{Ctx: context.Background(), Args: map[string]string{"text": "dump overlay status"}}, "session.insert"); err != nil {
		t.Fatal(err)
	}
	s := rt.Sessions().Active()
	deadline, since := time.Now().Add(10*time.Second), time.Time{}
	for {
		if s.Line() != "dump overlay status" {
			since = time.Time{}
		} else if since.IsZero() {
			since = time.Now()
		} else if time.Since(since) > 100*time.Millisecond {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the echo never settled, the line reads %q", s.Line())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !rt.RecordLine(s) {
		t.Fatalf("nothing recorded from the line %q", s.Line())
	}
	for _, u := range rt.Ranked("", "overlay") {
		if u.Command == "dump overlay status" {
			return
		}
	}
	t.Errorf("ranked = %v, want the typed line in the history", rt.Ranked("", "overlay"))
}
