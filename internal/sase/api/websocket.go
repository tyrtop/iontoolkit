package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/coder/websocket"
	"tyrtop.com/iontk/internal/sase/ioncli"
)

// DialPolicy bounds one dial and says how many to try. The toolkit socket
// sometimes just does not come up, and a single long wait cannot tell that from
// a device being slow.
type DialPolicy struct {
	Attempts int
	// Timeout bounds each attempt. A dial that is going to work does so
	// quickly, so this is short and tried again rather than long and once.
	Timeout time.Duration
	Backoff time.Duration
}

var DefaultDialPolicy = DialPolicy{Attempts: 3, Timeout: 10 * time.Second, Backoff: time.Second}

// DialAttempt is one try, reported as it begins. Err is what the previous try
// failed with, so a caller can show both which attempt is running and why the
// last one did not take.
type DialAttempt struct {
	ElementID string
	Attempt   int
	Of        int
	Err       error
}

// SetPagerOff sets the command sent once after login to stop the device paging.
// Empty sends nothing, which is the behaviour this had before.
func (s *Client) SetPagerOff(cmd string) {
	s.dialMu.Lock()
	s.pager = cmd
	s.dialMu.Unlock()
}

func (s *Client) pagerOff() string {
	s.dialMu.RLock()
	defer s.dialMu.RUnlock()
	return s.pager
}

// SetPagerPrompts sets the lines a session answers with a space. Nil keeps the
// built-in list; empty turns the scraper off.
func (s *Client) SetPagerPrompts(p []string) {
	s.dialMu.Lock()
	s.pagers = p
	s.dialMu.Unlock()
}

func (s *Client) pagerPrompts() []string {
	s.dialMu.RLock()
	defer s.dialMu.RUnlock()
	return s.pagers
}

// SetDialReporter watches the dial without disturbing the policy. It is how a
// UI shows a retry as it happens; dialling is otherwise silent until it either
// works or runs out.
func (s *Client) SetDialReporter(f func(DialAttempt)) {
	s.dialMu.Lock()
	s.onDial = f
	s.dialMu.Unlock()
}

func (s *Client) dialReporter() func(DialAttempt) {
	s.dialMu.RLock()
	defer s.dialMu.RUnlock()
	return s.onDial
}

func (p DialPolicy) withDefaults() DialPolicy {
	d := DefaultDialPolicy
	if p.Attempts > 0 {
		d.Attempts = p.Attempts
	}
	if p.Timeout > 0 {
		d.Timeout = p.Timeout
	}
	if p.Backoff > 0 {
		d.Backoff = p.Backoff
	}
	return d
}

// SetDialPolicy overrides the dial retry for this client. Zero fields keep the
// default.
func (s *Client) SetDialPolicy(p DialPolicy) {
	s.dialMu.Lock()
	s.dial = p.withDefaults()
	s.dialMu.Unlock()
}

func (s *Client) dialPolicy() DialPolicy {
	s.dialMu.RLock()
	defer s.dialMu.RUnlock()
	return s.dial.withDefaults()
}

func (s *Client) DialToolkit(ctx context.Context, eid string, cols, rows int) (*websocket.Conn, error) {
	url := fmt.Sprintf("wss://api.sase.paloaltonetworks.com/sdwan/v2.0/api/elements/%s/ws/toolkitsessions?cols=%d&rows=%d", eid, cols, rows)
	return s.dialURL(ctx, url, eid)
}

func (s *Client) dialURL(ctx context.Context, url, eid string) (*websocket.Conn, error) {
	p := s.dialPolicy()

	report := s.dialReporter()

	var err error
	for attempt := 1; ; attempt++ {
		if report != nil {
			report(DialAttempt{ElementID: eid, Attempt: attempt, Of: p.Attempts, Err: err})
		}
		var conn *websocket.Conn
		conn, err = s.dialOnce(ctx, url, p.Timeout)
		if err == nil {
			return conn, nil
		}
		if attempt >= p.Attempts || !retryableDial(err) || ctx.Err() != nil {
			break
		}
		if s.verbose {
			fmt.Fprintf(os.Stderr, "element %s: dial attempt %d: %v\n", eid, attempt, err)
		}
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(p.Backoff):
		}
	}
	return nil, err
}

func (s *Client) dialOnce(ctx context.Context, url string, timeout time.Duration) (*websocket.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPClient:   s.wsClient,
		Subprotocols: []string{"Bearer", s.getToken()},
		HTTPHeader: map[string][]string{
			"User-Agent": {browserUA},
		},
	})
	if err != nil {
		return nil, dialError{err: err, status: statusOf(resp)}
	}
	return conn, nil
}

// dialError carries the handshake status, so a rejected credential is told from
// a socket that would not come up without parsing the message.
type dialError struct {
	err    error
	status int
}

func (e dialError) Error() string { return e.err.Error() }
func (e dialError) Unwrap() error { return e.err }

func statusOf(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}

// retryableDial reports whether dialling again could plausibly work. A refused
// credential and a missing element will not change on their own; everything
// else is the socket not coming up, which is what the retry is for.
func retryableDial(err error) bool {
	var de dialError
	if !errors.As(err, &de) {
		return true
	}
	switch de.status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		return false
	}
	return true
}

func (s *Client) ToolkitSession(ctx context.Context, eid, prompt, user, pass string, cmds []string) ([]ioncli.CommandOutput, error) {
	conn, err := s.DialToolkit(ctx, eid, 200, 24)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	// this line lets you do more than ~150 devices at once. If you don't "gracefully close your websocket connection"
	// the ION hijacks it, and it seems like PA keeps track of the # of sessions you have open.
	// Sacrificing a frame lets you utilize more socket connections overall.
	// if you just use CloseNow() you will start getting limited at ~500 connections opened.
	defer conn.Close(websocket.StatusNormalClosure, "")

	return ioncli.Run(ctx, conn, ioncli.RunOptions{
		Prompt:   prompt,
		User:     user,
		Pass:     pass,
		Commands: cmds,
		PagerOff: s.pagerOff(),
		Pagers:   s.pagerPrompts(),
	})
}
