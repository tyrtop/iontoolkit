package ionterm

import (
	"context"
	"slices"
	"sync"
	"time"
)

type Manager struct {
	mu       sync.Mutex
	sessions []*Session
	detached []*detachedSession
	active   int
	dirty    chan string
	lastEID  string
	lastName string
	hasLast  bool
}

type detachedSession struct {
	s     *Session
	since time.Time
	left  chan struct{}
}

func NewManager() *Manager {
	return &Manager{dirty: make(chan string, 1)}
}

func (m *Manager) Dirty() <-chan string {
	if m == nil {
		return nil
	}
	return m.dirty
}

func (m *Manager) notify(eid string) {
	select {
	case m.dirty <- eid:
	default:
	}
}

func (m *Manager) Open(ctx context.Context, d Dialer, eid, name string, cols, rows int, creds Credentials) (*Session, error) {
	s, err := m.Dial(ctx, d, eid, name, cols, rows, creds)
	if err != nil {
		return nil, err
	}
	m.Add(s)
	return s, nil
}

func (m *Manager) Dial(ctx context.Context, d Dialer, eid, name string, cols, rows int, creds Credentials) (*Session, error) {
	return Open(ctx, d, eid, name, cols, rows, creds, m.notify)
}

func (m *Manager) Add(s *Session) {
	m.mu.Lock()
	m.sessions = append(m.sessions, s)
	m.active = len(m.sessions) - 1
	m.mu.Unlock()

	m.notify(s.ElementID)
}

func (m *Manager) Count() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

func (m *Manager) All() []*Session {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Session, len(m.sessions))
	copy(out, m.sessions)
	return out
}

func (m *Manager) ActiveIndex() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active
}

func (m *Manager) Active() *Session {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active < 0 || m.active >= len(m.sessions) {
		return nil
	}
	return m.sessions[m.active]
}

func (m *Manager) Find(eid string) *Session {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		if s.ElementID == eid {
			return s
		}
	}
	return nil
}

func (m *Manager) IndexOf(eid string) int {
	if m == nil {
		return -1
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, s := range m.sessions {
		if s.ElementID == eid {
			return i
		}
	}
	return -1
}

func (m *Manager) Select(i int) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if i >= 0 && i < len(m.sessions) {
		m.active = i
	}
}

func (m *Manager) Next() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sessions) > 0 {
		m.active = (m.active + 1) % len(m.sessions)
	}
}

func (m *Manager) Prev() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if n := len(m.sessions); n > 0 {
		m.active = (m.active - 1 + n) % n
	}
}

func (m *Manager) CloseActive() {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.active < 0 || m.active >= len(m.sessions) {
		m.mu.Unlock()
		return
	}
	s := m.sessions[m.active]
	m.lastEID, m.lastName, m.hasLast = s.ElementID, s.Name, true
	m.sessions = append(m.sessions[:m.active], m.sessions[m.active+1:]...)
	if m.active >= len(m.sessions) {
		m.active = len(m.sessions) - 1
	}
	if m.active < 0 {
		m.active = 0
	}
	m.mu.Unlock()

	s.Close()
	m.notify(s.ElementID)
}

func (m *Manager) Close(eid string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	at := -1
	for i, s := range m.sessions {
		if s.ElementID == eid {
			at = i
			break
		}
	}
	if at < 0 {
		m.mu.Unlock()
		return false
	}
	s := m.sessions[at]
	m.lastEID, m.lastName, m.hasLast = s.ElementID, s.Name, true
	m.sessions = append(m.sessions[:at], m.sessions[at+1:]...)
	switch {
	case at < m.active:
		m.active--
	case m.active >= len(m.sessions):
		m.active = len(m.sessions) - 1
	}
	if m.active < 0 {
		m.active = 0
	}
	m.mu.Unlock()

	s.Close()
	m.notify(s.ElementID)
	return true
}

func (m *Manager) CloseDeadOn(eid string) int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	var gone []*Session
	kept := m.sessions[:0:0]
	for _, s := range m.sessions {
		if s.ElementID == eid && (s.Err() != nil || s.Ended() || s.Hung()) {
			m.lastEID, m.lastName, m.hasLast = s.ElementID, s.Name, true
			gone = append(gone, s)
			continue
		}
		kept = append(kept, s)
	}
	m.sessions = kept
	m.active = clampActive(len(m.sessions), m.active)
	for i, s := range m.sessions {
		if s.ElementID == eid {
			m.active = i
			break
		}
	}
	m.mu.Unlock()

	for _, s := range gone {
		s.Close()
	}
	if len(gone) > 0 {
		m.notify(eid)
	}
	return len(gone)
}

