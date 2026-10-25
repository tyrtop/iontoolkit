package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"golang.org/x/term"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/output"
	"tyrtop.com/iontk/internal/runtime"
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

	rt := cliRuntime()
	st := rt.Store()
	if st == nil {
		return fmt.Errorf("--search needs the config store, which could not be opened")
	}
	app.ApplyDisplaySettings(st, nil)
	if err := ui.ThemesErr(); err != "" {
		fmt.Fprintln(os.Stderr, "themes: "+err)
	}

	tenants, err := rt.SearchScope(flagTenant, app.Defaults.TSGID)
	if err != nil {
		if cfg := rt.Config(); cfg != nil && flagTenant != "" {
			return fmt.Errorf("%w; %s", err, app.TenantChoices(cfg))
		}
		return err
	}

	q := search.Parse(flagSearch)
	found := find(ctx, rt, tenants, q, flagRefresh, os.Stderr)
	if len(found.SignIn) > 0 && renew(ctx, rt, found.SignIn, os.Stdin, os.Stderr, canSignIn()) {
		found = find(ctx, rt, tenants, q, false, os.Stderr)
	}
	if err := found.Err(); err != nil {
		return err
	}
	if len(found.Hits) == 0 {
		return fmt.Errorf("no elements match %q", flagSearch)
	}

	if len(flagCommands) == 0 {
		return printHits(os.Stdout, found.Hits, format)
	}

	if err := confirmRun(os.Stdin, os.Stderr, term.IsTerminal(int(os.Stdin.Fd())), found.Hits); err != nil {
		return err
	}
	ids := make([]string, 0, len(found.Hits))
	for _, h := range found.Hits {
		ids = append(ids, h.Element.ID)
	}
	return runBatch(ctx, selection{rt: rt, elements: ids, rows: found.Rows()})
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

func find(ctx context.Context, rt *runtime.Runtime, tenants []config.Tenant, q search.Query, refresh bool, log io.Writer) runtime.Found {
	events := make(chan runtime.TenantLoad, 2*len(tenants))
	done := make(chan struct{})
	go func() {
		defer close(done)
		if showProgress() {
			tui.DrainTenants(ctx, events, len(tenants))
			return
		}
		for ev := range events {
			switch {
			case !ev.Done:
			case ev.Err != nil:
				fmt.Fprintf(log, "%s: unreachable, %s\n", ev.Tenant, app.ShortAuthError(ev.Err))
			default:
				fmt.Fprintf(log, "%s: %d elements %s\n", ev.Tenant, ev.Count, ev.Note)
			}
		}
	}()
	found := rt.Search(ctx, tenants, q, refresh, events)
	close(events)
	<-done
	return found
}

func renew(ctx context.Context, rt *runtime.Runtime, needed []config.Tenant, in io.Reader, log io.Writer, interactive bool) bool {
	if !interactive {
		fmt.Fprintf(log, "%s need a credential — run `iontk auth login`, or this on a terminal\n",
			runtime.TenantNames(needed))
		return false
	}
	ok, err := askYesNo(in, log, fmt.Sprintf(
		"the sign-in for %s has expired; refresh it now?", runtime.TenantNames(needed)))
	if err != nil {
		fmt.Fprintln(log, err)
		return false
	}
	if !ok {
		return false
	}
	names := make([]string, len(needed))
	for i, t := range needed {
		names[i] = t.Name
	}
	err = rt.Exec(&runtime.Call{Ctx: ctx, Args: map[string]string{"tenants": strings.Join(names, ",")},
		Out: func(l string) { fmt.Fprintln(log, l) }}, "auth.login")
	if err != nil {
		fmt.Fprintln(log, "sign-in failed:", app.ShortAuthError(err))
		return false
	}
	return true
}

func printHits(w io.Writer, hits []runtime.Hit, format string) error {
	switch format {
	case searchIDs:
		for _, h := range hits {
			fmt.Fprintln(w, h.Element.ID)
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
				ID: h.Element.ID, Name: h.Record.Name, Site: h.Record.SiteLabel(),
				Model: h.Element.Model, Software: h.Element.Software,
				Connected: h.Element.Connected, Tenant: h.Tenant.Name, TSGID: h.Tenant.TSGID,
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
			label(h), h.Record.SiteLabel(), dash(h.Element.Model), h.Record.State(), h.Element.ID,
		}, "\t")
		if multiTenant(hits) {
			row += "\t" + h.Tenant.Name
		}
		fmt.Fprintln(tw, row)
	}
	return tw.Flush()
}

func label(h runtime.Hit) string {
	if h.Record.Name != "" {
		return h.Record.Name
	}
	return h.Element.ID
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func multiTenant(hits []runtime.Hit) bool {
	for _, h := range hits[1:] {
		if h.Tenant.TSGID != hits[0].Tenant.TSGID {
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

func confirmRun(in io.Reader, w io.Writer, tty bool, hits []runtime.Hit) error {
	if flagNoValidate {
		return nil
	}
	if !tty {
		return fmt.Errorf("%d elements match %q and stdin is not a terminal, so the confirmation cannot be answered; pass --no-validate to run anyway",
			len(hits), flagSearch)
	}

	cmds := runtime.SplitCommands(flagCommands...)
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

var fleetSearchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "List the elements a query matches, across the working set of tenants",
	Long: `Runs the TUI's filter over the elements of every tenant in the working
set, narrowed by --tenant or --tsg, and prints what matched. The query's
words must all match the name, site, model, software, tenant or state.

iontk batch run --search runs commands on what a query matches instead.`,
	Example: `  iontk fleet search store-0100
  iontk fleet search "s5024 ion 1200" --format json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(flagCommands) > 0 {
			return fmt.Errorf("fleet search only lists; iontk batch run --search %q runs commands on what it finds", args[0])
		}
		flagSearch = args[0]
		return runSearch(cmd.Context())
	},
}
