package runtime

import (
	"errors"
	"sync"

	"tyrtop.com/iontk/internal/logging"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/saseauth"
)

type Clients struct {
	mu      sync.Mutex
	clients map[string]*api.Client
	gone    map[*api.Client]chan struct{}
	onNew   func(string, *api.Client)
}

func newClients(onNew func(string, *api.Client)) *Clients {
	return &Clients{clients: map[string]*api.Client{}, gone: map[*api.Client]chan struct{}{}, onNew: onNew}
}

func (p *Clients) Get(tsg string) *api.Client {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.clients[tsg]
}

func (p *Clients) Set(tsg string, c *api.Client) {
	if p == nil {
		return
	}
	p.mu.Lock()
	fresh := c != nil && p.gone[c] == nil
	if fresh {
		p.gone[c] = make(chan struct{})
	}
	old := p.clients[tsg]
	p.clients[tsg] = c
	p.mu.Unlock()
	if old != c {
		old.Close()
	}
	if fresh && p.onNew != nil {
		go p.onNew(tsg, c)
	}
}

func (p *Clients) Gone(c *api.Client) <-chan struct{} {
	if p == nil || c == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.gone[c]
}

func (p *Clients) markGone(c *api.Client) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if ch := p.gone[c]; ch != nil {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
}

func (p *Clients) Drop(tsg string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	c := p.clients[tsg]
	delete(p.clients, tsg)
	p.mu.Unlock()
	c.Close()
}

func (p *Clients) closeAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.clients {
		c.Close()
	}
}

func (p *Clients) Count() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.clients)
}

func (rt *Runtime) Clients() *Clients {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.clients == nil {
		rt.clients = newClients(rt.watchToken)
	}
	return rt.clients
}

func (rt *Runtime) ResetClients() {
	rt.StopSweeps()
	rt.mu.Lock()
	old := rt.clients
	rt.clients = newClients(rt.watchToken)
	rt.mu.Unlock()
	if old != nil {
		old.closeAll()
	}
}

func (rt *Runtime) watchToken(tsg string, c *api.Client) {
	ch := c.TokenErrors()
	if ch == nil {
		return
	}
	for err := range ch {
		if errors.Is(err, saseauth.ErrInteractiveRequired) {
			rt.signInLost(tsg, "sign-in expired", false)
			continue
		}
		logging.L().Warn("token refresh", "err", err)
	}
	if c.RefreshStopped() {
		rt.signInLost(tsg, "token expired — sign in again", true)
		rt.Clients().markGone(c)
	}
}
