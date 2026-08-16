package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/coder/websocket"
	"golang.org/x/term"
)


func interactiveCLI(ctx context.Context, client *http.Client, cfg Config, eid string) error {
	// TODO: lookup — same as runElement, through the decode.
	// You need el.Name only for the verbose banner here.

	conn, err := dialToolkit(ctx, cfg.Token, eid)
	if err != nil {
		return fmt.Errorf("element %s: dial: %w", eid, err)
	}
	defer conn.CloseNow()

	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("element: %s: make raw: %w", eid, err)
	}
	defer term.Restore(fd, oldState)

	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				if i := bytes.IndexByte(buf[:n], 0x1d); i >= 0 {
					if i > 0 {
						conn.Write(ctx, websocket.MessageText, buf[:i])
					}
					conn.CloseNow()
					return
				}
				if werr := conn.Write(ctx, websocket.MessageText, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return nil
		}
		if cfg.Verbose {
			fmt.Printf("%q\r\n", data)
		} else {
			os.Stdout.Write(data)
		}
	}
}

