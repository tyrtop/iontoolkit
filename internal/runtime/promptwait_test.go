package runtime

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"tyrtop.com/iontk/internal/ionterm"
	"tyrtop.com/iontk/internal/sase/api"
)

type slowLogin struct {
	prompt    string
	answer    string
	hangUp    bool
	overprint string

	out  chan []byte
	pass chan struct{}
	done chan struct{}
	once sync.Once

	mu       sync.Mutex
	line     []byte
	lines    int
	prompted bool
	early    []string
}

func newSlowLogin(name, answer string, hangUp bool) *slowLogin {
	c := &slowLogin{prompt: name + "# ", answer: answer, hangUp: hangUp,
		out: make(chan []byte, 16), pass: make(chan struct{}), done: make(chan struct{})}
	c.out <- []byte("login: ")
	return c
}

func (c *slowLogin) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	select {
	case b := <-c.out:
		return websocket.MessageText, b, nil
	case <-c.done:
		return 0, nil, io.EOF
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	}
}

func (c *slowLogin) Write(_ context.Context, _ websocket.MessageType, p []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, b := range p {
		if b != '\r' {
			c.line = append(c.line, b)
			continue
		}
		typed := string(c.line)
		c.line = c.line[:0]
		c.lines++
		switch {
		case c.lines == 1:
			c.out <- []byte("Password: ")
		case c.lines == 2:
			close(c.pass)
			if c.hangUp {
				c.once.Do(func() { close(c.done) })
			}
		case !c.prompted:
			c.early = append(c.early, typed)
		case c.overprint != "":
			c.out <- []byte(typed + "\r\x1b[2K\r\n" + c.overprint + "\r\n\r\n" + c.prompt + typed + "\r\n" + c.answer + "\r\n" + c.prompt)
		default:
			c.out <- []byte(typed + "\r\n" + c.answer + "\r\n" + c.prompt)
		}
	}
	return nil
}

func (c *slowLogin) Ping(context.Context) error { return nil }

func (c *slowLogin) Close(websocket.StatusCode, string) error {
	c.once.Do(func() { close(c.done) })
	return nil
}

func (c *slowLogin) showPrompt() {
	c.mu.Lock()
	c.prompted = true
	c.mu.Unlock()
	c.out <- []byte("\r\nWelcome\r\n" + c.prompt)
}

func (c *slowLogin) typedEarly() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.early...)
}

type dialThis struct{ conn ionterm.Conn }

func (d dialThis) DialToolkit(context.Context, string, int, int) (ionterm.Conn, error) {
	return d.conn, nil
}

func scriptedSession(t *testing.T, conn ionterm.Conn) (*Runtime, string) {
	t.Helper()
	rt := deviceRuntime(t)
	rt.mu.Lock()
	rt.dev.Dialer = func(*api.Client) ionterm.Dialer { return dialThis{conn} }
	rt.mu.Unlock()
	var eid string
	for _, el := range rt.Elements() {
		if el.Name == "acme-rtr-01" {
			eid = el.ID
		}
	}
	if err := rt.Exec(&Call{Ctx: context.Background(), Args: map[string]string{"elements": eid}}, "session.open"); err != nil {
		t.Fatalf("session.open: %v", err)
	}
	t.Cleanup(rt.Sessions().CloseAll)
	return rt, eid
}

func TestASendTypesNothingUntilThePromptIsUp(t *testing.T) {
	conn := newSlowLogin("acme-rtr-01", "Serial : FAKE0001", false)
	rt, eid := scriptedSession(t, conn)
	type answer struct {
		out SentLine
		err error
	}
	got := make(chan answer, 1)
	go func() {
		c := &Call{Ctx: context.Background(), Args: map[string]string{"element": eid, "text": "dump device status"}}
		err := rt.Exec(c, "session.send")
		out, _ := c.Result.(SentLine)
		got <- answer{out, err}
	}()
	<-conn.pass
	conn.showPrompt()
	a := <-got
	if out := a.out.Output; a.err != nil || out != "Serial : FAKE0001" {
		t.Errorf("a send on a session still logging in = %q, %v, want the answer once the prompt was up", out, a.err)
	}
	if early := conn.typedEarly(); len(early) != 0 {
		t.Errorf("typed %q before the prompt came up, which the ION throws away", early)
	}
}

func TestASendFailsAsSoonAsTheSessionEndsBeforeItsPrompt(t *testing.T) {
	conn := newSlowLogin("acme-rtr-01", "", true)
	rt, eid := scriptedSession(t, conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := rt.Exec(&Call{Ctx: ctx, Args: map[string]string{"element": eid, "text": "dump device status"}}, "session.send")
	if err == nil || !strings.Contains(err.Error(), "acme-rtr-01 never got to its prompt: the session ended") || ctx.Err() != nil {
		t.Errorf("a session that hung up after the password = %v, want the send to fail when the session ended", err)
	}
	if early := conn.typedEarly(); len(early) != 0 {
		t.Errorf("typed %q into a session that never showed its prompt", early)
	}
}

func TestASendFindsItsAnswerWhenTheBannerLandsOnTopOfThePrompt(t *testing.T) {
	conn := newSlowLogin("acme-rtr-01", "bad SOURCE", false)
	conn.overprint = "****\r\nUse the config command carefully.\r\n****"
	rt, eid := scriptedSession(t, conn)
	go func() {
		<-conn.pass
		conn.showPrompt()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := &Call{Ctx: ctx, DoIt: true, Args: map[string]string{"element": eid, "text": "debug bw-test src-interface 4"}}
	err := rt.Exec(c, "session.send")
	out, _ := c.Result.(SentLine)
	if got := out.Output; err != nil || got != "bad SOURCE" {
		t.Errorf("an ION that printed its banner over the prompt = %q, %v, want the answer below the redrawn line", got, err)
	}
}
