package fixture

import (
	"context"
	"fmt"

	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/sase/ioncli"
)

// SessionClient is what a batch run uses in fixture mode. It satisfies
// sase.Client, and its ToolkitSession goes through the real ioncli code against
// a playback device, so autologin and the pager scraper are exercised rather
// than skipped.
type SessionClient struct {
	set *Set
	// Where paging settings come from, since the client is built before the
	// flags reach a session.
	pagerOff string
	pagers   []string
}

func (s *Set) SessionClient(pagerOff string, pagers []string) *SessionClient {
	return &SessionClient{set: s, pagerOff: pagerOff, pagers: pagers}
}

func (c *SessionClient) LookupElement(ctx context.Context, eid string) (api.Element, error) {
	el := c.set.element(eid)
	if el == nil {
		return api.Element{}, fmt.Errorf("element %s: status 404 Not Found: no such element in the fixtures", eid)
	}
	return el.apiElement(), nil
}

func (c *SessionClient) ToolkitSession(ctx context.Context, eid, prompt, user, pass string, cmds []string) ([]ioncli.CommandOutput, error) {
	conn, err := c.set.dial(eid)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	defer conn.hangUp()

	return ioncli.Run(ctx, conn, ioncli.RunOptions{
		Prompt:   prompt,
		User:     user,
		Pass:     pass,
		Commands: cmds,
		PagerOff: c.pagerOff,
		Pagers:   c.pagers,
	})
}
