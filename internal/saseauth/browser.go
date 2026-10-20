package saseauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

var ErrNoSession = errors.New("no existing browser session")

type Options struct {
	Chrome  string
	DataDir string
	Timeout time.Duration
	// hardened Linux images forbid the user namespaces Chrome needs and it cannot
	// start there at all; anything that can sandbox keeps the sandbox
	NoSandbox bool

	AuthorizeURL string
	TokenURL     string
	Client       *http.Client

	Seed *Seed
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

var cleanUpGrace = 5 * time.Second

func stopWaiting(cancel func()) {
	done := make(chan struct{})
	go func() { cancel(); close(done) }()
	select {
	case <-done:
	case <-time.After(cleanUpGrace):
	}
}

// one channel per data dir: Chrome locks a user-data-dir, so a second launch
// hands its URL to the running instance and exits, leaving chromedp waiting
var profileLocks sync.Map

func lockProfile(ctx context.Context, dir string) (func(), error) {
	if dir == "" {
		return func() {}, nil
	}
	v, _ := profileLocks.LoadOrStore(dir, make(chan struct{}, 1))
	ch := v.(chan struct{})
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func codeFromBrowser(ctx context.Context, authorizeURL, state string, opt Options, silent bool) (string, error) {
	unlock, err := lockProfile(ctx, opt.DataDir)
	if err != nil {
		return "", err
	}
	defer unlock()

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
	if opt.NoSandbox {
		allocOpts = append(allocOpts, chromedp.NoSandbox)
	}

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, allocOpts...)
	defer stopWaiting(cancelAlloc)
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer stopWaiting(cancelBrowser)
	defer stopWaiting(func() {
		shutdown, stop := context.WithTimeout(context.WithoutCancel(browserCtx), 5*time.Second)
		defer stop()
		chromedp.Cancel(shutdown)
	})

	events := chromedp.Events(browserCtx, network.RequestWillBeSent)

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
