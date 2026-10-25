package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/logging"
	"tyrtop.com/iontk/internal/tui"
	"tyrtop.com/iontk/internal/workflow"
)

var (
	flagElement      string
	flagElementsPath string
	flagCommands     []string
	flagNoProgress   bool
	flagFormat       string
	flagOrient       string
	flagOut          string
	flagTenant       string
	flagFixtures     string
	flagDemo         bool
	flagSearch       string
	flagRefresh      bool
	flagNoValidate   bool
	flagNoLogin      bool
	flagViews        string
	flagColumns      string
	flagKeybinds     string
	flagActions      string
	flagThemes       string
)

func wantsTheTerminal(cmd *cobra.Command) bool {
	if cmd.Parent() != nil {
		return cmd.Name() == "shell"
	}
	return len(flagCommands) == 0 && flagSearch == ""
}

var rootCmd = &cobra.Command{
	Use:   "iontk [flags]",
	Short: "API-driven ION CLI management for Prisma SD-WAN",
	Long: `iontk runs commands on Prisma SD-WAN ION appliances over the SASE API.
With no command it opens the TUI.

The commands are iontk's own, the same ones the TUI's keys and menus run:
iontk data open is data.open there. A command's arguments are its flags, and
--name - reads that one from the next line of standard input, for a password
out of the shell's history, and --name @file reads it from a file. A command that writes is a dry run that lists
what it would send unless --doit is given.

  iontk batch run      commands across elements: a JSON array on stdout,
                       live progress on stderr
  iontk session open   your terminal attached to one element's CLI
  iontk fleet search   the elements a query matches, by name, site, model
                       or tenant
  iontk workflow       a workflow: a name in workflows/ in the config
                       directory, or a path to a workflow's folder

Credentials come from a tenant saved in iontk, or from the environment and a
.env in the working directory. --tenant picks a saved tenant outright; with no
--tenant these win over the last tenant used:

  SCM_CLIENT_ID      service account client ID
  SCM_CLIENT_SECRET  service account client secret
  SCM_TSG_ID         tenant service group ID
  SCM_TOKEN          bearer token, overrides the service account
  ION_USER           CLI login, required for batch mode
  ION_PASS           CLI password, required for batch mode

A saved tenant supplies its own client secret, browser sign-in and device
login from the keyring, so --cmd needs none of these once one is signed in.`,

	Example: `  iontk session open 1234567890                                interactive shell
  iontk batch run -e 1234567890 -c "dump interface status 1"   batch, one element
  iontk batch run -f sites.txt -c "dump device status" -n 25   batch, many elements
  iontk fleet search store-0100                                list matching elements
  iontk batch run --search "s5024 ion 1200" -c uptime          batch, searched elements
  iontk workflow site-report --site S0272                      dry run a workflow
  iontk workflow site-report --site S0272 --doit               send its writes
  iontk workflow ./site-report --site S0272                    one not installed`,

	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,

	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if logErr != nil {
			if app.LogLevel != "" {
				return logErr
			}
			fmt.Fprintln(cmd.ErrOrStderr(), logErr)
		}
		logging.L().Info("iontk starting", "version", buildVersion(),
			"command", cmd.CommandPath(), "args", strings.Join(os.Args[1:], " "))
		return nil
	},

	RunE: func(cmd *cobra.Command, args []string) error {
		if app.Debug && flagFixtures == "" && flagTenant != "" {
			return fmt.Errorf("--debug has no saved tenants, so --tenant %s cannot resolve", flagTenant)
		}
		was := func(old, now string) {
			fmt.Fprintf(cmd.ErrOrStderr(), "iontk with %s is deprecated, use `%s`\n", old, now)
		}
		switch {
		case flagSearch != "" && len(flagCommands) == 0:
			was("--search alone", "iontk fleet search")
			return runSearch(cmd.Context())
		case flagSearch != "":
			was("--search and --cmd", "iontk batch run --search")
			return runSearch(cmd.Context())
		case len(flagCommands) > 0:
			was("--cmd", "iontk batch run")
			sel, err := flagSelection()
			if err != nil {
				return err
			}
			return runBatch(cmd.Context(), sel)
		case flagElement != "":
			was("--element alone", "iontk session open")
			return runShell(cmd.Context(), flagElement)
		case flagElementsPath != "":
			return fmt.Errorf("--elements-path goes with iontk batch run")
		default:
			tui.Version = buildVersion()
			tui.Tenant = flagTenant
			tui.Demo = flagDemo
			return tui.Run(cmd.Context(), cliRuntime())
		}
	},
}

