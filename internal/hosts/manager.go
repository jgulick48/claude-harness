// Package hosts implements session.HostManager: the persisted set of SSH
// hosts plus the local machine, and each SSH host's connection lifecycle.
package hosts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/jgulick48/claude-harness/internal/session"
	"github.com/jgulick48/claude-harness/internal/session/tmuxbackend"
	"github.com/jgulick48/claude-harness/internal/sshconn"
)

const localID = "local"

// Options configures a Manager.
type Options struct {
	// File is the hosts file; default <os.UserConfigDir()>/claude-harness/hosts.json.
	File string
	// Local is the always-present local backend (id "local"); may be nil in tests.
	Local session.Backend
	// SSH options used for every SSH host (config file, known_hosts, agent...).
	SSH sshconn.Options
	// TmuxOptions are applied to every SSH host's tmux backend.
	TmuxOptions tmuxbackend.Options
	Logger      *slog.Logger
	// Dial and NewBackend default to sshconn.Dial and tmuxbackend.NewSSH.
	Dial       func(ctx context.Context, target string, o sshconn.Options) (*sshconn.Client, error)
	NewBackend func(ctx context.Context, id, name string, c *sshconn.Client, o tmuxbackend.Options) (session.Backend, error)
}

// Manager implements session.HostManager.
type Manager struct {
	opts   Options
	file   string
	logger *slog.Logger

	// Seams (adapters over Options.Dial / Options.NewBackend; unit tests
	// replace them with fakes).
	dial       func(ctx context.Context, target string, o sshconn.Options) (sshClient, error)
	newBackend func(ctx context.Context, id, name string, c sshClient, o tmuxbackend.Options) (session.Backend, error)
	trust      func(knownHostsFile, address string, key ssh.PublicKey) error
	rs         retrySeams

	mu     sync.Mutex
	hosts  []*sshHost // insertion order
	closed bool

	subMu   sync.Mutex // guards subs; also serializes snapshots and callbacks
	subs    map[int]func([]session.Host)
	nextSub int

	wake chan struct{}
	done chan struct{}
	wg   sync.WaitGroup
}

var _ session.HostManager = (*Manager)(nil)

// New loads the hosts file (a missing file means no hosts, a corrupt one is an
// error and is left untouched) and starts connecting every host in the background.
func New(opts Options) (*Manager, error) {
	m := &Manager{opts: opts, logger: opts.Logger, subs: map[int]func([]session.Host){},
		wake: make(chan struct{}, 1), done: make(chan struct{}), trust: sshconn.TrustHostKey}
	if m.logger == nil {
		m.logger = slog.Default()
	}
	dial := opts.Dial
	if dial == nil {
		dial = sshconn.Dial
	}
	m.dial = func(ctx context.Context, t string, o sshconn.Options) (sshClient, error) {
		c, err := dial(ctx, t, o)
		if err != nil {
			return nil, err
		}
		return c, nil
	}
	nb := opts.NewBackend
	if nb == nil {
		nb = func(ctx context.Context, id, name string, c *sshconn.Client, o tmuxbackend.Options) (session.Backend, error) {
			return tmuxbackend.NewSSH(ctx, id, name, c, o)
		}
	}
	m.newBackend = func(ctx context.Context, id, name string, c sshClient, o tmuxbackend.Options) (session.Backend, error) {
		sc, ok := c.(*sshconn.Client)
		if !ok {
			return nil, fmt.Errorf("unexpected client type %T", c)
		}
		return nb(ctx, id, name, sc, o)
	}
	return m, m.init()
}

func (m *Manager) init() error {
	m.file = m.opts.File
	if m.file == "" {
		f, err := defaultFile()
		if err != nil {
			return err
		}
		m.file = f
	}
	saved, err := loadFile(m.file)
	if err != nil {
		return err
	}
	for _, fh := range saved {
		m.hosts = append(m.hosts, newSSHHost(m, fh.ID, fh.Name, fh.Target))
	}
	m.wg.Add(1)
	go m.dispatch()
	for _, h := range m.hosts {
		h.start(true)
	}
	return nil
}

func (m *Manager) knownHostsFile() string {
	if f := m.opts.SSH.KnownHostsFile; f != "" {
		return f
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".ssh", "known_hosts")
	}
	return filepath.Join(home, ".ssh", "known_hosts")
}

// Backends returns local first, then SSH hosts in insertion order.
func (m *Manager) Backends() []session.Backend {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]session.Backend, 0, len(m.hosts)+1)
	if m.opts.Local != nil {
		out = append(out, m.opts.Local)
	}
	for _, h := range m.hosts {
		out = append(out, h)
	}
	return out
}

func (m *Manager) Backend(id string) (session.Backend, bool) {
	if id == localID {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.opts.Local, m.opts.Local != nil
	}
	if h := m.find(id); h != nil {
		return h, true
	}
	return nil, false
}

func (m *Manager) find(id string) *sshHost {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, h := range m.hosts {
		if h.id == id {
			return h
		}
	}
	return nil
}

func (m *Manager) persistLocked() error {
	fh := make([]fileHost, 0, len(m.hosts))
	for _, h := range m.hosts {
		fh = append(fh, fileHost{ID: h.id, Name: h.name, Target: h.target})
	}
	return saveFile(m.file, fh)
}

