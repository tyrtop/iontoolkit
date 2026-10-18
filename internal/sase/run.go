package sase

import (
	"context"
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

func RunStream(ctx context.Context, client Client, o Options, elements []string) <-chan Event {
	out := make(chan Event, 2*len(elements))

	go func() {
		defer close(out)

		sem := make(chan struct{}, o.Concurrency)
		var wg sync.WaitGroup

		for i, eid := range elements {
			wg.Add(1)
			go func() {
				defer wg.Done()

				sem <- struct{}{}
				defer func() { <-sem }()

				out <- Event{Index: i, Element: eid}

				began := time.Now()
				res, err := runElement(ctx, client, o, eid)
				if err != nil {
					res = Result{ElementID: eid, Error: err.Error()}
				}
				out <- Event{
					Index:   i,
					Element: eid,
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
	for ev := range RunStream(ctx, client, o, elements) {
		if ev.Done {
			results[ev.Index] = ev.Result
		}
	}
	return results
}