func init() {
	pf := rootCmd.PersistentFlags()
	pf.StringVarP(&flagElement, "element", "e", "", "element ID of the target ION")
	pf.StringVarP(&flagElementsPath, "elements-path", "f", "", `file of element IDs, one per line ("-" reads stdin)`)
	pf.StringVar(&flagSearch, "search", "", "select elements by a query over name, site, model, software, tenant and state, as the TUI filter does")
	pf.BoolVar(&flagRefresh, "refresh", false, "re-fetch the element list for --search instead of answering from a cache under 15 minutes old")
	pf.BoolVar(&flagNoValidate, "no-validate", false, "skip the confirmation that lists which elements a --search run would touch")
	pf.BoolVar(&flagNoValidate, "yes", false, "alias for --no-validate")
	pf.BoolVar(&flagNoLogin, "no-login", false, "never open a browser for --search; report the tenants that need one instead")
	pf.StringArrayVarP(&flagCommands, "cmd", "c", nil, "command to run; repeatable, or comma separated")
	pf.IntVarP(&app.Defaults.Concurrency, "concurrency", "n", app.Defaults.Concurrency, "elements, or runs of a workflow's --inputs, worked in parallel")
	pf.DurationVar(&app.Defaults.ElementTimeout, "element-timeout", app.Defaults.ElementTimeout, "per-element deadline including the CLI session")
	pf.DurationVar(&app.Defaults.SessionTimeout, "session-timeout", app.Defaults.SessionTimeout, "deadline for a single login attempt")
	pf.IntVar(&app.Defaults.DialAttempts, "dial-attempts", app.Defaults.DialAttempts, "times to dial an element before dropping it")
	pf.DurationVar(&app.Defaults.DialTimeout, "dial-timeout", app.Defaults.DialTimeout, "how long one dial may take")
	pf.IntVar(&app.Defaults.DialAttempts, "attempts", app.Defaults.DialAttempts, "deprecated alias for --dial-attempts")
	_ = pf.MarkDeprecated("attempts", "use --dial-attempts; the retry now re-dials rather than re-running the session")
	pf.StringVar(&app.Defaults.PagerOff, "pager-off", app.Defaults.PagerOff, "command sent after login to stop the device paging (empty sends nothing)")
	pf.StringArrayVar(&app.Defaults.PagerPrompts, "pager-prompt", app.Defaults.PagerPrompts, "line to answer with a space when the device pages; repeatable, and empty turns it off")
	pf.DurationVar(&app.Defaults.HTTPTimeout, "http-timeout", app.Defaults.HTTPTimeout, "timeout for the element lookup")
	pf.Float64Var(&app.Defaults.RPS, "rps", app.Defaults.RPS, "API requests per second")
	pf.IntVar(&app.Defaults.Burst, "burst", app.Defaults.Burst, "burst allowance on top of --rps")
	pf.DurationVar(&app.Defaults.RefreshMargin, "refresh-margin", app.Defaults.RefreshMargin, "re-mint the service account token this long before it expires")
	pf.BoolVarP(&app.Defaults.Verbose, "verbose", "v", app.Defaults.Verbose, "print request details, rate limit headers, attempt errors")
	pf.BoolVar(&flagNoProgress, "no-progress", false, "never draw the batch progress bar")
	pf.BoolVar(&app.Defaults.AllowWrites, "allow-writes", app.Defaults.AllowWrites, "permit commands that are not read-only")
	pf.StringVar(&flagTenant, "tenant", "", "saved tenant to run as, by name (overrides the environment)")
	pf.BoolVar(&app.Debug, "debug", false, "run against a throwaway config with no saved tenants and secrets held only in memory")
	pf.StringVar(&flagFixtures, "fixtures", "", "YAML file of fake tenants, elements and CLI responses to run against instead of real hardware")
	pf.BoolVar(&flagDemo, "demo", false, "open on the fake fleet iontk ships (or fixtures.yaml in the config directory), signed in, with the fleet loaded behind the menu — the same fleet Settings → Debug swaps in")
	pf.StringVar(&flagViews, "views", "", "folder of screen files (or a single file of rich-data views) to use instead of views/ in the config directory")
	pf.StringVar(&flagColumns, "columns", "", "YAML file naming the element list's columns and their order, instead of views/elements.yaml in the config directory")
	pf.StringVar(&flagKeybinds, "keybinds", "", "YAML file of key bindings and actions, instead of keybinds.yaml in the config directory")
	pf.StringVar(&flagActions, "actions", "", "folder of Lua actions, instead of actions/ in the config directory")
	pf.StringVar(&app.TutorialFile, "tutorial", "", "tutorial YAML file, or a folder of them, to use instead of tutorials/ in the config directory")
	pf.StringVar(&flagThemes, "themes", "", "YAML file of color schemes to add to the built-in ones, instead of themes.yaml in the config directory")
	pf.StringVar(&app.LogFile, "log", "", "write this run's log here instead of a stamped file under logs/ in the config directory")
	pf.StringVar(&app.LogLevel, "log-level", "", "how much to log: "+strings.Join(logging.LevelNames(), ", ")+" (default info, or ION_LOG_LEVEL)")
	pf.StringVar(&app.Defaults.TSGID, "tsg", "", "tenant service group ID (overrides SCM_TSG_ID)")
	pf.StringVar(&flagFormat, "format", "", `batch output format: "json", "jsonl", "text", "table" or "csv" (default from settings, else json)`)
	pf.StringVar(&flagOrient, "orientation", "", `row order for "table" and "csv": "ion" groups each element's commands, "command" puts one command across the fleet together (default from settings, else ion)`)
	pf.StringVar(&flagOut, "out", "", `write batch output to this file ("-" is stdout; default from settings)`)

	inspectCmd.Flags().BoolVar(&flagInspectRaw, "raw", false, "print each response body as it arrived, keyed by URL")

	rootCmd.AddCommand(runCmd, shellCmd, inspectCmd, loginCmd, versionCmd, genFlagDocsCmd)
	addOwn(rootCmd, "batch run", newBatchRunCmd("run"))
	addOwn(rootCmd, "session open", newSessionOpenCmd("open"))
	addOwn(rootCmd, "fleet search", fleetSearchCmd)
	addOwn(rootCmd, "write", writeCmd)
}