func (m *Manager) Add(ctx context.Context, target, name string) (session.Host, error) {
	target = strings.TrimSpace(target)
	name = strings.TrimSpace(name)
	if err := validTarget(target); err != nil {
		return session.Host{}, fmt.Errorf("%w: %v", session.ErrBadRequest, err)
	}
	if name == "" {
		name = hostPart(target)
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return session.Host{}, fmt.Errorf("%w: host manager is closed", session.ErrUnavailable)
	}
	for _, h := range m.hosts {
		if h.target == target {
			m.mu.Unlock()
			return session.Host{}, fmt.Errorf("%w: host %q already exists as %q", session.ErrBadRequest, target, h.id)
		}
	}
	taken := map[string]bool{localID: true}
	for _, h := range m.hosts {
		taken[h.id] = true
	}
	base := slug(name)
	if base == "" {
		base = slug(hostPart(target))
	}
	if base == "" {
		base = "host"
	}
	id := base
	for i := 2; taken[id]; i++ {
		id = fmt.Sprintf("%s-%d", base, i)
	}
	h := newSSHHost(m, id, name, target)
	m.hosts = append(m.hosts, h)
	if err := m.persistLocked(); err != nil {
		m.hosts = m.hosts[:len(m.hosts)-1]
		m.mu.Unlock()
		return session.Host{}, fmt.Errorf("save hosts: %w", err)
	}
	m.mu.Unlock()
	h.start(true)
	return h.Host(ctx), nil
}

func (m *Manager) Remove(ctx context.Context, id string) error {
	if id == localID {
		return fmt.Errorf("%w: the local host can't be removed", session.ErrBadRequest)
	}
	m.mu.Lock()
	idx := -1
	for i, h := range m.hosts {
		if h.id == id {
			idx = i
		}
	}
	if idx < 0 {
		m.mu.Unlock()
		return fmt.Errorf("%w: unknown host: %s", session.ErrNotFound, id)
	}
	h := m.hosts[idx]
	m.hosts = append(m.hosts[:idx:idx], m.hosts[idx+1:]...)
	if err := m.persistLocked(); err != nil {
		m.hosts = append(m.hosts[:idx:idx], append([]*sshHost{h}, m.hosts[idx:]...)...)
		m.mu.Unlock()
		return fmt.Errorf("save hosts: %w", err)
	}
	m.mu.Unlock()
	h.shutdown()
	m.notify()
	return nil
}

func (m *Manager) Connect(ctx context.Context, id string) error {
	if id == localID {
		return fmt.Errorf("%w: the local host is always connected", session.ErrBadRequest)
	}
	h := m.find(id)
	if h == nil {
		return fmt.Errorf("%w: unknown host: %s", session.ErrNotFound, id)
	}
	h.start(true) // no-op while connecting or connected; cancels a pending retry
	return nil
}

func (m *Manager) TrustHostKey(ctx context.Context, id, fingerprint string) error {
	h := m.find(id)
	if h == nil {
		return fmt.Errorf("%w: unknown host: %s", session.ErrNotFound, id)
	}
	h.mu.Lock()
	key := h.key
	state := h.state
	h.mu.Unlock()
	if state != session.HostKeyUnknown || key == nil {
		return fmt.Errorf("%w: host %s has no pending host key", session.ErrBadRequest, id)
	}
	if fingerprint != key.Fingerprint {
		return fmt.Errorf("%w: fingerprint does not match the pending host key", session.ErrBadRequest)
	}
	if err := m.trust(m.knownHostsFile(), key.Address, key.Key); err != nil {
		return fmt.Errorf("trust host key: %w", err)
	}
	h.start(true)
	return nil
}

// Subscribe calls fn with all hosts immediately and whenever a host's state or
// the host set changes. Callbacks run outside the manager's locks, are
// serialized, and must not block (hand off to another goroutine); they may
// call back into the manager except Subscribe's unsubscribe from within fn.
// Rapid changes may be coalesced: fn always ends with the latest state.
func (m *Manager) Subscribe(fn func([]session.Host)) func() {
	m.subMu.Lock()
	n := m.nextSub
	m.nextSub++
	m.subs[n] = fn
	snap := m.snapshot()
	fn(snap)
	m.subMu.Unlock()
	return func() {
		m.subMu.Lock()
		delete(m.subs, n)
		m.subMu.Unlock()
	}
}

func (m *Manager) snapshot() []session.Host {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	bs := m.Backends()
	out := make([]session.Host, 0, len(bs))
	for _, b := range bs {
		out = append(out, b.Host(ctx))
	}
	return out
}

func (m *Manager) notify() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Manager) dispatch() {
	defer m.wg.Done()
	for {
		select {
		case <-m.done:
			return
		case <-m.wake:
		}
		m.subMu.Lock()
		if len(m.subs) > 0 {
			snap := m.snapshot()
			for _, fn := range m.subs {
				fn(snap)
			}
		}
		m.subMu.Unlock()
	}
}

// Close closes every host and the local backend.
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	hosts := m.hosts
	m.mu.Unlock()
	for _, h := range hosts {
		h.shutdown()
	}
	close(m.done)
	m.wg.Wait()
	var err error
	if m.opts.Local != nil {
		err = errors.Join(err, m.opts.Local.Close())
	}
	return err
}

// ConfigHosts implements session.ConfigHostLister: the concrete hosts of
// ~/.ssh/config, with Added set when a saved host's target is that alias.
func (m *Manager) ConfigHosts(ctx context.Context) ([]session.ConfigHost, error) {
	rs, err := sshconn.ListConfigHosts(m.opts.SSH)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	added := map[string]bool{}
	for _, h := range m.hosts {
		added[h.target] = true
	}
	m.mu.Unlock()
	out := make([]session.ConfigHost, 0, len(rs))
	for _, r := range rs {
		out = append(out, session.ConfigHost{Alias: r.Target, HostName: r.HostName, User: r.User,
			Port: r.Port, Added: added[r.Target]})
	}
	return out, nil
}

var _ session.ConfigHostLister = (*Manager)(nil)
