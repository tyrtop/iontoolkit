package scm

import (
	"context"
	"fmt"
	"github.com/coder/websocket"
)

func (s *Client) DialToolkit(ctx context.Context, eid string) (*websocket.Conn, error) {
	url := fmt.Sprintf("wss://api.sase.paloaltonetworks.com/sdwan/v2.0/api/elements/%s/ws/toolkitsessions?cols=200&rows=24", eid)
	c, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPClient:   s.wsClient,
		Subprotocols: []string{"Bearer", s.token},
		HTTPHeader: map[string][]string{
			"User-Agent": {browserUA},
		},
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}
