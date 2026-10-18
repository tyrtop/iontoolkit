package saseauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// ErrNoSession reports that a silent attempt found no usable browser session, so
// the caller has to show a window.
var ErrNoSession = errors.New("no existing browser session")

type Options struct {
	// Chrome overrides the browser binary; empty lets chromedp search.
	Chrome string
	// DataDir is a browser profile to keep between runs. Holding the identity
	// provider's session cookie there is what lets later logins stay silent.
	DataDir string
	// Timeout bounds an interactive sign-in, including however long the human takes.
	Timeout time.Duration

	AuthorizeURL string
	TokenURL     string
	Client       *http.Client
}

func (o Options) authorize() string {
	if o.AuthorizeURL != "" {
		return o.AuthorizeURL
	}
	return AuthorizeURL
}

func (o Options) token() string {
	if o.TokenURL != "" {
		return o.TokenURL
	}
	return TokenURL
}

func (o Options) httpClient() *http.Client {
	if o.Client != nil {
		return o.Client
	}
	return http.DefaultClient
}

func (o Options) timeout() time.Duration {
	if o.Timeout > 0 {
		return o.Timeout
	}
	return 5 * time.Minute
}

// Login obtains a token for tsg, opening a browser window only when it has to.
// When a profile from an earlier run still holds a session, a headless attempt
// with prompt=none gets a token without the user seeing anything.
func Login(ctx context.Context, tsg string, opt Options) (*Token, error) {
	if opt.DataDir != "" {
		if fi, err := os.Stat(opt.DataDir); err == nil && fi.IsDir() {
			tok, err := login(ctx, tsg, opt, true)
			if err == nil {
				return tok, nil
			}
			if ctx.Err() != nil {
				return nil, err
			}
		}
	}
	return login(ctx, tsg, opt, false)
}

// LoginSilent attempts a token without ever showing a window, and reports
// ErrNoSession when that is not possible.
func LoginSilent(ctx context.Context, tsg string, opt Options) (*Token, error) {
	if opt.DataDir == "" {
		return nil, ErrNoSession
	}
	if fi, err := os.Stat(opt.DataDir); err != nil || !fi.IsDir() {
		return nil, ErrNoSession
	}
	tok, err := login(ctx, tsg, opt, true)
	if err != nil && ctx.Err() == nil {
		return nil, fmt.Errorf("%w: %v", ErrNoSession, err)
	}
	return tok, err
}

func login(ctx context.Context, tsg string, opt Options, silent bool) (*Token, error) {
	return loginToken(ctx, tsg, opt, silent)
}

func loginToken(ctx context.Context, tsg string, opt Options, silent bool) (*Token, error) {
	p, err := newPKCE()
	if err != nil {
		return nil, err
	}
	code, err := codeFromBrowser(ctx, p.authorizeURL(opt.authorize(), tsg, silent), p.state, opt, silent)
	if err != nil {
		return nil, err
	}
	return ExchangeCode(ctx, opt.httpClient(), opt.token(), code, p.verifier)
}

// codeFromBrowser drives the browser through the authorization page and watches
// the network for the redirect back to RedirectURI. That redirect is never
// fetched for its content: it only carries the code in its query string, so
// watching requests is enough and no local listener is needed.
func codeFromBrowser(ctx context.Context, authorizeURL, state string, opt Options, silent bool) (string, error) {
	timeout := opt.timeout()
	if silent {
		timeout = 45 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	allocOpts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	if !silent {
		allocOpts = append(allocOpts, chromedp.VisibleWindow)
	}
	if opt.Chrome != "" {
		allocOpts = append(allocOpts, chromedp.ExecPath(opt.Chrome))
	}
	if opt.DataDir != "" {
		allocOpts = append(allocOpts, chromedp.UserDataDir(opt.DataDir))
	}

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, allocOpts...)
	defer cancelAlloc()
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()
	// Close the profile cleanly so a following attempt can reuse the data dir.
	defer func() {
		shutdown, stop := context.WithTimeout(context.WithoutCancel(browserCtx), 5*time.Second)
		defer stop()
		chromedp.Cancel(shutdown)
	}()

	events := chromedp.Events(browserCtx, network.RequestWillBeSent)

	// The navigation runs alongside the watch rather than before it. Its final
	// hop is to RedirectURI, a page this tool never wants to load, so it often
	// ends in a navigation error even though the redirect already carried the
	// code past the watcher. Events queues from the moment it returns, so
	// nothing is lost by not awaiting the navigation first.
	navErr := make(chan error, 1)
	go func() { navErr <- chromedp.Do(browserCtx, chromedp.Navigate(authorizeURL)) }()

	for ev, err := range events {
		if err != nil {
			return "", fmt.Errorf("browser: %w", err)
		}
		if ev.Request == nil || !strings.HasPrefix(ev.Request.URL, RedirectURI) {
			continue
		}
		return CodeFromCallback(ev.Request.URL, state)
	}

	select {
	case err := <-navErr:
		if err != nil {
			return "", fmt.Errorf("open authorization page: %w", err)
		}
	default:
	}
	if ctx.Err() != nil {
		return "", fmt.Errorf("sign-in did not complete within %s", timeout)
	}
	return "", fmt.Errorf("browser closed before sign-in completed")
}

// ManualURL returns an authorization URL for the paste-back flow, alongside the
// verifier and state needed to finish it. It is the fallback for machines with
// no usable browser: the user signs in elsewhere, then pastes the address bar
// contents into CompleteManual.
func ManualURL(tsg string, opt Options) (authURL string, verifier, state string, err error) {
	p, err := newPKCE()
	if err != nil {
		return "", "", "", err
	}
	return p.authorizeURL(opt.authorize(), tsg, false), p.verifier, p.state, nil
}

func CompleteManual(ctx context.Context, callbackURL, verifier, state string, opt Options) (*Token, error) {
	code, err := CodeFromCallback(callbackURL, state)
	if err != nil {
		return nil, err
	}
	return ExchangeCode(ctx, opt.httpClient(), opt.token(), code, verifier)
}
