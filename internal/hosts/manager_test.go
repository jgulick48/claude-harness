package hosts

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/jgulick48/claude-harness/internal/session"
	"github.com/jgulick48/claude-harness/internal/session/tmuxbackend"
	"github.com/jgulick48/claude-harness/internal/sshconn"
)

type fakeClient struct {
	done chan struct{}
	once sync.Once
	mu   sync.Mutex
	err  error
}

func newFakeClient() *fakeClient { return &fakeClient{done: make(chan struct{})} }

func (c *fakeClient) Done() <-chan struct{} { return c.done }
func (c *fakeClient) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}
func (c *fakeClient) Close() error { c.once.Do(func() { close(c.done) }); return nil }
func (c *fakeClient) drop(err error) {
	c.mu.Lock()
	c.err = err
	c.mu.Unlock()
	c.once.Do(func() { close(c.done) })
}

type fakeInner struct {
	id      string
	mu      sync.Mutex
	fn      func([]session.Session)
	closed  bool
	unsubs  int
	list    []session.Session
	tmuxBad bool
}

func (b *fakeInner) ID() string { return b.id }
func (b *fakeInner) Host(context.Context) session.Host {
	if b.tmuxBad {
		return session.Host{ID: b.id, State: session.HostUnavailable, Error: "tmux not found"}
	}
	return session.Host{ID: b.id, State: session.HostConnected, Home: "/home/x", TmuxVersion: "3.4"}
}
func (b *fakeInner) List(context.Context) ([]session.Session, error) { return b.list, nil }
func (b *fakeInner) Create(context.Context, session.CreateOptions) (session.Session, error) {
	return session.Session{ID: "$1"}, nil
}
func (b *fakeInner) Kill(context.Context, string) error           { return nil }
func (b *fakeInner) Rename(context.Context, string, string) error { return nil }
func (b *fakeInner) Attach(context.Context, string, int, int) (session.Terminal, error) {
	return nil, errors.New("no")
}
func (b *fakeInner) Subscribe(fn func([]session.Session)) func() {
	b.mu.Lock()
	b.fn = fn
	l := b.list
	b.mu.Unlock()
	fn(l)
	return func() { b.mu.Lock(); b.unsubs++; b.mu.Unlock() }
}
func (b *fakeInner) Close() error { b.mu.Lock(); b.closed = true; b.mu.Unlock(); return nil }
func (b *fakeInner) push(l []session.Session) {
	b.mu.Lock()
	fn := b.fn
	b.mu.Unlock()
	fn(l)
}
func (b *fakeInner) isClosed() bool { b.mu.Lock(); defer b.mu.Unlock(); return b.closed }

// fakes drives dial/newBackend behaviour per target.
type fakes struct {
	mu       sync.Mutex
	dialErr  map[string]error
	dials    []string
	clients  []*fakeClient
	inners   []*fakeInner
	trusted  [][2]string
	trustErr error
	block    chan struct{} // if non-nil, dial waits for it
}

func (f *fakes) dial(ctx context.Context, target string, _ sshconn.Options) (sshClient, error) {
	f.mu.Lock()
	block := f.block
	f.dials = append(f.dials, target)
	err := f.dialErr[target]
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	c := newFakeClient()
	f.mu.Lock()
	f.clients = append(f.clients, c)
	f.mu.Unlock()
	return c, nil
}

func (f *fakes) newBackend(_ context.Context, id, _ string, _ sshClient, _ tmuxbackend.Options) (session.Backend, error) {
	b := &fakeInner{id: id}
	f.mu.Lock()
	f.inners = append(f.inners, b)
	f.mu.Unlock()
	return b, nil
}

func (f *fakes) client(i int) *fakeClient { f.mu.Lock(); defer f.mu.Unlock(); return f.clients[i] }
func (f *fakes) inner(i int) *fakeInner   { f.mu.Lock(); defer f.mu.Unlock(); return f.inners[i] }
func (f *fakes) nDials() int              { f.mu.Lock(); defer f.mu.Unlock(); return len(f.dials) }

