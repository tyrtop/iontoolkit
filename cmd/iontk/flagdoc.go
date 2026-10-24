package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var genFlagDocsCmd = &cobra.Command{
	Use:    "gen-flag-docs [file]",
	Short:  "Write the flag reference to a file, or to stdout",
	Hidden: true,
	Args:   cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var b strings.Builder
		if err := writeFlagDocs(&b, rootCmd); err != nil {
			return err
		}
		if len(args) == 1 {
			if dir := filepath.Dir(args[0]); dir != "." {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return fmt.Errorf("create %s: %w", dir, err)
				}
			}
			if err := os.WriteFile(args[0], []byte(b.String()), 0o644); err != nil {
				return fmt.Errorf("write %s: %w", args[0], err)
			}
			return nil
		}
		_, err := io.WriteString(cmd.OutOrStdout(), b.String())
		return err
	},
}

var flagGroups = []struct {
	name  string
	intro string
	flags []string
}{
	{"Targets", "Which elements `iontk batch run` and `iontk write` act on; exactly one is required. `--search` runs the TUI's filter over the working set of tenants, narrowed by `--tenant` or `--tsg`.", []string{
		"element", "elements-path", "search", "refresh", "no-validate", "yes", "no-login",
	}},
	{"Commands", "What `iontk batch run` runs on each element, and whether it may run commands that are not read-only.", []string{
		"cmd", "allow-writes",
	}},
	{"Credentials", "`--tenant` reads a saved tenant's credentials from the keyring; with no `--tenant`, the environment wins over the last tenant used. See [Environment](#environment).", []string{
		"tenant", "tsg",
	}},
	{"Connection", "Reaching the device. `--pager-off` holds the command that turns paging off, verified on ION 6.3.4; `--pager-prompt` answers the pager anyway, for firmware that ignores it.", []string{
		"pager-off", "pager-prompt", "dial-attempts", "dial-timeout", "http-timeout",
		"session-timeout", "element-timeout", "refresh-margin",
	}},
	{"Throughput", "Rate limits are the API's, not the device's.", []string{
		"concurrency", "rps", "burst",
	}},
	{"Output", "`--orientation` is read by `table` and `csv` only; the other formats have no rows to sort.", []string{
		"format", "orientation", "out", "no-progress", "verbose",
	}},
	{"Testing", "Running without touching saved state. `--debug` redirects the config root and keeps secrets in memory; `--fixtures` does the same and seeds fake tenants, elements and CLI sessions from a YAML file. `--demo` is `--fixtures` with the fleet iontk ships: two tenants, a hundred and six elements, HA pairs, a shell that answers everything, and the fleet already loaded when the menu comes up. `Settings → Debug` swaps the same fleet in mid-run, and `alt+e` there edits it.", []string{
		"debug", "fixtures", "demo",
	}},
	{"Screens", "The screens iontk draws from a file live in `views/` in the config directory, one file per screen, each saying what it is with a `kind:` line -- `list` for the element list's columns, `data` for a rich-data page, `menu` for the main menu. `--views` points at a folder of them, or at a single file of rich-data views, which is also how a `--fixtures` run gets screens of its own. The older single files are still read and the folder wins over them; `--columns` points at one that names the element list's columns. Inside iontk, `alt+e` opens the file the page you are on is drawn from.", []string{
		"views",
		"columns",
	}},
	{"Keys", "A key names an action, and the action says what to do. The ones iontk ships are bound already; `keybinds.yaml` in the config directory rebinds any of them, `none` takes a key back, and a screen's own file binds a key for that screen alone. You can write an action in the same file as a sequence of verbs: `do` runs another action, `goto` goes to a place, `set` changes a setting, `status` writes a line. Bindings, menu rows and tutorial pages all name actions, so any of them can run yours. `--keybinds` points somewhere else.\n\nFor an action that has to branch on what a read returned, or loop until something comes back, write Lua instead -- one file per action in `actions/` in the config directory, and `--actions` points elsewhere. A script gets one table and nothing else: no io, no os, no require, no sockets. It asks for work through the same verbs (`ctx.run` for any of iontk's commands, `ctx.status`, `ctx.set`, `ctx.goto`, `ctx.export`, `ctx.budget`), so the rate limit, the retries, the logging and the redaction stay iontk's. A binding does not care which kind of action it names.", []string{
		"keybinds",
		"actions",
	}},
	{"Logging", "Every run writes a JSON log under `logs/` in the config directory, and the last ten are kept. A token or a password goes in as a fingerprint, the last four characters and a length, which tells two of them apart without carrying either. Ask for a level and a headless run gets the same records on stderr as text, next to the lines it always printed.", []string{
		"log", "log-level",
	}},
	{"Tutorial", "What the TUI offers on a first run, and what `Settings → Tutorial` lists after that. The pages are YAML, one file per tutorial in `tutorials/` in the config directory, and writing one of your own takes the place of the pages iontk ships. `--tutorial` takes a file or a folder and points somewhere else again. It runs against a fake tenant with nothing on the other end of the wire, so a page can teach any key.", []string{
		"tutorial",
	}},
	{"Appearance", "Color schemes are YAML too: `--themes` points at a file whose schemes are added to the built-in ones, or replace one of the same name, instead of `themes.yaml` in the config directory. The same escape hatch as `--views`, for the same reason.", []string{
		"themes",
	}},
}

