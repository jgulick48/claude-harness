package hosts

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/jgulick48/claude-harness/internal/session"
	"github.com/jgulick48/claude-harness/internal/sshconn"
)

// sshClient is the part of *sshconn.Client the manager uses; it exists so
// unit tests can supply fakes.
type sshClient interface {
	Done() <-chan struct{}
	Err() error
	Close() error
}

// sshHost is the stable session.Backend for one SSH host. The API holds it for
// the host's whole life; it delegates to the current inner backend while
// connected and reports ErrUnavailable otherwise.
//
// Locking: mu guards connection state; fanMu guards the subscriber list and
// serializes deliveries. They are never held together, and neither is held
// while calling the inner backend or subscriber callbacks of the manager.
type sshHost struct {
	m      *Manager
	id     string
	name   string
	target string

	mu     sync.Mutex
	state  string
	errMsg string
	key    *sshconn.UnknownHostKeyError
	inner  session.Backend
	client sshClient
	cancel context.CancelFunc
	gen    int
	closed bool

	fanMu     sync.Mutex
	subs      map[int]func([]session.Session)
	nextSub   int
	last      []session.Session
	activeSub int // generation whose inner Subscribe feeds subscribers; 0 = none
}

func newSSHHost(m *Manager, id, name, target string) *sshHost {
	return &sshHost{m: m, id: id, name: name, target: target, state: session.HostDisconnected,
		subs: map[int]func([]session.Session){}}
}

func (h *sshHost) ID() string { return h.id }

func (h *sshHost) Host(ctx context.Context) session.Host {
	h.mu.Lock()
	host := session.Host{ID: h.id, Name: h.name, Kind: session.KindSSH, Target: h.target,
		State: h.state, Error: h.errMsg}
	inner := h.inner
	if h.key != nil && h.state == session.HostKeyUnknown {
		host.HostKey = &session.HostKeyPrompt{Address: h.key.Address, KeyType: h.key.KeyType, Fingerprint: h.key.Fingerprint}
	}
	h.mu.Unlock()
	if host.State == session.HostConnected && inner != nil {
		ih := inner.Host(ctx)
		host.State, host.Error = ih.State, ih.Error
		host.Home, host.TmuxVersion = ih.Home, ih.TmuxVersion
	}
	return host
}

func (h *sshHost) current() (session.Backend, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.inner == nil {
		msg := "host " + h.id + " is " + h.state
		if h.errMsg != "" {
			msg += ": " + h.errMsg
		}
		return nil, fmt.Errorf("%w: %s", session.ErrUnavailable, msg)
	}
	return h.inner, nil
}

func (h *sshHost) List(ctx context.Context) ([]session.Session, error) {
	b, err := h.current()
	if err != nil {
		return nil, err
	}
	return b.List(ctx)
}

func (h *sshHost) Create(ctx context.Context, o session.CreateOptions) (session.Session, error) {
	b, err := h.current()
	if err != nil {
		return session.Session{}, err
	}
	return b.Create(ctx, o)
}

func (h *sshHost) Kill(ctx context.Context, id string) error {
	b, err := h.current()
	if err != nil {
		return err
	}
	return b.Kill(ctx, id)
}

func (h *sshHost) Rename(ctx context.Context, id, name string) error {
	b, err := h.current()
	if err != nil {
		return err
	}
	return b.Rename(ctx, id, name)
}

func (h *sshHost) Attach(ctx context.Context, id string, cols, rows int) (session.Terminal, error) {
	b, err := h.current()
	if err != nil {
		return nil, err
	}
	return b.Attach(ctx, id, cols, rows)
}

// Subscribe delivers the current list immediately ([] while not connected),
// then every change, including [] on disconnect. Subscriptions survive
// reconnects.
func (h *sshHost) Subscribe(fn func([]session.Session)) func() {
	h.fanMu.Lock()
	n := h.nextSub
	h.nextSub++
	h.subs[n] = fn
	last := h.last
	if last == nil {
		last = []session.Session{}
	}
	fn(last)
	h.fanMu.Unlock()
	return func() {
		h.fanMu.Lock()
		delete(h.subs, n)
		h.fanMu.Unlock()
	}
}

