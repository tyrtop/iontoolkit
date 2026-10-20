package ioncli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type scriptServer struct {
	srv *httptest.Server

	mu  sync.Mutex
	got strings.Builder
}

func (s *scriptServer) record(b []byte) {
	s.mu.Lock()
	s.got.Write(b)
	s.mu.Unlock()
}

func (s *scriptServer) sent() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.got.String()
}

func (s *scriptServer) Close() { s.srv.Close() }

func dialTestConn(t *testing.T, chunks []string) (*scriptServer, *websocket.Conn) {
	t.Helper()
	s := &scriptServer{}

	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx := r.Context()

		if len(chunks) > 0 {
			if err := c.Write(ctx, websocket.MessageText, []byte(chunks[0])); err != nil {
				return
			}
		}
		for _, next := range chunks[1:] {
			_, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			s.record(data)
			if err := c.Write(ctx, websocket.MessageText, []byte(next)); err != nil {
				return
			}
		}
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			s.record(data)
		}
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://"+strings.TrimPrefix(s.srv.URL, "http://"), nil)
	if err != nil {
		s.Close()
		t.Fatalf("dial test server: %v", err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return s, conn
}

func TestRunLogsInAndReturnsOutput(t *testing.T) {
	srv, conn := dialTestConn(t, []string{
		"login: ", "Password: ", "ion#", "ion# uptime\r\nup 1 day\r\nion#",
	})
	defer srv.Close()

	outs, err := Run(context.Background(), conn, RunOptions{
		Prompt: "ion#", User: "u", Pass: "p", Commands: []string{"uptime"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(outs) != 1 || outs[0].Command != "uptime" || outs[0].Output != "up 1 day" {
		t.Errorf("outputs = %+v, want one uptime result of %q", outs, "up 1 day")
	}
	if sent := srv.sent(); !strings.Contains(sent, "u\r") || !strings.Contains(sent, "p\r") {
		t.Errorf("sent = %q, want the login and password", sent)
	}
}

func TestRunSendsThePagerOffCommandBeforeAnythingElse(t *testing.T) {
	srv, conn := dialTestConn(t, []string{
		"login: ", "Password: ", "ion#", "ion#", "ion# uptime\r\nup 1 day\r\nion#",
	})
	defer srv.Close()

	outs, err := Run(context.Background(), conn, RunOptions{
		Prompt:   "ion#",
		User:     "u",
		Pass:     "p",
		PagerOff: "terminal length 0",
		Commands: []string{"uptime"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	sent := srv.sent()
	i, j := strings.Index(sent, "terminal length 0"), strings.Index(sent, "uptime")
	if i < 0 {
		t.Fatalf("the pager-off command was not sent: %q", sent)
	}
	if j >= 0 && i > j {
		t.Errorf("pager-off was sent after the first command: %q", sent)
	}
	if len(outs) != 1 || outs[0].Command != "uptime" {
		t.Errorf("outputs = %+v, want only the uptime result", outs)
	}
}

func TestRunSendsNothingWhenNoPagerCommandIsSet(t *testing.T) {
	srv, conn := dialTestConn(t, []string{
		"login: ", "Password: ", "ion#", "ion# uptime\r\nup 1 day\r\nion#",
	})
	defer srv.Close()

	if _, err := Run(context.Background(), conn, RunOptions{
		Prompt: "ion#", User: "u", Pass: "p", Commands: []string{"uptime"},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if sent := srv.sent(); strings.Contains(sent, "terminal") {
		t.Errorf("a pager command was sent with none configured: %q", sent)
	}
}

func TestATimedOutReadNamesWhatTheDeviceSent(t *testing.T) {
	srv, conn := dialTestConn(t, []string{
		"login: ", "Password: ", "ion#", "ion# help\r\nlots of text\r\n--More--",
	})
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_, err := Run(ctx, conn, RunOptions{
		Prompt: "ion#", User: "u", Pass: "p", Commands: []string{"help"},
	})
	if err == nil {
		t.Fatal("Run() error = nil, want the paged command to time out")
	}
	if !strings.Contains(err.Error(), "--More--") {
		t.Errorf("error = %v, want the pager line it stopped on", err)
	}
}

func TestATimedOutReadKeepsThePasswordOutOfTheError(t *testing.T) {
	srv, conn := dialTestConn(t, []string{
		"login: ", "Password: ", "sw0rdf1sh\r\nnot a prompt",
	})
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_, err := Run(ctx, conn, RunOptions{
		Prompt: "ion#", User: "u", Pass: "sw0rdf1sh", Commands: []string{"uptime"},
	})
	if err == nil {
		t.Fatal("Run() error = nil, want the missing prompt to time out")
	}
	if strings.Contains(err.Error(), "sw0rdf1sh") {
		t.Errorf("error = %v, want the password redacted", err)
	}
	if !strings.Contains(err.Error(), "***") {
		t.Errorf("error = %v, want the redaction to show something was removed", err)
	}
}

func TestTailOfAReadIsBoundedAndReadable(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		redact string
		want   string
	}{
		{"nothing arrived", "", "", "nothing"},
		{"ansi is stripped", "\x1b[0mion# \x1b[K", "", `"ion#"`},
		{"trailing newlines go", "paged\r\n", "", `"paged"`},
		{"a secret is removed", "login as hunter2", "hunter2", `"login as ***"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tail(tt.raw, tt.redact); got != tt.want {
				t.Errorf("tail() = %s, want %s", got, tt.want)
			}
		})
	}

	long := tail(strings.Repeat("x", 500), "")
	if len([]rune(long)) > 130 {
		t.Errorf("tail() of a screenful is %d runes, want it bounded", len([]rune(long)))
	}
	if !strings.Contains(long, "\u2026") {
		t.Errorf("tail() = %s, want a mark that it was cut", long)
	}
}

func TestRunAnswersThePagerAndStitchesTheOutput(t *testing.T) {
	srv, conn := dialTestConn(t, []string{
		"login: ", "Password: ", "ion#",
		"ion# help\r\nfirst page\r\n--More--(4%)[Press space to continue, 'q' to quit.]",
		"\r                        \rsecond page\r\nion#",
	})
	defer srv.Close()

	outs, err := Run(context.Background(), conn, RunOptions{
		Prompt: "ion#", User: "u", Pass: "p", Commands: []string{"help"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(outs) != 1 {
		t.Fatalf("outputs = %+v, want one", outs)
	}
	got := outs[0].Output
	for _, want := range []string{"first page", "second page"} {
		if !strings.Contains(got, want) {
			t.Errorf("output = %q, want it to contain %q", got, want)
		}
	}
	if strings.Contains(strings.ToLower(got), "press space") {
		t.Errorf("output = %q, want the pager line stripped", got)
	}
	if sent := srv.sent(); !strings.Contains(sent, "help\r ") {
		t.Errorf("sent = %q, want a bare space after the command", sent)
	}
}

func TestRunAnswersEachPageExactlyOnce(t *testing.T) {
	srv, conn := dialTestConn(t, []string{
		"login: ", "Password: ", "ion#",
		"ion# help\r\npage one\r\n--More--(4%)[Press space to continue, 'q' to quit.]",
		"\r      \rpage two\r\n--More--(50%)[Press space to continue, 'q' to quit.]",
		"\r      \rpage three\r\nion#",
	})
	defer srv.Close()

	outs, err := Run(context.Background(), conn, RunOptions{
		Prompt: "ion#", User: "u", Pass: "p", Commands: []string{"help"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if n := strings.Count(srv.sent(), " "); n != 2 {
		t.Errorf("sent %d spaces for two pages: %q", n, srv.sent())
	}
	for _, want := range []string{"page one", "page two", "page three"} {
		if !strings.Contains(outs[0].Output, want) {
			t.Errorf("output = %q, want it to contain %q", outs[0].Output, want)
		}
	}
}

func TestRunStopsWhenThePagerIsNotRecognised(t *testing.T) {
	srv, conn := dialTestConn(t, []string{
		"login: ", "Password: ", "ion#", "ion# help\r\nlots of text\r\n<<<hit a key>>>",
	})
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_, err := Run(ctx, conn, RunOptions{
		Prompt: "ion#", User: "u", Pass: "p", Commands: []string{"help"},
	})
	if err == nil {
		t.Fatal("Run() error = nil, want an unknown pager to time out")
	}
	for _, want := range []string{"help", "hit a key"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to mention %q", err, want)
		}
	}
}

func TestRunCanHaveThePagerScraperTurnedOff(t *testing.T) {
	srv, conn := dialTestConn(t, []string{
		"login: ", "Password: ", "ion#", "ion# help\r\nlots of text\r\n--More--(4%)[Press space to continue, 'q' to quit.]",
	})
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_, err := Run(ctx, conn, RunOptions{
		Prompt: "ion#", User: "u", Pass: "p", Commands: []string{"help"},
		Pagers: []string{},
	})
	if err == nil {
		t.Fatal("Run() error = nil, want no pager handling with an empty list")
	}
	if sent := srv.sent(); strings.Contains(sent, "help\r ") {
		t.Errorf("sent = %q, want no space sent with the scraper off", sent)
	}
}

func TestCleanDropsPagerLinesAndTheirErase(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "pager line and the spaces it is erased with",
			raw:  "ion# help\r\nfirst\r\npress space to continue\r      \rsecond\r\nion#",
			want: "first\nsecond",
		},
		{
			name: "output that merely mentions a pager stays",
			raw:  "ion# help\r\nset the pager with: terminal length 0\r\nion#",
			want: "set the pager with: terminal length 0",
		},
		{
			name: "text after an in-place erase is kept",
			raw:  "ion# help\r\n--more--\b\b\b   kept text\r\nion#",
			want: "kept text",
		},
		{
			// Word for word from an ion 1200-s on 6.3.4. It matches two of the
			// patterns, and cutting the later one left "--More--(4%)[" in
			// every paged command's output.
			name: "a real ION pager line goes entirely",
			raw:  "ion# help\r\nfirst\r\n--More--(4%)[Press space to continue, 'q' to quit.]\r      \rsecond\r\nion#",
			want: "first\nsecond",
		},
		{
			name: "content before the prompt on one line stays",
			raw:  "ion# help\r\nlast of the page --More--(4%)[Press space to continue, 'q' to quit.]\r\nion#",
			want: "last of the page",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clean(tt.raw, "ion#", "help", DefaultPagers); got != tt.want {
				t.Errorf("clean() = %q, want %q", got, tt.want)
			}
		})
	}
}
