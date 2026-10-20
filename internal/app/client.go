package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/logging"
	"tyrtop.com/iontk/internal/sase/api"
)

func NewClient(ctx context.Context, st *config.Store, f Flags, logw io.Writer) (*api.Client, error) {
	log := logging.L().With("tsg", f.TSGID)
	chose := func(what string) {
		fmt.Fprintln(logw, what)
		log.Info("credential path", "path", what)
	}

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
		chose("using fixtures")
		fake := &http.Client{Transport: Fixture.Transport(f.TSGID)}
		const fixtureRPS = 1000
		return setup(api.NewClient(fake, fake, "fixture", f.Verbose, fixtureRPS, fixtureRPS)), nil
	}
	if f.Token != "" {
		chose("using SCM_TOKEN")
		return setup(api.NewClient(httpClient, wsClient, f.Token, f.Verbose, f.RPS, f.Burst)), nil
	}
	if f.BrowserAuth {
		chose("using the browser sign-in for this tenant")
		src := BrowserSource(f, httpClient, st)
		c, err := api.NewTokenSourceClient(ctx, httpClient, wsClient, src, f.RefreshMargin, f.Verbose, f.RPS, f.Burst)
		if err != nil {
			log.Warn("the browser credential did not produce a token", "err", err)
			return nil, err
		}
		return setup(c), nil
	}
	chose("minting from service account")
	c, err := api.NewServiceAccountClient(ctx, httpClient, wsClient,
		f.ClientID, f.ClientSecret, f.TSGID, f.RefreshMargin, f.Verbose, f.RPS, f.Burst)
	if err != nil {
		log.Warn("the service account did not mint a token", "err", err, "client_id", f.ClientID)
		return nil, err
	}
	return setup(c), nil
}
