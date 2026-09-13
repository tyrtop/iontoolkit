package api

import (
	"context"
	"fmt"

	"github.com/coder/websocket"
	"tyrtop.com/iontk/internal/sase/ioncli"
)

func (s *Client) DialToolkit(ctx context.Context, eid string) (*websocket.Conn, error) {
	url := fmt.Sprintf("wss://api.sase.paloaltonetworks.com/sdwan/v2.0/api/elements/%s/ws/toolkitsessions?cols=200&rows=24", eid)
	c, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPClient:   s.wsClient,
		Subprotocols: []string{"Bearer", s.getToken()},
		HTTPHeader: map[string][]string{
			"User-Agent": {browserUA},
		},
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Client) ToolkitSession(ctx context.Context, eid, prompt, user, pass string, cmds []string) ([]ioncli.CommandOutput, error) {
	conn, err := s.DialToolkit(ctx, eid)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	// this line lets you do more than ~150 devices at once. If you don't "gracefully close your websocket connection"
	// the ION hijacks it, and it seems like PA keeps track of the # of sessions you have open.
	// Sacrificing a frame lets you utilize more socket connections overall.
	// if you just use CloseNow() you will start getting limited at ~500 connections opened.
	defer conn.Close(websocket.StatusNormalClosure, "")

	return ioncli.Run(ctx, conn, prompt, user, pass, cmds)
}