func writeFlagDocs(w io.Writer, root *cobra.Command) error {
	grouped, err := groupFlags(root)
	if err != nil {
		return err
	}

	var b strings.Builder
	b.WriteString("# Flags\n\n")
	b.WriteString("Generated by `go generate ./...` — edit `cmd/iontk/flagdoc.go`, not this file.\n\n")
	b.WriteString("Every flag below is global: `iontk` takes them directly, and so does\n")
	b.WriteString("every subcommand.\n")

	for _, g := range flagGroups {
		flags := grouped[g.name]
		if len(flags) == 0 {
			continue
		}
		b.WriteString("\n## " + g.name + "\n\n")
		if g.intro != "" {
			b.WriteString(g.intro + "\n\n")
		}
		writeFlagTable(&b, flags)
	}

	if dep := grouped["Deprecated"]; len(dep) > 0 {
		b.WriteString("\n## Deprecated\n\nStill accepted, with a warning.\n\n")
		b.WriteString("| Flag | Note |\n|---|---|\n")
		for _, f := range dep {
			fmt.Fprintf(&b, "| `--%s` | %s |\n", f.Name, f.Deprecated)
		}
	}

	b.WriteString("\n## Environment\n\n")
	b.WriteString("Read at startup, and overridden by the flags above. A `.env` in the working\n")
	b.WriteString("directory is loaded first.\n\n")
	b.WriteString("| Variable | Purpose |\n|---|---|\n")
	for _, e := range [][2]string{
		{"SCM_CLIENT_ID", "service account client ID"},
		{"SCM_CLIENT_SECRET", "service account client secret"},
		{"SCM_TSG_ID", "tenant service group ID; `--tsg` wins"},
		{"SCM_TOKEN", "bearer token, overriding the service account entirely"},
		{"ION_USER", "device CLI login, required with `--cmd`"},
		{"ION_PASS", "device CLI password, required with `--cmd`"},
		{"ION_LOG_LEVEL", "log level; `--log-level` wins"},
	} {
		fmt.Fprintf(&b, "| `%s` | %s |\n", e[0], e[1])
	}

	b.WriteString("\n## Subcommands\n\n| Command | Purpose |\n|---|---|\n")
	for _, c := range root.Commands() {
		if c.Hidden || c.Deprecated != "" || c.Name() == "help" || c.Name() == "completion" {
			continue
		}
		fmt.Fprintf(&b, "| `iontk %s` | %s |\n", c.Name(), c.Short)
	}

	_, err = io.WriteString(w, b.String())
	return err
}

func writeFlagTable(b *strings.Builder, flags []*pflag.Flag) {
	b.WriteString("| Flag | Type | Default | Purpose |\n|---|---|---|---|\n")
	for _, f := range flags {
		name := "`--" + f.Name + "`"
		if f.Shorthand != "" {
			name += ", `-" + f.Shorthand + "`"
		}
		def := "—"
		if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "[]" {
			def = "`" + f.DefValue + "`"
		}
		fmt.Fprintf(b, "| %s | %s | %s | %s |\n", name, f.Value.Type(), def, f.Usage)
	}
}

func groupFlags(root *cobra.Command) (map[string][]*pflag.Flag, error) {
	want := map[string]string{}
	for _, g := range flagGroups {
		for _, name := range g.flags {
			want[name] = g.name
		}
	}

	out := map[string][]*pflag.Flag{}
	var ungrouped []string

	visit := func(f *pflag.Flag) {
		switch {
		case f.Deprecated != "":
			out["Deprecated"] = append(out["Deprecated"], f)
		case want[f.Name] != "":
			out[want[f.Name]] = append(out[want[f.Name]], f)
		default:
			ungrouped = append(ungrouped, f.Name)
		}
	}
	root.PersistentFlags().VisitAll(visit)
	root.LocalNonPersistentFlags().VisitAll(visit)

	if len(ungrouped) > 0 {
		sort.Strings(ungrouped)
		return nil, fmt.Errorf("flags in no group, add them to flagGroups in cmd/iontk/flagdoc.go: %s",
			strings.Join(ungrouped, ", "))
	}

	for _, g := range flagGroups {
		rank := map[string]int{}
		for i, name := range g.flags {
			rank[name] = i
		}
		sort.SliceStable(out[g.name], func(i, j int) bool {
			return rank[out[g.name][i].Name] < rank[out[g.name][j].Name]
		})
	}
	return out, nil
}
