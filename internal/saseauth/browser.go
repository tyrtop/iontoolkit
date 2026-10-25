package saseauth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"tyrtop.com/iontk/internal/logging"
)

var ErrNoSession = errors.New("no existing browser session")

type Options struct {
	Chrome  string
	DataDir string
	Timeout time.Duration
	// hardened Linux images forbid the user namespaces Chrome's sandbox needs
	NoSandbox bool

	Host   string
	Client *http.Client
	Logger *slog.Logger

	Seed *Seed
}

func (o Options) log() *slog.Logger {
	if o.Logger != nil {
		return o.Logger
	}
	return logging.L()
}

func (o Options) authorize() string { return o.url("authorize") }

func (o Options) token() string { return o.url("token") }

func (o Options) url(name string) string {
	u, err := endpoints.URLAt(name, o.Host, nil)
	if err != nil {
		panic(err)
	}
	return u
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
	tok, err := LoginSilent(ctx, tsg, opt)
	if err == nil {
		return tok, nil
	}
	if ctx.Err() != nil {
		return nil, err
	}
	return login(ctx, tsg, opt, false)
}

var silentRefused sync.Map

func refusalKey(dir, tsg string) string { return dir + "\x00" + tsg }

func clearRefusals(dir string) {
	if dir == "" {
		return
	}
	silentRefused.Range(func(k, _ any) bool {
		if s, ok := k.(string); ok && strings.HasPrefix(s, dir+"\x00") {
			silentRefused.Delete(k)
		}
		return true
	})
}

func LoginSilent(ctx context.Context, tsg string, opt Options) (*Token, error) {
	if opt.DataDir == "" {
		return nil, ErrNoSession
	}
	if fi, err := os.Stat(opt.DataDir); err != nil || !fi.IsDir() {
		return nil, ErrNoSession
	}
	key := refusalKey(opt.DataDir, tsg)
	if _, refused := silentRefused.Load(key); refused {
		opt.log().Info("silent sign-in skipped", "tsg", tsg, "reason", "already refused for this profile")
		return nil, fmt.Errorf("%w: a silent sign-in was already refused for this profile", ErrNoSession)
	}
	tok, err := login(ctx, tsg, opt, true)
	if err != nil && ctx.Err() == nil {
		silentRefused.Store(key, struct{}{})
		opt.log().Info("silent sign-in refused", "tsg", tsg, "err", err)
		return nil, fmt.Errorf("%w: %v", ErrNoSession, err)
	}
	return tok, err
}

func login(ctx context.Context, tsg string, opt Options, silent bool) (*Token, error) {
	tok, err := loginToken(ctx, tsg, opt, silent)
	if err != nil {
		return nil, err
	}
	clearRefusals(opt.DataDir)
	return tok, nil
}

func loginToken(ctx context.Context, tsg string, opt Options, silent bool) (*Token, error) {
	log := opt.log().With("tsg", tsg, "silent", silent)

	p, err := newPKCE()
	if err != nil {
		return nil, err
	}
	log.Info("launching chrome", "data_dir", opt.DataDir, "chrome", opt.Chrome)
	code, err := codeFromBrowser(ctx, p.authorizeURL(opt.authorize(), tsg, silent), p.state, opt, silent)
	if err != nil {
		log.Warn("browser leg failed", "err", err)
		return nil, err
	}

	log.Info("authorization code received")
	tok, err := ExchangeCode(ctx, opt.httpClient(), opt.token(), code, p.verifier)
	if err != nil {
		log.Warn("code exchange failed", "err", err)
		return nil, err
	}
	return tok, nil
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

// Chrome hands a second launch on one user-data-dir to the first
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
		if ev.Request == nil || !strings.HasPrefix(ev.Request.URL, RedirectURI()) {
			continue
		}
		opt.log().Debug("callback reached", "url", ev.Request.URL)
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
