package api

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jgulick48/claude-harness/internal/session"
)

// fakeHM is a session.HostManager over fake backends.
type fakeHM struct {
	mu       sync.Mutex
	backends []session.Backend
	subs     map[int]func([]session.Host)
	next     int
	calls    []string
	err      error
}

func newFakeHM(bs ...*fakeBackend) *fakeHM {
	hm := &fakeHM{subs: map[int]func([]session.Host){}}
	for _, b := range bs {
		hm.backends = append(hm.backends, b)
	}
	return hm
}

func (h *fakeHM) hostsLocked() []session.Host {
	out := []session.Host{}
	for _, b := range h.backends {
		out = append(out, b.Host(context.Background()))
	}
	return out
}

// set replaces the backend set and notifies subscribers.
func (h *fakeHM) set(bs ...session.Backend) {
	h.mu.Lock()
	h.backends = bs
	hosts := h.hostsLocked()
	fns := make([]func([]session.Host), 0, len(h.subs))
	for _, fn := range h.subs {
		fns = append(fns, fn)
	}
	h.mu.Unlock()
	for _, fn := range fns {
		fn(hosts)
	}
}

func (h *fakeHM) Backends() []session.Backend {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]session.Backend(nil), h.backends...)
}
func (h *fakeHM) Backend(id string) (session.Backend, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, b := range h.backends {
		if b.ID() == id {
			return b, true
		}
	}
	return nil, false
}
func (h *fakeHM) rec(s string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, s)
	return h.err
}
func (h *fakeHM) Add(_ context.Context, target, name string) (session.Host, error) {
	if err := h.rec("add " + target + "|" + name); err != nil {
		return session.Host{}, err
	}
	return session.Host{ID: "devbox", Name: "devbox", Kind: session.KindSSH, Target: target, State: session.HostConnecting}, nil
}
func (h *fakeHM) Remove(_ context.Context, id string) error  { return h.rec("remove " + id) }
func (h *fakeHM) Connect(_ context.Context, id string) error { return h.rec("connect " + id) }
func (h *fakeHM) TrustHostKey(_ context.Context, id, fp string) error {
	return h.rec("trust " + id + " " + fp)
}
func (h *fakeHM) Subscribe(fn func([]session.Host)) func() {
	h.mu.Lock()
	n := h.next
	h.next++
	h.subs[n] = fn
	hosts := h.hostsLocked()
	h.mu.Unlock()
	fn(hosts)
	return func() { h.mu.Lock(); delete(h.subs, n); h.mu.Unlock() }
}
func (h *fakeHM) Close() error { return nil }

func (h *fakeHM) nSubs() int { h.mu.Lock(); defer h.mu.Unlock(); return len(h.subs) }

func TestHostMethods(t *testing.T) {
	hm := newFakeHM(newFakeBackend())
	c, ctx := connect2(t, hm)

	m := rpc(t, ctx, c, `{"type":"hosts.add","id":1,"target":"me@devbox","name":"Dev"}`)
	if m["type"] != "result" || m["result"].(map[string]any)["id"] != "devbox" || m["result"].(map[string]any)["target"] != "me@devbox" {
		t.Fatal(m)
	}
	for i, r := range []string{
		`{"type":"hosts.remove","id":2,"host":"devbox"}`,
		`{"type":"hosts.connect","id":3,"host":"devbox"}`,
		`{"type":"hosts.trustKey","id":4,"host":"devbox","fingerprint":"SHA256:abc"}`,
	} {
		m = rpc(t, ctx, c, r)
		if m["type"] != "result" || m["result"] != nil || m["id"] != float64(i+2) {
			t.Fatal(m)
		}
	}
	hm.mu.Lock()
	got := fmt.Sprint(hm.calls)
	hm.mu.Unlock()
	if want := "[add me@devbox|Dev remove devbox connect devbox trust devbox SHA256:abc]"; got != want {
		t.Fatalf("calls %s, want %s", got, want)
	}

	for _, r := range []string{
		`{"type":"hosts.add","id":1}`,
		`{"type":"hosts.add","id":1,"target":""}`,
		`{"type":"hosts.add","id":1,"target":5}`,
		`{"type":"hosts.remove","id":1}`,
		`{"type":"hosts.connect","id":1}`,
		`{"type":"hosts.trustKey","id":1,"host":"devbox"}`,
		`{"type":"hosts.trustKey","id":1,"fingerprint":"x"}`,
	} {
		wantErr(t, rpc(t, ctx, c, r), "bad_request")
	}

	for err, code := range map[error]string{
		session.ErrNotFound:    "not_found",
		session.ErrBadRequest:  "bad_request",
		session.ErrUnavailable: "unavailable",
		fmt.Errorf("boom"):     "internal",
	} {
		hm.mu.Lock()
		hm.err = fmt.Errorf("%w: detail", err)
		hm.mu.Unlock()
		for _, r := range []string{
			`{"type":"hosts.add","id":5,"target":"x"}`,
			`{"type":"hosts.remove","id":5,"host":"x"}`,
			`{"type":"hosts.connect","id":5,"host":"x"}`,
			`{"type":"hosts.trustKey","id":5,"host":"x","fingerprint":"f"}`,
		} {
			m = rpc(t, ctx, c, r)
			wantErr(t, m, code)
			if m["message"] != hm.err.Error() {
				t.Fatalf("message %v", m)
			}
		}
	}
}

