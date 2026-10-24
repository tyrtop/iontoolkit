package runtime

import (
	"sync"

	"tyrtop.com/iontk/internal/sase/api"
)

type Clients struct {
	mu      sync.Mutex
	clients map[string]*api.Client
}

func newClients() *Clients { return &Clients{clients: map[string]*api.Client{}} }

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
	defer p.mu.Unlock()
	p.clients[tsg] = c
}

func (p *Clients) Drop(tsg string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.clients, tsg)
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
		rt.clients = newClients()
	}
	return rt.clients
}

func (rt *Runtime) ResetClients() {
	rt.mu.Lock()
	rt.clients = newClients()
	rt.mu.Unlock()
}