func clampActive(n, at int) int {
	if at >= n {
		at = n - 1
	}
	if at < 0 {
		at = 0
	}
	return at
}

func (m *Manager) Reap() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	var gone []*Session
	kept := m.sessions[:0:0]
	for i, s := range m.sessions {
		if !s.Ended() {
			kept = append(kept, s)
			continue
		}
		if i == m.active || !m.hasLast {
			m.lastEID, m.lastName, m.hasLast = s.ElementID, s.Name, true
		}
		gone = append(gone, s)
	}
	m.sessions = kept
	if m.active >= len(m.sessions) {
		m.active = len(m.sessions) - 1
	}
	if m.active < 0 {
		m.active = 0
	}

	detached := m.detached[:0:0]
	for _, d := range m.detached {
		if d.s.Ended() {
			close(d.left)
			gone = append(gone, d.s)
			continue
		}
		detached = append(detached, d)
	}
	m.detached = detached
	m.mu.Unlock()

	for _, s := range gone {
		s.Close()
	}
	return len(gone)
}

func (m *Manager) Detach() (*Session, bool) {
	if m == nil {
		return nil, false
	}
	m.mu.Lock()
	if m.active < 0 || m.active >= len(m.sessions) {
		m.mu.Unlock()
		return nil, false
	}
	s := m.sessions[m.active]
	m.sessions = append(m.sessions[:m.active], m.sessions[m.active+1:]...)
	m.detached = append(m.detached, newDetached(s))
	m.lastEID, m.lastName, m.hasLast = s.ElementID, s.Name, true
	if m.active >= len(m.sessions) {
		m.active = len(m.sessions) - 1
	}
	if m.active < 0 {
		m.active = 0
	}
	m.mu.Unlock()

	m.notify(s.ElementID)
	return s, true
}

func (m *Manager) AddDetached(s *Session) {
	m.mu.Lock()
	m.detached = append(m.detached, newDetached(s))
	m.lastEID, m.lastName, m.hasLast = s.ElementID, s.Name, true
	m.mu.Unlock()

	m.notify(s.ElementID)
}

func newDetached(s *Session) *detachedSession {
	return &detachedSession{s: s, since: time.Now(), left: make(chan struct{})}
}

func (m *Manager) Reattach() (*Session, bool) {
	if m == nil {
		return nil, false
	}
	m.mu.Lock()
	var s *Session
	var discard []*Session
	for len(m.detached) > 0 {
		cand := m.detached[len(m.detached)-1]
		m.detached = m.detached[:len(m.detached)-1]
		close(cand.left)
		if cand.s.Closed() || cand.s.Ended() || cand.s.Err() != nil {
			discard = append(discard, cand.s)
			continue
		}
		s = cand.s
		break
	}
	m.mu.Unlock()
	for _, d := range discard {
		d.Close()
	}
	if s == nil {
		return nil, false
	}

	m.mu.Lock()
	m.sessions = append(m.sessions, s)
	m.active = len(m.sessions) - 1
	m.mu.Unlock()

	m.notify(s.ElementID)
	return s, true
}

func (m *Manager) DetachedCount() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.detached)
}

func (m *Manager) Detached() []*Session {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Session, len(m.detached))
	for i, d := range m.detached {
		out[i] = d.s
	}
	return out
}

func (m *Manager) DetachedSince(s *Session) (time.Time, <-chan struct{}, bool) {
	if m == nil {
		return time.Time{}, nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.detached {
		if d.s == s {
			return d.since, d.left, true
		}
	}
	return time.Time{}, nil, false
}

func (m *Manager) CloseDetached(s *Session) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	at := slices.IndexFunc(m.detached, func(d *detachedSession) bool { return d.s == s })
	if at < 0 {
		m.mu.Unlock()
		return false
	}
	close(m.detached[at].left)
	m.detached = slices.Delete(m.detached, at, at+1)
	m.mu.Unlock()

	s.Close()
	m.notify(s.ElementID)
	return true
}

func (m *Manager) LastClosed() (string, string, bool) {
	if m == nil {
		return "", "", false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastEID, m.lastName, m.hasLast
}

func (m *Manager) CloseAll() {
	if m == nil {
		return
	}
	m.mu.Lock()
	all := append([]*Session(nil), m.sessions...)
	for _, d := range m.detached {
		close(d.left)
		all = append(all, d.s)
	}
	m.sessions, m.detached = nil, nil
	m.active = 0
	m.mu.Unlock()

	for _, s := range all {
		s.Close()
	}
}