// connect2 dials and consumes hello, the initial hosts.changed and one
// sessions.changed per backend.
func connect2(t *testing.T, hm session.HostManager) (*websocket.Conn, context.Context) {
	t.Helper()
	c, ctx := dial(t, newHMTS(t, hm))
	if m := readMsg(t, ctx, c); m["type"] != "hello" {
		t.Fatalf("hello: %v", m)
	}
	if m := readMsg(t, ctx, c); m["event"] != "hosts.changed" {
		t.Fatalf("hosts event: %v", m)
	}
	for range hm.Backends() {
		if m := readMsg(t, ctx, c); m["event"] != "sessions.changed" {
			t.Fatalf("sessions event: %v", m)
		}
	}
	return c, ctx
}

func TestHostsChangedInitialAndOnChange(t *testing.T) {
	local := newFakeBackend()
	hm := newFakeHM(local)
	c, ctx := dial(t, newHMTS(t, hm))
	readMsg(t, ctx, c) // hello
	m := readMsg(t, ctx, c)
	if m["type"] != "event" || m["event"] != "hosts.changed" {
		t.Fatal(m)
	}
	hs := m["hosts"].([]any)
	if len(hs) != 1 || hs[0].(map[string]any)["id"] != "local" {
		t.Fatal(m)
	}
	if m := readMsg(t, ctx, c); m["event"] != "sessions.changed" || m["host"] != "local" {
		t.Fatal(m)
	}

	hm.set(local, newFakeBackendID("devbox"))
	m = readMsg(t, ctx, c)
	if m["event"] != "hosts.changed" || len(m["hosts"].([]any)) != 2 {
		t.Fatal(m)
	}
}

func TestSessionsForHostAddedMidConnection(t *testing.T) {
	local := newFakeBackend()
	hm := newFakeHM(local)
	c, ctx := connect2(t, hm)

	dev := newFakeBackendID("devbox")
	hm.set(local, dev)
	m := readMsg(t, ctx, c)
	if m["event"] != "hosts.changed" || len(m["hosts"].([]any)) != 2 {
		t.Fatal(m)
	}
	m = readMsg(t, ctx, c)
	if m["event"] != "sessions.changed" || m["host"] != "devbox" || len(m["sessions"].([]any)) != 1 {
		t.Fatalf("initial sessions for new host: %v", m)
	}
	// Later changes keep flowing, and the new host is addressable.
	dev.mu.Lock()
	fn := dev.subFn
	dev.mu.Unlock()
	fn([]session.Session{{ID: "$5"}, {ID: "$6"}})
	m = readMsg(t, ctx, c)
	if m["host"] != "devbox" || len(m["sessions"].([]any)) != 2 {
		t.Fatal(m)
	}
	m = rpc(t, ctx, c, `{"type":"sessions.list","id":1,"host":"devbox"}`)
	if m["type"] != "result" {
		t.Fatal(m)
	}
}

func TestUnsubscribeWhenHostRemoved(t *testing.T) {
	local := newFakeBackend()
	dev := newFakeBackendID("devbox")
	hm := newFakeHM(local, dev)
	c, ctx := connect2(t, hm)

	hm.set(local)
	if m := readMsg(t, ctx, c); m["event"] != "hosts.changed" || len(m["hosts"].([]any)) != 1 {
		t.Fatal(m)
	}
	select {
	case <-dev.unsubbed:
	case <-time.After(5 * time.Second):
		t.Fatal("removed host not unsubscribed")
	}
	select {
	case <-local.unsubbed:
		t.Fatal("local unsubscribed")
	default:
	}
	wantErr(t, rpc(t, ctx, c, `{"type":"sessions.list","id":1,"host":"devbox"}`), "not_found")

	// Closing the connection releases everything, including the HostManager subscription.
	c.Close(websocket.StatusNormalClosure, "")
	select {
	case <-local.unsubbed:
	case <-time.After(5 * time.Second):
		t.Fatal("local not unsubscribed on close")
	}
	deadline := time.Now().Add(5 * time.Second)
	for hm.nSubs() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if hm.nSubs() != 0 {
		t.Fatal("HostManager subscription leaked")
	}
}
