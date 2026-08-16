package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"
	"os/signal"
	"sync"

	"github.com/joho/godotenv"
)

//strata expects a browser UA in the header. 
const browserUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36"

func main() {
	_ = godotenv.Load()

	element := flag.String("element", "", "element_id to query")
	httpTimeout := flag.Duration("http-timeout", 15*time.Second, "HTTP timeout")
	cmd := flag.String("cmd", "", "run a command and exit")
	verbose := flag.Bool("v", false, "print request details")
	elementTimeout := flag.Duration("element-timeout", 60*time.Second, "per-element deadline including the CLI session")
	concurancy := flag.Int("concurancy", 10, "sets the number of conncurant http sesssions to the Strata API")
	elementsFile := flag.String("elements-path", "", "sets the path to the elements file")
	rps := flag.Float64("rps", 5, "sets amount amount of requests per second")
	burst := flag.Int("burst", 10, "sets api burst")
	sessionTimeout := flag.Duration("session-timeout", 20*time.Second, "sets the session timeout, referring to retries for a hung ION login")
	//this is set to 1 to prevent executing config changes unintentially. Multiple attemps and a write to the device can cause undersirable behavior. 
	attempts := flag.Int("attempts", 1, "sets the number of attempts to connect to the CLI before dropping the session")
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
	
	cfg := Config {
		Token: os.Getenv("SCM_TOKEN"),
		IONUsername: os.Getenv("ION_USER"),
		IONPassword: os.Getenv("ION_PASS"),
		Command: *cmd,
		Elements: elements,
		HTTPTimeout: *httpTimeout, 
		Verbose: *verbose,
		ElementTimeout: *elementTimeout,
		Concurancy: *concurancy,
		RPS: *rps,
		Burst: *burst,
		SessionTimeout: *sessionTimeout,
		Attempts: *attempts,
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	client := &http.Client{Timeout: cfg.HTTPTimeout}
	scm := NewSCM(client, cfg.Token, cfg.Verbose, cfg.RPS, cfg.Burst)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	

	if cfg.Command == "" {
		if err := interactiveCLI(ctx, scm, cfg, cfg.Elements[0]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}


	results := make([]Result, len(cfg.Elements))
	sem := make(chan struct{}, cfg.Concurancy)
	var wg sync.WaitGroup

	for i, eid := range cfg.Elements {
		wg.Add(1)
		go func() {
			defer wg.Done()

			sem <- struct{}{}
			defer func() {<-sem}()

			res, err := runElement(ctx, scm, cfg, eid)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				results[i] = Result{ElementID: eid, Error: err.Error()}
				return
			}
			results[i] = res
		}()
	}
	wg.Wait()

	failed := 0
	for _, r := range results{
		if r.Error != ""{
			failed++
		}
	}

	json.NewEncoder(os.Stdout).Encode(results)


	if failed > 0 {
		os.Exit(1)
	}
}
