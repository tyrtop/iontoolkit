package iontk

import (
	"context"
	"fmt"
	"os"
	"sync"

	"tyrtop.com/iontk/internal/scm"
)

func Run(ctx context.Context, client *scm.Client, o Options, elements []string) []Result {
	results := make([]Result, len(elements))
	sem := make(chan struct{}, o.Concurrency)
	var wg sync.WaitGroup

	for i, eid := range elements {
		wg.Add(1)
		go func() {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			res, err := runElement(ctx, client, o, eid)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				results[i] = Result{ElementID: eid, Error: err.Error()}
				return
			}
			results[i] = res
		}()
	}
	wg.Wait()
	return results
}
