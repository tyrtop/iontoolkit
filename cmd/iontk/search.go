package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"golang.org/x/term"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/output"
	"tyrtop.com/iontk/internal/sase"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/saseauth"
	"tyrtop.com/iontk/internal/search"
	"tyrtop.com/iontk/internal/tui"
	"tyrtop.com/iontk/internal/ui"
)

const (
	searchTable = "table"
	searchIDs   = "ids"
	searchJSON  = "json"
)

var searchFormats = []string{searchTable, searchIDs, searchJSON}

type hit struct {
	element api.Element
	tenant  config.Tenant
	record  search.Record
}

func runSearch(ctx context.Context) error {
	if flagElement != "" || flagElementsPath != "" {
		return fmt.Errorf("--search selects the elements itself; drop --element and --elements-path")
	}

	var format string
	var err error
	if len(flagCommands) == 0 {
		if format, err = searchFormat(); err != nil {
			return err
		}
	}

	st := app.OpenStore()
	if st == nil {
		return fmt.Errorf("--search needs the config store, which could not be opened")
	}
	app.ApplyDisplaySettings(st, nil)
	if err := ui.ThemesErr(); err != "" {
		fmt.Fprintln(os.Stderr, "themes: "+err)
	}

	tenants, err := searchScope(st)
	if err != nil {
		return err
	}

	fl := newFleet(st, os.Stdin, os.Stderr, showProgress(), canSignIn())
	hits, err := fl.find(ctx, tenants, search.Parse(flagSearch))
	if err != nil {
		return err
	}
	if len(hits) == 0 {
		return fmt.Errorf("no elements match %q", flagSearch)
	}

	if len(flagCommands) == 0 {
		return printHits(os.Stdout, hits, format)
	}

	if err := confirmRun(os.Stdin, os.Stderr, term.IsTerminal(int(os.Stdin.Fd())), hits); err != nil {
		return err
	}
	sel, err := fl.selection(ctx, hits)
	if err != nil {
		return err
	}
	return runBatch(ctx, sel)
}

func canSignIn() bool {
	return !flagNoLogin && term.IsTerminal(int(os.Stderr.Fd()))
}

func showProgress() bool {
	return !flagNoProgress && term.IsTerminal(int(os.Stderr.Fd()))
}

func searchFormat() (string, error) {
	switch f := strings.ToLower(strings.TrimSpace(flagFormat)); f {
	case "":
		return searchTable, nil
	case searchTable, searchIDs, searchJSON:
		return f, nil
	default:
		want := strings.Join(searchFormats, ", ")
		if _, err := output.NormalizeFormat(f); err == nil {
			return "", fmt.Errorf("--format %s formats command results, not a list of elements; --search prints %s", f, want)
		}
		return "", fmt.Errorf("unknown --format %q: --search prints %s", flagFormat, want)
	}
}

func searchScope(st *config.Store) ([]config.Tenant, error) {
	cfg, err := st.Config()
	if err != nil {
		return nil, err
	}

	if flagTenant != "" {
		t, ok := cfg.Tenant(flagTenant)
		if !ok {
			return nil, fmt.Errorf("no saved tenant named %q; %s", flagTenant, app.TenantChoices(cfg))
		}
		return []config.Tenant{t}, nil
	}

	scope := cfg.AddedTenants()
	if len(scope) == 0 {
		t, ok := app.ResolveTenant(cfg, "")
		if !ok {
			return nil, fmt.Errorf("no tenants to search: %s", app.TenantChoices(cfg))
		}
		scope = []config.Tenant{t}
	}

	if tsg := app.Defaults.TSGID; tsg != "" {
		var kept []config.Tenant
		for _, t := range scope {
			if t.TSGID == tsg {
				kept = append(kept, t)
			}
		}
		if len(kept) == 0 {
			return nil, fmt.Errorf("--tsg %s is not in the working set (%s)", tsg, tenantNames(scope))
		}
		scope = kept
	}
	return scope, nil
}

func tenantNames(ts []config.Tenant) string {
	names := make([]string, 0, len(ts))
	for _, t := range ts {
		names = append(names, t.Name)
	}
	return strings.Join(names, ", ")
}

type fleet struct {
	store       *config.Store
	log         io.Writer
	progress    bool
	interactive bool
	in          io.Reader

	mu      sync.Mutex
	clients map[string]*api.Client
}

func newFleet(st *config.Store, in io.Reader, log io.Writer, progress, interactive bool) *fleet {
	return &fleet{
		store: st, in: in, log: log, progress: progress, interactive: interactive,
		clients: map[string]*api.Client{},
	}
}

type loaded struct {
	tenant config.Tenant
	els    []api.Element
	sites  search.SiteMap
	note   string
	err    error
	stale  error
}

func (l loaded) wantsSignIn() bool {
	return errors.Is(l.err, saseauth.ErrInteractiveRequired) ||
		errors.Is(l.stale, saseauth.ErrInteractiveRequired)
}

