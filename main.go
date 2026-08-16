package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/joho/godotenv"
)

//strata expects a browser UA in the header. 
const browserUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36"

func main() {
	_ = godotenv.Load()

	element := flag.String("element", "", "element_id to query")
	timeout := flag.Duration("timeout", 15*time.Second, "HTTP timeout")
	cmd := flag.String("cmd", "", "run a command and exit")
	verbose := flag.Bool("v", false, "print request details")
	flag.Parse()
	
	var elements []string
	if *element != "" {
		elements = append(elements, *element)
	}
	
	cfg := Config {
		Token: os.Getenv("SCM_TOKEN"),
		IONUsername: os.Getenv("ION_USER"),
		IONPassword: os.Getenv("ION_PASS"),
		Command: *cmd,
		Elements: elements,
		Timeout: *timeout, 
		Verbose: *verbose,
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	client := &http.Client{Timeout: cfg.Timeout}
	ctx := context.Background()

	if cfg.Command == "" {
		if err := interactiveCLI(ctx, client, cfg, cfg.Elements[0]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	var results []Result
	failed := 0

	for _, eid := range cfg.Elements {
		res, err := runElement(ctx, client, cfg, eid)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			results = append(results, Result{ElementID: eid, Error: err.Error()})
			failed++
			continue
		}
		results = append(results, res)
	}

	json.NewEncoder(os.Stdout).Encode(results)

	if failed > 0 {
		os.Exit(1)
	}	
}
