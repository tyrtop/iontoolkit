package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
	"tyrtop.com/iontk/internal/sase/api"
)

var (
	flagElement       string
	flagElementsPath  string
	flagCommands      []string
	flagConcurrency   int
	flagElementTO     time.Duration
	flagSessionTO     time.Duration
	flagAttempts      int
	flagHTTPTimeout   time.Duration
	flagRPS           float64
	flagBurst         int
	flagRefreshMargin time.Duration
	flagVerbose       bool
	flagNoProgress    bool
	flagAllowWrites   bool
	flagTSG           string
)

var rootCmd = &cobra.Command{
	Use:   "iontk [flags]",
	Short: "API-driven ION CLI management for Prisma SD-WAN",
	Long: `iontk runs commands on Prisma SD-WAN ION appliances over the SASE API.

With --cmd it runs in batch mode: a JSON array on stdout, live progress on
stderr. With --element alone it attaches your terminal to that element's CLI.

Credentials come from the environment or a .env in the working directory:

  SCM_CLIENT_ID      service account client ID
  SCM_CLIENT_SECRET  service account client secret
  SCM_TSG_ID         tenant service group ID
  SCM_TOKEN          bearer token, overrides the service account
  ION_USER           CLI login, required for batch mode
  ION_PASS           CLI password, required for batch mode`,

	Example: `  iontk --element 1234567890                         interactive shell
  iontk -e 1234567890 -c "dump interface status 1"   batch, one element
  iontk -f sites.txt -c "dump device status" -n 25   batch, many elements`,

	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,

	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		_ = godotenv.Load()
	},

	RunE: func(cmd *cobra.Command, args []string) error {
		switch {
		case len(flagCommands) > 0:
			return runBatch(cmd.Context())
		case flagElement != "":
			return runShell(cmd.Context(), flagElement)
		case flagElementsPath != "":
			return fmt.Errorf("--elements-path needs --cmd; use --element for a single interactive session")
		default:
			return runTUI(cmd.Context())
		}
	},
}

func init() {
	pf := rootCmd.PersistentFlags()
	pf.StringVarP(&flagElement, "element", "e", "", "element ID of the target ION")
	pf.StringVarP(&flagElementsPath, "elements-path", "f", "", `file of element IDs, one per line ("-" reads stdin)`)
	pf.StringArrayVarP(&flagCommands, "cmd", "c", nil, "command to run; repeatable, or comma separated")
	pf.IntVarP(&flagConcurrency, "concurrency", "n", 10, "elements worked in parallel")
	pf.DurationVar(&flagElementTO, "element-timeout", 60*time.Second, "per-element deadline including the CLI session")
	pf.DurationVar(&flagSessionTO, "session-timeout", 20*time.Second, "deadline for a single login attempt")
	// Defaults to 1 to prevent executing config changes unintentionally.
	// Multiple attempts plus a write to the device can cause undesirable behavior.
	pf.IntVar(&flagAttempts, "attempts", 1, "login attempts before the element is dropped")
	pf.DurationVar(&flagHTTPTimeout, "http-timeout", 15*time.Second, "timeout for the element lookup")
	pf.Float64Var(&flagRPS, "rps", 5, "API requests per second")
	pf.IntVar(&flagBurst, "burst", 10, "burst allowance on top of --rps")
	pf.DurationVar(&flagRefreshMargin, "refresh-margin", 75*time.Second, "re-mint the service account token this long before it expires")
	pf.BoolVarP(&flagVerbose, "verbose", "v", false, "print request details, rate limit headers, attempt errors")
	pf.BoolVar(&flagNoProgress, "no-progress", false, "never draw the batch progress bar")
	pf.BoolVar(&flagAllowWrites, "allow-writes", false, "permit commands that are not read-only")
	pf.StringVar(&flagTSG, "tsg", "", "tenant service group ID (overrides SCM_TSG_ID)")

	rootCmd.AddCommand(runCmd, shellCmd, versionCmd)
}

func baseFlags() flags {
	f := flags{
		Token:          os.Getenv("SCM_TOKEN"),
		ClientID:       os.Getenv("SCM_CLIENT_ID"),
		ClientSecret:   os.Getenv("SCM_CLIENT_SECRET"),
		TSGID:          os.Getenv("SCM_TSG_ID"),
		IONUsername:    os.Getenv("ION_USER"),
		IONPassword:    os.Getenv("ION_PASS"),
		HTTPTimeout:    flagHTTPTimeout,
		Verbose:        flagVerbose,
		RPS:            flagRPS,
		Burst:          flagBurst,
		RefreshMargin:  flagRefreshMargin,
		Concurrency:    flagConcurrency,
		ElementTimeout: flagElementTO,
		SessionTimeout: flagSessionTO,
		Attempts:       flagAttempts,
	}
	if flagTSG != "" {
		f.TSGID = flagTSG
	}
	return f
}

func newClient(ctx context.Context, f flags, logw io.Writer) (*api.Client, error) {
	wsClient := &http.Client{Transport: &http.Transport{
		MaxConnsPerHost:     f.Concurrency,
		MaxIdleConnsPerHost: 50,
		TLSHandshakeTimeout: 10 * time.Second,
	}}
	httpClient := &http.Client{Timeout: f.HTTPTimeout}

	if f.Token != "" {
		fmt.Fprintln(logw, "using SCM_TOKEN")
		return api.NewClient(httpClient, wsClient, f.Token, f.Verbose, f.RPS, f.Burst), nil
	}
	if f.BrowserAuth {
		fmt.Fprintln(logw, "using the browser sign-in for this tenant")
		src := browserSource(f, httpClient, openStore())
		return api.NewTokenSourceClient(ctx, httpClient, wsClient, src, f.RefreshMargin, f.Verbose, f.RPS, f.Burst)
	}
	fmt.Fprintln(logw, "minting from service account")
	return api.NewServiceAccountClient(ctx, httpClient, wsClient,
		f.ClientID, f.ClientSecret, f.TSGID, f.RefreshMargin, f.Verbose, f.RPS, f.Burst)
}
