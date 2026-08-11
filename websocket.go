package main

import(
	"context"
	"fmt"
	"github.com/coder/websocket"
)

func dialToolkit(ctx context.Context, token, eid string) (*websocket.Conn, error) {
	url:= fmt.Sprintf("wss://api.sase.paloaltonetworks.com/sdwan/v2.0/api/elements/%s/ws/toolkitsessions?cols=80&rows=24", eid)
	
	c, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		Subprotocols: []string{"Bearer", token},
		HTTPHeader: map[string][]string{
			"User-Agent": {browserUA},
		},
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}
