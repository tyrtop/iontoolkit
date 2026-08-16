package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"io"
	"golang.org/x/time/rate"
)

const scmBase = "https://api.sase.paloaltonetworks.com/sdwan/v3.2/api"

type Element struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	HWID      string `json:"hw_id"`
	Model     string `json:"model_name"`
	Software  string `json:"software_version"`
	SiteID    string `json:"site_id"`
	Connected bool   `json:"connected"`
	State     string `json:"state"`
}

type SCM struct {
	client *http.Client
	limiter *rate.Limiter
	token string
	verbose bool
}

func NewSCM(client *http.Client, token string, verbose bool, rps float64, burst int) *SCM {
	return &SCM{
		client: client, 
		limiter: rate.NewLimiter(rate.Limit(rps), burst),
		token: token,
		verbose: verbose,
	}
}

func (s *SCM) lookupElement(ctx context.Context, eid string) (Element, error) {
	if err := s.limiter.Wait(ctx); err != nil {
		return Element{}, fmt.Errorf("element %s: rate limit wait: %w", eid, err)
	}

	url := scmBase + "/elements/" + eid

	if s.verbose {
		fmt.Fprintln(os.Stderr, "GET", url)
	}
	
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Element{}, fmt.Errorf("element %s: build request: %w", eid, err)
	}
	
	req.Header.Set("Authorization", "Bearer "+ s.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", browserUA)

	resp, err := s.client.Do(req)
	if err != nil{
		return Element{}, fmt.Errorf("element %s: response: %w", eid, err)
	}
	
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return Element{}, fmt.Errorf("element %s: status %s: %s", eid, resp.Status, body)
	}

	var el Element
	if err := json.NewDecoder(resp.Body).Decode(&el); err != nil {
		return Element{}, fmt.Errorf("element %s: json decode: %w", eid, err)
	}
	
	return el, nil
}