// Close shuts this host down for good; the manager normally does this.
func (h *sshHost) Close() error {
	h.shutdown()
	return nil
}

// deliver fans a list out to subscribers if gen is still the active inner
// subscription (gen 0 forces delivery, used for the empty list on disconnect).
func (h *sshHost) deliver(gen int, list []session.Session) {
	if list == nil {
		list = []session.Session{}
	}
	h.fanMu.Lock()
	defer h.fanMu.Unlock()
	if gen != 0 && gen != h.activeSub {
		return
	}
	h.last = list
	for _, fn := range h.subs {
		fn(list)
	}
}

// deactivate stops the inner feed and tells subscribers the list is empty.
func (h *sshHost) deactivate() {
	h.fanMu.Lock()
	h.activeSub = 0
	h.fanMu.Unlock()
	h.deliver(0, nil)
}

// start begins a connection attempt unless one is running or established.
func (h *sshHost) start() {
	h.mu.Lock()
	if h.closed || h.state == session.HostConnecting || h.state == session.HostConnected {
		h.mu.Unlock()
		return
	}
	h.gen++
	gen := h.gen
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.state, h.errMsg, h.key = session.HostConnecting, "", nil
	h.mu.Unlock()
	h.m.notify()
	go h.connect(ctx, gen)
}

func (h *sshHost) fail(gen int, state, msg string, key *sshconn.UnknownHostKeyError) {
	h.mu.Lock()
	if gen != h.gen || h.closed {
		h.mu.Unlock()
		return
	}
	h.state, h.errMsg, h.key = state, msg, key
	h.mu.Unlock()
	h.m.notify()
}

func (h *sshHost) connect(ctx context.Context, gen int) {
	m := h.m
	c, err := m.dial(ctx, h.target, m.opts.SSH)
	if err != nil {
		var unknown *sshconn.UnknownHostKeyError
		var changed *sshconn.HostKeyChangedError
		switch {
		case errors.As(err, &unknown):
			h.fail(gen, session.HostKeyUnknown, "", unknown)
		case errors.As(err, &changed):
			h.fail(gen, session.HostError, changed.Error(), nil)
		default:
			h.fail(gen, session.HostError, err.Error(), nil)
		}
		return
	}
	if ctx.Err() != nil { // removed or closed while dialing
		_ = c.Close()
		return
	}
	inner, err := m.newBackend(ctx, h.id, h.name, c, m.opts.TmuxOptions)
	if err != nil {
		_ = c.Close()
		h.fail(gen, session.HostError, err.Error(), nil)
		return
	}
	h.mu.Lock()
	if gen != h.gen || h.closed {
		h.mu.Unlock()
		_ = inner.Close()
		_ = c.Close()
		return
	}
	h.inner, h.client = inner, c
	h.state, h.errMsg, h.key = session.HostConnected, "", nil
	h.mu.Unlock()

	h.fanMu.Lock()
	h.activeSub = gen
	h.fanMu.Unlock()
	// Subscribe outside fanMu: the inner backend calls back immediately.
	unsub := inner.Subscribe(func(l []session.Session) { h.deliver(gen, l) })
	h.m.notify()

	select {
	case <-c.Done():
	case <-ctx.Done():
		unsub()
		return
	}
	unsub()
	msg := "connection lost"
	if err := c.Err(); err != nil {
		msg += ": " + err.Error()
	}
	h.mu.Lock()
	if gen != h.gen || h.closed {
		h.mu.Unlock()
		return
	}
	h.inner, h.client = nil, nil
	h.state, h.errMsg = session.HostDisconnected, msg
	h.mu.Unlock()
	h.deactivate()
	_ = inner.Close()
	_ = c.Close()
	h.m.notify()
}

// shutdown cancels any attempt, closes the connection and marks the host closed.
func (h *sshHost) shutdown() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	h.gen++
	if h.cancel != nil {
		h.cancel()
	}
	inner, client := h.inner, h.client
	h.inner, h.client = nil, nil
	h.state, h.errMsg, h.key = session.HostDisconnected, "removed", nil
	h.mu.Unlock()
	h.deactivate()
	if inner != nil {
		_ = inner.Close()
	}
	if client != nil {
		_ = client.Close()
	}
}
