package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/coder/websocket"
)

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

func strip(s string) string {
	return ansi.ReplaceAllString(s, "")
}

func clean(out, prompt string, cmd string) string {
	s := strip(out)

	marker := prompt + " " + cmd
	if i := strings.LastIndex(s, marker); i >= 0 {
		s = s[i+len(marker):]
	}
	
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	s = strings.Join(lines, "\n")

	s = strings.Trim(s, " \b\r\n")
	s = strings.TrimSuffix(s, prompt)
	return strings.Trim(s, " \b\r\n")
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

func runOnce(ctx context.Context, conn *websocket.Conn, prompt, user, pass, cmd string) (string, error) {
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

	return readUntilPrompt(ctx, conn, prompt)
}