func (fl *fleet) find(ctx context.Context, tenants []config.Tenant, q search.Query) ([]hit, error) {
	results := fl.load(ctx, tenants)

	var again []config.Tenant
	for _, r := range results {
		if r.wantsSignIn() {
			again = append(again, r.tenant)
		}
	}
	if len(again) > 0 && fl.renew(ctx, again) {
		for _, r := range fl.load(ctx, again) {
			for i := range results {
				if results[i].tenant.TSGID == r.tenant.TSGID {
					results[i] = r
				}
			}
		}
	}

	var hits []hit
	var failed []string
	for _, r := range results {
		if r.err != nil {
			failed = append(failed, r.tenant.Name+": "+app.ShortAuthError(r.err))
			continue
		}
		for _, el := range r.els {
			rec := search.Of(el, r.tenant.TSGID, r.tenant.Name, r.sites)
			if q.Match(rec) {
				hits = append(hits, hit{element: el, tenant: r.tenant, record: rec})
			}
		}
	}
	if len(failed) == len(tenants) {
		return nil, fmt.Errorf("no tenant could be searched:\n  %s", strings.Join(failed, "\n  "))
	}

	sort.Slice(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.record.Name != b.record.Name {
			return a.record.Name < b.record.Name
		}
		return a.tenant.TSGID < b.tenant.TSGID
	})
	return hits, nil
}

func (fl *fleet) load(ctx context.Context, tenants []config.Tenant) []loaded {
	results := make([]loaded, len(tenants))
	events := make(chan tui.TenantLoad, 2*len(tenants))

	var wg sync.WaitGroup
	for i, t := range tenants {
		wg.Add(1)
		go func() {
			defer wg.Done()
			events <- tui.TenantLoad{Tenant: t.Name}
			r := fl.elements(ctx, t)
			results[i] = r
			events <- tui.TenantLoad{
				Tenant: t.Name, Count: len(r.els), Note: r.note,
				Err: r.err, Warn: r.stale != nil, Done: true,
			}
		}()
	}
	go func() {
		wg.Wait()
		close(events)
	}()

	if fl.progress {
		tui.DrainTenants(ctx, events, len(tenants))
	} else {
		fl.report(events)
	}
	return results
}

func (fl *fleet) renew(ctx context.Context, needed []config.Tenant) bool {
	if !fl.interactive {
		fmt.Fprintf(fl.log, "%s need a credential — run `iontk login`, or this on a terminal\n",
			tenantNames(needed))
		return false
	}

	ok, err := askYesNo(fl.in, fl.log, fmt.Sprintf(
		"the sign-in for %s has expired; refresh it now?", tenantNames(needed)))
	if err != nil {
		fmt.Fprintln(fl.log, err)
		return false
	}
	if !ok {
		return false
	}

	if _, err := app.SignIn(ctx, fl.store, fl.log); err != nil {
		fmt.Fprintln(fl.log, "sign-in failed:", app.ShortAuthError(err))
		return false
	}
	if err := app.WarmTenants(ctx, fl.store, needed, fl.log); err != nil {
		fmt.Fprintln(fl.log, err)
		return false
	}
	return true
}

func (fl *fleet) report(events <-chan tui.TenantLoad) {
	for ev := range events {
		switch {
		case !ev.Done:
		case ev.Err != nil:
			fmt.Fprintf(fl.log, "%s: unreachable, %s\n", ev.Tenant, app.ShortAuthError(ev.Err))
		default:
			fmt.Fprintf(fl.log, "%s: %d elements %s\n", ev.Tenant, ev.Count, ev.Note)
		}
	}
}

func (fl *fleet) elements(ctx context.Context, t config.Tenant) loaded {
	out := loaded{tenant: t}
	cached, cerr := fl.store.Cache(t.TSGID)
	if cerr != nil {
		cached = nil
	}
	if !flagRefresh && cached.Usable() && !cached.Stale(config.CacheFresh) {
		out.els, out.sites = cached.Elements, search.SiteMap(cached.Sites)
		out.note = fmt.Sprintf("from the cache, %s old", age(cached.Age()))
		return out
	}

	els, sites, err := fl.fetch(ctx, t, cached)
	if err != nil {
		if cached.Usable() {
			out.els, out.sites = cached.Elements, search.SiteMap(cached.Sites)
			out.note = fmt.Sprintf("from a stale cache %s old (%s)", age(cached.Age()), app.ShortAuthError(err))
			out.stale = err
			return out
		}
		out.err = err
		return out
	}
	out.els, out.sites, out.note = els, sites, "refreshed"
	return out
}

