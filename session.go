package main

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/coder/websocket"
)

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

func strip(s string) string {
	return ansi.ReplaceAllString(s, "")
}

func clean(out, prompt string) string {
	s := strip(out)
	s = strings.TrimRight(s, " \b\r\n")
	s = strings.TrimSuffix(s, prompt)
	return strings.TrimRight(s, " \r\n")
}

func readUntil(ctx context.Context, conn *websocket.Conn, pattern string) (string, error) {
	var buf strings.Builder
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return buf.String(), err
		}
		buf.Write(data)
		if strings.Contains(strip(buf.String()), pattern) {
			return buf.String(), nil
		}
	}
}

func readUntilPrompt(ctx context.Context, conn *websocket.Conn, prompt string) (string, error) {
	var buf strings.Builder
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return buf.String(), err
		}
		buf.Write(data)
		tail := strings.TrimRight(strip(buf.String()), " \b\r\n")
		if strings.HasSuffix(tail, prompt) {
			return buf.String(), nil
		}
	}
}

func runOnce(ctx context.Context, conn *websocket.Conn, prompt, cmd string) (string, error) {
	user := os.Getenv("ION_USER")
	pass := os.Getenv("ION_PASS")
	if user == "" || pass == "" {
		return "", fmt.Errorf("ION_USER and ION_PASS must be set")
	}

	if _, err := readUntil(ctx, conn, "login: "); err != nil {
		return "", fmt.Errorf("waiting for login prompt: %w", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte(user+"\r")); err != nil {
		return "", err
	}

	if _, err := readUntil(ctx, conn, "Password: "); err != nil {
		return "", fmt.Errorf("waiting for password prompt: %w", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte(pass+"\r")); err != nil {
		return "", err
	}

	if _, err := readUntilPrompt(ctx, conn, prompt); err != nil {
		return "", fmt.Errorf("waiting for shell: %w", err)
	}

	if err := conn.Write(ctx, websocket.MessageText, []byte(cmd+"\r")); err != nil {
		return "", err
	}

	// swallow the per-character echo until the full command is drawn
	if _, err := readUntil(ctx, conn, prompt+" "+cmd); err != nil {
		return "", fmt.Errorf("waiting for command echo: %w", err)
	}

	return readUntilPrompt(ctx, conn, prompt)
}
