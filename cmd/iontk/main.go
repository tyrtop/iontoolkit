package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"tyrtop.com/iontk/internal/sase"
	"tyrtop.com/iontk/internal/sase/api"
)

func main() {
	_ = godotenv.Load()

	element := flag.String("element", "", "element_id to query")
	httpTimeout := flag.Duration("http-timeout", 15*time.Second, "HTTP timeout")
	cmd := flag.String("cmd", "", "run a command and exit")
	verbose := flag.Bool("v", false, "print request details")
	elementTimeout := flag.Duration("element-timeout", 60*time.Second, "per-element deadline including the CLI session")
	concurrency := flag.Int("concurrency", 10, "sets the number of concurrent http sesssions to the Strata API")
	elementsFile := flag.String("elements-path", "", "sets the path to the elements file")
	rps := flag.Float64("rps", 5, "sets amount amount of requests per second")
	burst := flag.Int("burst", 10, "sets api burst")
	sessionTimeout := flag.Duration("session-timeout", 20*time.Second, "sets the session timeout, referring to retries for a hung ION login")
	//this is set to 1 to prevent executing config changes unintentially. Multiple attempts and a write to the device can cause undersirable behavior.
	attempts := flag.Int("attempts", 1, "sets the number of attempts to connect to the CLI before dropping the session")
	refreshMargin := flag.Duration("refresh-margin", 75*time.Second, "re-mint the service account token this long before it expires")
	flag.Parse()

	var elements []string
	if *element != "" {
		elements = append(elements, *element)
	}
	if *elementsFile != "" {
		loaded, err := loadElements(*elementsFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		elements = append(elements, loaded...)
	}

	var commands []string
	if *cmd != "" {
		for _, c := range strings.Split(*cmd, ",") {
			c = strings.TrimSpace(c)
			if c != "" {
				commands = append(commands, c)
			}
		}
	}

	f := flags{
		Token:          os.Getenv("SCM_TOKEN"),
		IONUsername:    os.Getenv("ION_USER"),
		IONPassword:    os.Getenv("ION_PASS"),
		Commands:       commands,
		Elements:       elements,
		HTTPTimeout:    *httpTimeout,
		Verbose:        *verbose,
		ElementTimeout: *elementTimeout,
		Concurrency:    *concurrency,
		RPS:            *rps,
		Burst:          *burst,
		SessionTimeout: *sessionTimeout,
		Attempts:       *attempts,
		ClientID:       os.Getenv("SCM_CLIENT_ID"),
		ClientSecret:   os.Getenv("SCM_CLIENT_SECRET"),
		TSGID:          os.Getenv("SCM_TSG_ID"),
		RefreshMargin:  *refreshMargin,
	}
	if err := f.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	opts := f.options()
	if err := opts.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	wsClient := &http.Client{Transport: &http.Transport{
		MaxConnsPerHost:     f.Concurrency,
		MaxIdleConnsPerHost: 50,
		TLSHandshakeTimeout: 10 * time.Second,
	}}

	httpClient := &http.Client{Timeout: f.HTTPTimeout}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	var client *api.Client
	if f.Token != "" {
		fmt.Fprintln(os.Stderr, "using SCM_TOKEN")
		client = api.NewClient(httpClient, wsClient, f.Token, f.Verbose, f.RPS, f.Burst)
	} else {
		fmt.Fprintln(os.Stderr, "minting from service account")
		c, err := api.NewServiceAccountClient(ctx, httpClient, wsClient, f.ClientID, f.ClientSecret, f.TSGID, f.RefreshMargin, f.Verbose, f.RPS, f.Burst)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		client = c
	}

	if len(f.Commands) == 0 {
		if err := interactiveCLI(ctx, client, f.Verbose, f.Elements[0]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	results := sase.Run(ctx, client, opts, f.Elements)

	failed := 0
	for _, r := range results {
		if r.Error != "" {
			failed++
		}
	}

	json.NewEncoder(os.Stdout).Encode(results)

	if failed > 0 {
		os.Exit(1)
	}
}