func (fl *fleet) fetch(ctx context.Context, t config.Tenant, cached *config.ElementCache) ([]api.Element, search.SiteMap, error) {
	c, err := fl.client(ctx, t)
	if err != nil {
		return nil, nil, err
	}

	els, err := c.ListElements(ctx)
	if err != nil {
		return nil, nil, err
	}

	sites := search.SiteMap{}
	ss, siteErr := c.ListSites(ctx)
	for _, s := range ss {
		sites[t.TSGID+"/"+s.ID] = s.Name
	}
	if siteErr != nil && cached != nil {
		sites = search.SiteMap(cached.Sites)
	}

	_ = fl.store.SaveCache(&config.ElementCache{
		TSGID: t.TSGID, FetchedAt: time.Now(), Elements: els, Sites: sites,
	})
	return els, sites, nil
}

func (fl *fleet) client(ctx context.Context, t config.Tenant) (*api.Client, error) {
	fl.mu.Lock()
	defer fl.mu.Unlock()
	if c, ok := fl.clients[t.TSGID]; ok {
		return c, nil
	}
	f := fl.flags(t)
	if err := f.ValidateCreds(); err != nil {
		return nil, err
	}
	c, err := app.NewClient(ctx, fl.store, f, io.Discard)
	if err != nil {
		return nil, err
	}
	fl.clients[t.TSGID] = c
	return c, nil
}

func (fl *fleet) flags(t config.Tenant) app.Flags {
	return app.TenantFlags(fl.store, t, os.Getenv("ION_USER"))
}

func age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "under a minute"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

func printHits(w io.Writer, hits []hit, format string) error {
	switch format {
	case searchIDs:
		for _, h := range hits {
			fmt.Fprintln(w, h.element.ID)
		}
		return nil

	case searchJSON:
		type out struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			Site      string `json:"site"`
			Model     string `json:"model"`
			Software  string `json:"software"`
			Connected bool   `json:"connected"`
			Tenant    string `json:"tenant"`
			TSGID     string `json:"tsg_id"`
		}
		rows := make([]out, 0, len(hits))
		for _, h := range hits {
			rows = append(rows, out{
				ID: h.element.ID, Name: h.record.Name, Site: h.record.SiteLabel(),
				Model: h.element.Model, Software: h.element.Software,
				Connected: h.element.Connected, Tenant: h.tenant.Name, TSGID: h.tenant.TSGID,
			})
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	header := "ELEMENT\tSITE\tMODEL\tSTATE\tID"
	if multiTenant(hits) {
		header += "\tTENANT"
	}
	fmt.Fprintln(tw, header)
	for _, h := range hits {
		row := strings.Join([]string{
			label(h), h.record.SiteLabel(), dash(h.element.Model), h.record.State(), h.element.ID,
		}, "\t")
		if multiTenant(hits) {
			row += "\t" + h.tenant.Name
		}
		fmt.Fprintln(tw, row)
	}
	return tw.Flush()
}

func label(h hit) string {
	if h.record.Name != "" {
		return h.record.Name
	}
	return h.element.ID
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func multiTenant(hits []hit) bool {
	for _, h := range hits[1:] {
		if h.tenant.TSGID != hits[0].tenant.TSGID {
			return true
		}
	}
	return false
}

func askYesNo(in io.Reader, w io.Writer, question string) (bool, error) {
	fmt.Fprintf(w, "%s [y/N] ", question)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return false, fmt.Errorf("read answer: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	}
	return false, nil
}

func confirmRun(in io.Reader, w io.Writer, tty bool, hits []hit) error {
	if flagNoValidate {
		return nil
	}
	if !tty {
		return fmt.Errorf("%d elements match %q and stdin is not a terminal, so the confirmation cannot be answered; pass --no-validate to run anyway",
			len(hits), flagSearch)
	}

	cmds := app.SplitCommands(flagCommands)
	fmt.Fprintf(w, "\nrunning %s on %s:\n", plural(len(cmds), "command"), plural(len(hits), "element"))
	for _, c := range cmds {
		fmt.Fprintf(w, "  $ %s\n", c)
	}
	if err := printHits(w, hits, searchTable); err != nil {
		return err
	}

	ok, err := askYesNo(in, w, "continue?")
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("cancelled")
	}
	return nil
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func (fl *fleet) selection(ctx context.Context, hits []hit) (selection, error) {
	sel := selection{elements: make([]string, 0, len(hits))}
	seen := map[string]bool{}

	for _, h := range hits {
		c, err := fl.client(ctx, h.tenant)
		if err != nil {
			return selection{}, fmt.Errorf("tenant %s: %w", h.tenant.Name, err)
		}
		if !seen[h.tenant.TSGID] {
			seen[h.tenant.TSGID] = true
			sel.clients = append(sel.clients, c)
		}
		f := fl.flags(h.tenant)
		sel.elements = append(sel.elements, h.element.ID)
		sel.targets = append(sel.targets, sase.Target{
			ElementID: h.element.ID,
			Client:    c,
			User:      f.IONUsername,
			Pass:      f.IONPassword,
		})
	}
	return sel, nil
}
