package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/sase/api"
)

func NewClient(ctx context.Context, st *config.Store, f Flags, logw io.Writer) (*api.Client, error) {
	wsClient := &http.Client{Transport: &http.Transport{
		MaxConnsPerHost:     f.Concurrency,
		MaxIdleConnsPerHost: 50,
		TLSHandshakeTimeout: 10 * time.Second,
	}}
	httpClient := &http.Client{Timeout: f.HTTPTimeout}

	dial := api.DialPolicy{Attempts: f.DialAttempts, Timeout: f.DialTimeout}
	setup := func(c *api.Client) *api.Client {
		c.SetDialPolicy(dial)
		c.SetPagerOff(f.PagerOff)
		c.SetPagerPrompts(f.PagerPrompts)
		return c
	}

	if Fixture != nil {
		fmt.Fprintln(logw, "using fixtures")
		fake := &http.Client{Transport: Fixture.Transport(f.TSGID)}
		const fixtureRPS = 1000
		return setup(api.NewClient(fake, fake, "fixture", f.Verbose, fixtureRPS, fixtureRPS)), nil
	}
	if f.Token != "" {
		fmt.Fprintln(logw, "using SCM_TOKEN")
		return setup(api.NewClient(httpClient, wsClient, f.Token, f.Verbose, f.RPS, f.Burst)), nil
	}
	if f.BrowserAuth {
		fmt.Fprintln(logw, "using the browser sign-in for this tenant")
		src := BrowserSource(f, httpClient, st)
		c, err := api.NewTokenSourceClient(ctx, httpClient, wsClient, src, f.RefreshMargin, f.Verbose, f.RPS, f.Burst)
		if err != nil {
			return nil, err
		}
		return setup(c), nil
	}
	fmt.Fprintln(logw, "minting from service account")
	c, err := api.NewServiceAccountClient(ctx, httpClient, wsClient,
		f.ClientID, f.ClientSecret, f.TSGID, f.RefreshMargin, f.Verbose, f.RPS, f.Burst)
	if err != nil {
		return nil, err
	}
	return setup(c), nil
}
