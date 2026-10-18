package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"tyrtop.com/iontk/internal/sase/api"
)

func NewClient(ctx context.Context, f Flags, logw io.Writer) (*api.Client, error) {
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
		src := BrowserSource(f, httpClient, OpenStore())
		return api.NewTokenSourceClient(ctx, httpClient, wsClient, src, f.RefreshMargin, f.Verbose, f.RPS, f.Burst)
	}
	fmt.Fprintln(logw, "minting from service account")
	return api.NewServiceAccountClient(ctx, httpClient, wsClient,
		f.ClientID, f.ClientSecret, f.TSGID, f.RefreshMargin, f.Verbose, f.RPS, f.Burst)
}
