package main

import (
	"fmt"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/tui"
)

var (
	flagElement      string
	flagElementsPath string
	flagCommands     []string
	flagNoProgress   bool
	flagFormat       string
	flagOut          string
	flagTenant       string
	flagFixtures     string
)

var rootCmd = &cobra.Command{
	Use:   "iontk [flags]",
	Short: "API-driven ION CLI management for Prisma SD-WAN",
	Long: `iontk runs commands on Prisma SD-WAN ION appliances over the SASE API.

With --cmd it runs in batch mode: a JSON array on stdout, live progress on
stderr. With --element alone it attaches your terminal to that element's CLI.

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

	Example: `  iontk --element 1234567890                         interactive shell
  iontk -e 1234567890 -c "dump interface status 1"   batch, one element
  iontk -f sites.txt -c "dump device status" -n 25   batch, many elements`,

	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,

	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		_ = godotenv.Load()
		return loadFixtures(cmd.ErrOrStderr())
	},

	RunE: func(cmd *cobra.Command, args []string) error {
		if app.Debug && flagFixtures == "" && flagTenant != "" {
			return fmt.Errorf("--debug has no saved tenants, so --tenant %s cannot resolve", flagTenant)
		}
		switch {
		case len(flagCommands) > 0:
			return runBatch(cmd.Context())
		case flagElement != "":
			return runShell(cmd.Context(), flagElement)
		case flagElementsPath != "":
			return fmt.Errorf("--elements-path needs --cmd; use --element for a single interactive session")
		default:
			tui.Version = buildVersion()
			tui.Tenant = flagTenant
			return tui.Run(cmd.Context())
		}
	},
}

func init() {
	pf := rootCmd.PersistentFlags()
	pf.StringVarP(&flagElement, "element", "e", "", "element ID of the target ION")
	pf.StringVarP(&flagElementsPath, "elements-path", "f", "", `file of element IDs, one per line ("-" reads stdin)`)
	pf.StringArrayVarP(&flagCommands, "cmd", "c", nil, "command to run; repeatable, or comma separated")
	pf.IntVarP(&app.Defaults.Concurrency, "concurrency", "n", app.Defaults.Concurrency, "elements worked in parallel")
	pf.DurationVar(&app.Defaults.ElementTimeout, "element-timeout", app.Defaults.ElementTimeout, "per-element deadline including the CLI session")
	pf.DurationVar(&app.Defaults.SessionTimeout, "session-timeout", app.Defaults.SessionTimeout, "deadline for a single login attempt")
	// Defaults to 1 to prevent executing config changes unintentionally.
	// The toolkit socket sometimes does not come up at all, so a dial is
	// retried. A session that connected is never re-run: its commands may
	// already have taken effect on the device.
	pf.IntVar(&app.Defaults.DialAttempts, "dial-attempts", app.Defaults.DialAttempts, "times to dial an element before dropping it")
	pf.DurationVar(&app.Defaults.DialTimeout, "dial-timeout", app.Defaults.DialTimeout, "how long one dial may take")
	pf.IntVar(&app.Defaults.DialAttempts, "attempts", app.Defaults.DialAttempts, "deprecated alias for --dial-attempts")
	_ = pf.MarkDeprecated("attempts", "use --dial-attempts; the retry now re-dials rather than re-running the session")
	// Better to turn paging off than to scrape the pager, where the device
	// allows it. The default works on 6.3.4; the flag is for trying another
	// firmware's syntax without a rebuild.
	pf.StringVar(&app.Defaults.PagerOff, "pager-off", app.Defaults.PagerOff, "command sent after login to stop the device paging (empty sends nothing)")
	// For firmware that pages anyway. The prompt text varies, so it's config
	// rather than a constant.
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
	pf.StringVar(&app.Defaults.TSGID, "tsg", "", "tenant service group ID (overrides SCM_TSG_ID)")
	pf.StringVar(&flagFormat, "format", "", `batch output format: "json", "jsonl", or "text" (default from settings, else json)`)
	pf.StringVar(&flagOut, "out", "", `write batch output to this file ("-" is stdout; default from settings)`)

	rootCmd.AddCommand(runCmd, shellCmd, versionCmd, genFlagDocsCmd)
}
