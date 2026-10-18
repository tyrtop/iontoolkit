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

// Target is one element with everything needed to reach it. The client and the
// device login both travel per element, which is what lets a single run span
// tenants: each tenant has its own token and its own ION credentials.
type Target struct {
	ElementID string
	Client    Client
	// User and Pass override the Options credentials for this element. Empty
	// means fall back to the Options, which is the single-tenant case.
	User string
	Pass string
}

// creds returns the login to use for a target, preferring its own.
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

// A spent token fails every remaining element identically, each for a full
// element timeout, burying the one cause in a report full of 401s.
func credentialDead(c Client) bool {
	cd, ok := c.(credential)
	return ok && cd.RefreshStopped()
}

// Targets pairs every element with the same client, for a run inside one tenant.
func Targets(client Client, elements []string) []Target {
	out := make([]Target, 0, len(elements))
	for _, eid := range elements {
		out = append(out, Target{ElementID: eid, Client: client})
	}
	return out
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
					res = Result{ElementID: t.ElementID, Error: err.Error()}
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

func Run(ctx context.Context, client Client, o Options, elements []string) []Result {
	results := make([]Result, len(elements))
	for ev := range RunStream(ctx, o, Targets(client, elements)) {
		if ev.Done {
			results[ev.Index] = ev.Result
		}
	}
	return results
}