func (f *fakes) trustFn(file, addr string, _ ssh.PublicKey) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trusted = append(f.trusted, [2]string{file, addr})
	if f.trustErr != nil {
		return f.trustErr
	}
	// The key is now known: later dials succeed.
	delete(f.dialErr, "me@box")
	return nil
}

func newTest(t *testing.T, f *fakes, file string) *Manager {
	t.Helper()
	m, err := newTestErr(t, f, file)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func newTestErr(t *testing.T, f *fakes, file string) (*Manager, error) {
	t.Helper()
	if file == "" {
		file = filepath.Join(t.TempDir(), "sub", "hosts.json")
	}
	m := &Manager{opts: Options{File: file, SSH: sshconn.Options{KnownHostsFile: "/kh"}},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)), subs: map[int]func([]session.Host){},
		wake: make(chan struct{}, 1), done: make(chan struct{}),
		dial: f.dial, newBackend: f.newBackend, trust: f.trustFn}
	if err := m.init(); err != nil {
		return nil, err
	}
	t.Cleanup(func() { m.Close() })
	return m, nil
}

func newFakes() *fakes { return &fakes{dialErr: map[string]error{}} }

func waitState(t *testing.T, m *Manager, id, state string) session.Host {
	t.Helper()
	var h session.Host
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b, ok := m.Backend(id)
		if ok {
			h = b.Host(context.Background())
			if h.State == state {
				return h
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("host %s: state %q (err %q), want %q", id, h.State, h.Error, state)
	return h
}

func unknownKeyErr(t *testing.T) *sshconn.UnknownHostKeyError {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return &sshconn.UnknownHostKeyError{Address: "box", KeyType: k.Type(), Fingerprint: ssh.FingerprintSHA256(k), Key: k}
}

func TestPersistenceRoundTrip(t *testing.T) {
	f := newFakes()
	file := filepath.Join(t.TempDir(), "cfg", "hosts.json")
	m := newTest(t, f, file)
	if _, err := m.Add(context.Background(), "me@devbox.lan:2222", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Add(context.Background(), "alias2", "Second"); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(file)
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("file mode %v err %v", st, err)
		}
		dst, _ := os.Stat(filepath.Dir(file))
		if dst.Mode().Perm() != 0o700 {
			t.Fatalf("dir mode %v", dst.Mode())
		}
	}
	m.Close()

	f2 := newFakes()
	m2 := newTest(t, f2, file)
	bs := m2.Backends()
	if len(bs) != 2 || bs[0].ID() != "devbox-lan" || bs[1].ID() != "second" {
		t.Fatalf("backends %v", bs)
	}
	h := waitState(t, m2, "devbox-lan", session.HostConnected)
	if h.Name != "devbox.lan" || h.Target != "me@devbox.lan:2222" || h.Kind != session.KindSSH {
		t.Fatalf("host %+v", h)
	}
	waitState(t, m2, "second", session.HostConnected)
	if f2.nDials() != 2 {
		t.Fatalf("dials %d", f2.nDials())
	}
}

func TestMissingAndCorruptFile(t *testing.T) {
	f := newFakes()
	m := newTest(t, f, filepath.Join(t.TempDir(), "none.json"))
	if len(m.Backends()) != 0 {
		t.Fatal("expected no hosts")
	}
	bad := filepath.Join(t.TempDir(), "hosts.json")
	for _, content := range []string{"{not json", `{"version":9,"hosts":[]}`,
		`{"version":1,"hosts":[{"id":"local","name":"x","target":"x"}]}`,
		`{"version":1,"hosts":[{"id":"a","name":"x","target":"x"},{"id":"b","name":"y","target":"x"}]}`} {
		if err := os.WriteFile(bad, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := newTestErr(t, f, bad); err == nil {
			t.Fatalf("expected error for %q", content)
		}
		got, _ := os.ReadFile(bad)
		if string(got) != content {
			t.Fatalf("corrupt file was overwritten: %q", got)
		}
	}
}

func TestIDsAndDefaultNames(t *testing.T) {
	m := newTest(t, newFakes(), "")
	tests := []struct{ target, name, wantID, wantName string }{
		{"me@devbox.lan:2222", "", "devbox-lan", "devbox.lan"},
		{"devbox", "", "devbox", "devbox"},
		{"other@devbox", "", "devbox-2", "devbox"},
		{"x@[::1]:22", "", "1", "::1"},
		{"a1", "Local", "local-2", "Local"},
		{"a2", "My  Box!!", "my-box", "My  Box!!"},
		{"a3", "!!!", "a3", "!!!"},
	}
	for _, tc := range tests {
		h, err := m.Add(context.Background(), tc.target, tc.name)
		if err != nil {
			t.Fatalf("%v: %v", tc, err)
		}
		if h.ID != tc.wantID || h.Name != tc.wantName {
			t.Errorf("%s: got id %q name %q, want %q %q", tc.target, h.ID, h.Name, tc.wantID, tc.wantName)
		}
	}
}

func TestAddValidation(t *testing.T) {
	m := newTest(t, newFakes(), "")
	if _, err := m.Add(context.Background(), "me@box", ""); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"", "  ", "a b", "a\tb", "a\x00b", "me@box", "-oProxyCommand=x"} {
		_, err := m.Add(context.Background(), target, "")
		if !errors.Is(err, session.ErrBadRequest) {
			t.Errorf("target %q: err %v, want ErrBadRequest", target, err)
		}
	}
	if len(m.Backends()) != 1 {
		t.Fatal("rejected hosts were added")
	}
}

func TestUnknownKeyTrustFlow(t *testing.T) {
	f := newFakes()
	uk := unknownKeyErr(t)
	f.dialErr["me@box"] = uk
	m := newTest(t, f, "")
	h, err := m.Add(context.Background(), "me@box", "")
	if err != nil {
		t.Fatal(err)
	}
	h = waitState(t, m, h.ID, session.HostKeyUnknown)
	if h.HostKey == nil || h.HostKey.Fingerprint != uk.Fingerprint || h.HostKey.Address != "box" || h.HostKey.KeyType != uk.KeyType {
		t.Fatalf("hostKey %+v", h.HostKey)
	}
	if err := m.TrustHostKey(context.Background(), h.ID, "SHA256:wrong"); !errors.Is(err, session.ErrBadRequest) {
		t.Fatalf("mismatch: %v", err)
	}
	if len(f.trusted) != 0 {
		t.Fatal("trusted despite mismatch")
	}
	if err := m.TrustHostKey(context.Background(), "nope", "x"); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("unknown host: %v", err)
	}
	if err := m.TrustHostKey(context.Background(), h.ID, uk.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if f.trusted[0] != [2]string{"/kh", "box"} {
		t.Fatalf("trusted %v", f.trusted)
	}
	h = waitState(t, m, h.ID, session.HostConnected)
	if h.HostKey != nil || h.Home != "/home/x" {
		t.Fatalf("host %+v", h)
	}
	// No pending key any more.
	if err := m.TrustHostKey(context.Background(), h.ID, uk.Fingerprint); !errors.Is(err, session.ErrBadRequest) {
		t.Fatalf("no pending: %v", err)
	}
}

func TestChangedKeyAndDialError(t *testing.T) {
	f := newFakes()
	ch := &sshconn.HostKeyChangedError{Address: "box", KeyType: "ssh-ed25519", Fingerprint: "SHA256:x"}
	f.dialErr["a"] = ch
	f.dialErr["b"] = errors.New("auth failed")
	m := newTest(t, f, "")
	a, _ := m.Add(context.Background(), "a", "")
	b, _ := m.Add(context.Background(), "b", "")
	if h := waitState(t, m, a.ID, session.HostError); h.Error != ch.Error() || h.HostKey != nil {
		t.Fatalf("changed: %+v", h)
	}
	if h := waitState(t, m, b.ID, session.HostError); h.Error != "auth failed" {
		t.Fatalf("dial err: %+v", h)
	}
	// An error host rejects session ops and can be reconnected.
	be, _ := m.Backend(b.ID)
	if _, err := be.List(context.Background()); !errors.Is(err, session.ErrUnavailable) {
		t.Fatalf("list: %v", err)
	}
	f.mu.Lock()
	delete(f.dialErr, "b")
	f.mu.Unlock()
	if err := m.Connect(context.Background(), b.ID); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, b.ID, session.HostConnected)
	if err := m.Connect(context.Background(), "nope"); !errors.Is(err, session.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestDisconnectAndReconnect(t *testing.T) {
	f := newFakes()
	m := newTest(t, f, "")
	h, _ := m.Add(context.Background(), "me@box", "")
	waitState(t, m, h.ID, session.HostConnected)
	// Connect while connected is a no-op.
	if err := m.Connect(context.Background(), h.ID); err != nil {
		t.Fatal(err)
	}
	if f.nDials() != 1 {
		t.Fatalf("dials %d", f.nDials())
	}
	f.client(0).drop(errors.New("boom"))
	got := waitState(t, m, h.ID, session.HostDisconnected)
	if got.Error != "connection lost: boom" {
		t.Fatalf("error %q", got.Error)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !f.inner(0).isClosed() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !f.inner(0).isClosed() {
		t.Fatal("inner backend not closed")
	}
	time.Sleep(50 * time.Millisecond)
	if f.nDials() != 1 {
		t.Fatal("auto-reconnected")
	}
	be, _ := m.Backend(h.ID)
	if _, err := be.Create(context.Background(), session.CreateOptions{}); !errors.Is(err, session.ErrUnavailable) {
		t.Fatalf("create: %v", err)
	}
	if err := m.Connect(context.Background(), h.ID); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, h.ID, session.HostConnected)
	if _, err := be.Create(context.Background(), session.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestTmuxUnavailableReflected(t *testing.T) {
	f := newFakes()
	m := newTest(t, f, "")
	h, _ := m.Add(context.Background(), "me@box", "")
	waitState(t, m, h.ID, session.HostConnected)
	f.inner(0).tmuxBad = true
	got := waitState(t, m, h.ID, session.HostUnavailable)
	if got.Error != "tmux not found" || got.Name != "box" {
		t.Fatalf("%+v", got)
	}
}

func TestRemove(t *testing.T) {
	f := newFakes()
	file := filepath.Join(t.TempDir(), "hosts.json")
	m := newTest(t, f, file)
	h, _ := m.Add(context.Background(), "me@box", "")
	waitState(t, m, h.ID, session.HostConnected)
	be, _ := m.Backend(h.ID)
	if err := m.Remove(context.Background(), "local"); !errors.Is(err, session.ErrBadRequest) {
		t.Fatalf("local: %v", err)
	}
	if err := m.Remove(context.Background(), "nope"); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	if err := m.Remove(context.Background(), h.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Backend(h.ID); ok || len(m.Backends()) != 0 {
		t.Fatal("host still present")
	}
	if !f.inner(0).isClosed() {
		t.Fatal("inner not closed")
	}
	if _, err := be.List(context.Background()); !errors.Is(err, session.ErrUnavailable) {
		t.Fatalf("removed wrapper: %v", err)
	}
	saved, err := loadFile(file)
	if err != nil || len(saved) != 0 {
		t.Fatalf("saved %v %v", saved, err)
	}
}

func TestRemoveWhileConnecting(t *testing.T) {
	f := newFakes()
	f.block = make(chan struct{})
	m := newTest(t, f, "")
	h, _ := m.Add(context.Background(), "me@box", "")
	if got := waitState(t, m, h.ID, session.HostConnecting); got.State != session.HostConnecting {
		t.Fatal(got)
	}
	if err := m.Remove(context.Background(), h.ID); err != nil {
		t.Fatal(err)
	}
	close(f.block)
	time.Sleep(50 * time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.inners) != 0 {
		t.Fatal("backend created for removed host")
	}
}

func TestManagerSubscribe(t *testing.T) {
	f := newFakes()
	m := newTest(t, f, "")
	var mu sync.Mutex
	var snaps [][]session.Host
	ch := make(chan struct{}, 64)
	unsub := m.Subscribe(func(h []session.Host) {
		mu.Lock()
		snaps = append(snaps, h)
		mu.Unlock()
		ch <- struct{}{}
	})
	<-ch // immediate
	mu.Lock()
	if len(snaps) != 1 || len(snaps[0]) != 0 {
		t.Fatalf("initial %v", snaps)
	}
	mu.Unlock()

	last := func() []session.Host {
		mu.Lock()
		defer mu.Unlock()
		return snaps[len(snaps)-1]
	}
	waitLast := func(pred func([]session.Host) bool, what string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if pred(last()) {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("never saw: %s; last %+v", what, last())
	}
	h, _ := m.Add(context.Background(), "me@box", "")
	waitLast(func(l []session.Host) bool { return len(l) == 1 && l[0].State == session.HostConnected }, "connected")
	f.client(0).drop(nil)
	waitLast(func(l []session.Host) bool {
		return len(l) == 1 && l[0].State == session.HostDisconnected && l[0].Error == "connection lost"
	}, "disconnected")
	m.Remove(context.Background(), h.ID)
	waitLast(func(l []session.Host) bool { return len(l) == 0 }, "removed")
	unsub()
	mu.Lock()
	n := len(snaps)
	mu.Unlock()
	m.Add(context.Background(), "x@y", "")
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(snaps) != n {
		t.Fatal("notified after unsubscribe")
	}
}

func TestWrapperSubscribeAcrossReconnects(t *testing.T) {
	f := newFakes()
	m := newTest(t, f, "")
	h, _ := m.Add(context.Background(), "me@box", "")
	be, _ := m.Backend(h.ID)
	var mu sync.Mutex
	var got [][]session.Session
	unsub := be.Subscribe(func(l []session.Session) {
		mu.Lock()
		got = append(got, l)
		mu.Unlock()
	})
	defer unsub()
	lastIs := func(n int, id string) bool {
		mu.Lock()
		defer mu.Unlock()
		if len(got) == 0 {
			return false
		}
		l := got[len(got)-1]
		if n == 0 {
			return len(l) == 0 && l != nil
		}
		return len(l) == n && l[0].ID == id
	}
	waitFor := func(cond func() bool, what string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if cond() {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("timeout: %s; got %v", what, got)
	}
	waitState(t, m, h.ID, session.HostConnected)
	waitFor(func() bool { return f.nDials() == 1 && len(f.inners) == 1 }, "inner")
	f.inner(0).push([]session.Session{{ID: "$1"}})
	waitFor(func() bool { return lastIs(1, "$1") }, "first list")

	f.client(0).drop(nil)
	waitFor(func() bool { return lastIs(0, "") }, "empty on disconnect")
	// Stale inner pushes after disconnect are ignored.
	f.inner(0).push([]session.Session{{ID: "$stale"}})
	time.Sleep(20 * time.Millisecond)
	if !lastIs(0, "") {
		t.Fatal("stale inner delivered")
	}

	m.Connect(context.Background(), h.ID)
	waitState(t, m, h.ID, session.HostConnected)
	waitFor(func() bool { return len(f.inners) == 2 }, "second inner")
	f.inner(1).push([]session.Session{{ID: "$2"}})
	waitFor(func() bool { return lastIs(1, "$2") }, "list after reconnect")

	// New subscribers get the current list immediately.
	var first []session.Session
	u2 := be.Subscribe(func(l []session.Session) {
		if first == nil {
			first = l
		}
	})
	u2()
	if len(first) != 1 || first[0].ID != "$2" {
		t.Fatalf("late subscriber got %v", first)
	}
}

func TestCloseClosesEverything(t *testing.T) {
	f := newFakes()
	local := &fakeInner{id: "local"}
	m := newTest(t, f, "")
	m.mu.Lock()
	m.opts.Local = local
	m.mu.Unlock()
	h, _ := m.Add(context.Background(), "me@box", "")
	waitState(t, m, h.ID, session.HostConnected)
	if bs := m.Backends(); len(bs) != 2 || bs[0].ID() != "local" {
		t.Fatalf("backends %v", bs)
	}
	if _, ok := m.Backend("local"); !ok {
		t.Fatal("no local")
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if !local.isClosed() || !f.inner(0).isClosed() {
		t.Fatal("not closed")
	}
	if _, err := m.Add(context.Background(), "z", ""); err == nil || strings.Contains(err.Error(), "bad request") {
		t.Fatalf("add after close: %v", err)
	}
}
