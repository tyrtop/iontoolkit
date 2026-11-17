package sase

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Event struct {
	Index   int
	Element string
	Done    bool
	Result  Result
	Elapsed time.Duration
}

type Target struct {
	ElementID string
	Client    Client
	User      string
	Pass      string
}

func (t Target) creds(o Options) (string, string) {
	user, pass := t.User, t.Pass
	if user == "" {
		user = o.Username
	}
	if pass == "" {
		pass = o.Password
	}
	return user, pass
}

type credential interface{ RefreshStopped() bool }

func credentialDead(c Client) bool {
	cd, ok := c.(credential)
	return ok && cd.RefreshStopped()
}

func RunStream(ctx context.Context, o Options, targets []Target) <-chan Event {
	out := make(chan Event, 2*len(targets))

	go func() {
		defer close(out)

		sem := make(chan struct{}, o.Concurrency)
		var wg sync.WaitGroup

		for i, t := range targets {
			wg.Add(1)
			go func() {
				defer wg.Done()

				sem <- struct{}{}
				defer func() { <-sem }()

				out <- Event{Index: i, Element: t.ElementID}

				began := time.Now()
				var res Result
				var err error
				if t.Client == nil {
					err = fmt.Errorf("element %s: no client for its tenant", t.ElementID)
				} else if credentialDead(t.Client) {
					err = fmt.Errorf("element %s: sign-in expired before this element was reached", t.ElementID)
				} else {
					res, err = runElement(ctx, t.Client, o, t)
				}
				if err != nil {
					res.ElementID, res.Error = t.ElementID, err.Error()
				}
				out <- Event{
					Index:   i,
					Element: t.ElementID,
					Done:    true,
					Result:  res,
					Elapsed: time.Since(began),
				}
			}()
		}

		wg.Wait()
	}()

	return out
}