func prepare(argv []string) error {
	fs := pflag.NewFlagSet("prepare", pflag.ContinueOnError)
	fs.ParseErrorsWhitelist.UnknownFlags = true
	fs.Usage = func() {}
	fs.AddFlagSet(rootCmd.PersistentFlags())
	fs.BoolP("help", "h", false, "")
	_ = fs.Parse(argv)
	fs.Visit(func(f *pflag.Flag) {
		if sv, ok := f.Value.(pflag.SliceValue); ok {
			_ = sv.Replace(nil)
		}
	})

	_ = godotenv.Load()
	app.ThemesFile = flagThemes
	app.ColumnsFile = flagColumns
	app.ViewsFolder = flagViews
	app.ActionsFolder = flagActions
	app.FixturesFile = flagFixtures
	app.KeybindsFile = flagKeybinds
	if err := workflowByPath(fs.Args()); err != nil {
		return err
	}
	if err := loadFixtures(os.Stderr); err != nil {
		return err
	}
	startLogging(fs.Args())
	return nil
}

var logErr error

func startLogging(words []string) {
	terminal := wantsTheTerminal(rootCmd)
	if len(words) > 0 {
		c, _, err := rootCmd.Find(words)
		terminal = err == nil && c != rootCmd && wantsTheTerminal(c)
	}
	var toStderr io.Writer
	if app.LogLevelAsked() != "" && !terminal {
		toStderr = os.Stderr
	}
	logErr = app.StartLogging(app.OpenStore(), toStderr)
}

var workflowAliases = map[string]string{}

func workflowByPath(words []string) error {
	if len(words) < 2 || words[0] != "workflow" {
		return nil
	}
	if p := words[1]; p != "." && p != ".." && !strings.ContainsRune(p, filepath.Separator) {
		return nil
	}
	w, err := workflow.LoadOne(words[1])
	if err != nil {
		return fmt.Errorf("workflow %s: %w", words[1], err)
	}
	app.WorkflowPaths = append(app.WorkflowPaths, words[1])
	workflowAliases[w.Name] = words[1]
	return nil
}
