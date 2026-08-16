package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"io"
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

func lookupElement(ctx context.Context, client *http.Client, cfg Config, eid string) (Element, error) {
	url := scmBase + "/elements/" + eid

	if cfg.Verbose {
		fmt.Fprintln(os.Stderr, "GET", url)
	}
	
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Element{}, fmt.Errorf("element %s: build request: %w", eid, err)
	}
	
	req.Header.Set("Authorization", "Bearer "+ cfg.Token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", browserUA)

	resp, err := client.Do(req)
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
