package hosts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jgulick48/claude-harness/internal/session"
	"github.com/jgulick48/claude-harness/internal/sshconn"
)

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

type plainNetErr struct{}

func (plainNetErr) Error() string   { return "some net error" }
func (plainNetErr) Timeout() bool   { return false }
func (plainNetErr) Temporary() bool { return false }

func TestIsRetryable(t *testing.T) {
	opErr := func(e error) error {
		return &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", e)}
	}
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"refused", fmt.Errorf("connect x:22: %w", opErr(syscall.ECONNREFUSED)), true},
		{"reset", opErr(syscall.ECONNRESET), true},
		{"hostunreach", opErr(syscall.EHOSTUNREACH), true},
		{"netunreach", opErr(syscall.ENETUNREACH), true},
		{"windows refused", fmt.Errorf("x: %w", syscall.Errno(10061)), true},
		{"dns", fmt.Errorf("connect: %w", &net.DNSError{Err: "no such host", Name: "x"}), true},
		{"net timeout", fmt.Errorf("x: %w", timeoutErr{}), true},
		{"net non-timeout", plainNetErr{}, false},
		{"handshake eof", fmt.Errorf("ssh x: ssh: handshake failed: %w", io.EOF), true},
		{"unexpected eof", io.ErrUnexpectedEOF, true},
		{"deadline", fmt.Errorf("connect: %w", context.DeadlineExceeded), true},
		{"keepalive loss", fmt.Errorf("%w: keepalive timed out after 15s", sshconn.ErrConnectionLost), true},
		{"canceled", context.Canceled, false},
		{"unknown key", &sshconn.UnknownHostKeyError{Address: "x"}, false},
		{"wrapped unknown key", fmt.Errorf("hop: %w", &sshconn.UnknownHostKeyError{Address: "x"}), false},
		{"changed key", &sshconn.HostKeyChangedError{Address: "x"}, false},
		{"no auth", fmt.Errorf("x: %w", sshconn.ErrNoAuthMethods), false},
		{"auth failed", errors.New("ssh x: ssh: handshake failed: ssh: unable to authenticate, attempted methods [none publickey], no supported methods remain"), false},
		{"auth failed over eof", fmt.Errorf("ssh: unable to authenticate: %w", io.EOF), false},
		{"unknown", errors.New("kaboom"), false},
		{"tmux", errors.New("tmux not found"), false},
		{"unknown key beats refused", errors.Join(&sshconn.UnknownHostKeyError{}, syscall.ECONNREFUSED), false},
	}
	for _, tc := range tests {
		if got := isRetryable(tc.err); got != tc.want {
			t.Errorf("%s: isRetryable(%v) = %v, want %v", tc.name, tc.err, got, tc.want)
		}
	}
}

func TestBackoffDelay(t *testing.T) {
	want := []time.Duration{1, 2, 4, 8, 16, 32, 60, 60, 60}
	for i, w := range want {
		if got := backoffDelay(i, time.Second, 60*time.Second, 0); got != w*time.Second {
			t.Errorf("attempt %d: %v want %v", i, got, w*time.Second)
		}
	}
	if got := backoffDelay(1000, time.Second, 60*time.Second, 0); got != 60*time.Second {
		t.Errorf("huge attempt: %v", got)
	}
	if lo := backoffDelay(2, time.Second, time.Minute, -1); lo != 3200*time.Millisecond {
		t.Errorf("min jitter %v", lo)
	}
	if hi := backoffDelay(2, time.Second, time.Minute, 1); hi != 4800*time.Millisecond {
		t.Errorf("max jitter %v", hi)
	}
	if backoffDelay(2, time.Second, time.Minute, 50) != 4800*time.Millisecond {
		t.Error("jitter not clamped")
	}
	// The default random jitter stays within +/-20%.
	m := &Manager{}
	for i := 0; i < 200; i++ {
		d := m.delayFor(3)
		if d < 6400*time.Millisecond || d > 9600*time.Millisecond {
			t.Fatalf("jittered delay %v out of bounds", d)
		}
	}
}

// fakeClock is a manual clock + timer factory.
type fakeClock struct {
	mu     sync.Mutex
	t      time.Time
	timers []*fakeTimer
	jit    float64
}

type fakeTimer struct {
	c       *fakeClock
	d       time.Duration
	f       func()
	stopped bool
	fired   bool
}

func (t *fakeTimer) Stop() bool {
	t.c.mu.Lock()
	defer t.c.mu.Unlock()
	was := !t.stopped && !t.fired
	t.stopped = true
	return was
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.UnixMilli(1_700_000_000_000)} }

func (c *fakeClock) jitter() float64  { c.mu.Lock(); defer c.mu.Unlock(); return c.jit }
func (c *fakeClock) setJit(j float64) { c.mu.Lock(); c.jit = j; c.mu.Unlock() }

func (c *fakeClock) now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) advance(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }
func (c *fakeClock) after(d time.Duration, f func()) stopper {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{c: c, d: d, f: f}
	c.timers = append(c.timers, t)
	return t
}

// pending returns the active (not stopped, not fired) timers.
func (c *fakeClock) pending() []*fakeTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []*fakeTimer
	for _, t := range c.timers {
		if !t.stopped && !t.fired {
			out = append(out, t)
		}
	}
	return out
}

// waitPending waits for exactly n pending timers and returns them.
func (c *fakeClock) waitPending(t *testing.T, n int) []*fakeTimer {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if p := c.pending(); len(p) == n {
			return p
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("pending timers = %d, want %d", len(c.pending()), n)
	return nil
}

func (c *fakeClock) fire(tm *fakeTimer) {
	c.mu.Lock()
	tm.fired = true
	c.t = c.t.Add(tm.d)
	c.mu.Unlock()
	tm.f()
}

func (c *fakeClock) delays() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []time.Duration
	for _, t := range c.timers {
		out = append(out, t.d)
	}
	return out
}

func newClockTest(t *testing.T, f *fakes, clk *fakeClock) *Manager {
	t.Helper()
	m := newTest(t, f, "")
	m.rs = retrySeams{now: clk.now, jitter: clk.jitter, afterFunc: clk.after}
	return m
}

// recorder collects the distinct consecutive states of one host.
func record(m *Manager, id string) (get func() []string, stop func()) {
	var mu sync.Mutex
	var seq []string
	unsub := m.Subscribe(func(hs []session.Host) {
		for _, h := range hs {
			if h.ID != id {
				continue
			}
			mu.Lock()
			if len(seq) == 0 || seq[len(seq)-1] != h.State {
				seq = append(seq, h.State)
			}
			mu.Unlock()
		}
	})
	return func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), seq...) }, unsub
}

func TestLossRetriesThenConnects(t *testing.T) {
	f := newFakes()
	clk := newFakeClock()
	m := newClockTest(t, f, clk)
	h, _ := m.Add(context.Background(), "me@box", "")
	waitState(t, m, h.ID, session.HostConnected)
	seq, stop := record(m, h.ID)
	defer stop()

	f.client(0).drop(errors.New("boom"))
	got := waitState(t, m, h.ID, session.HostDisconnected)
	tm := clk.waitPending(t, 1)[0]
	got = waitState(t, m, h.ID, session.HostDisconnected)
	if got.Error != "connection lost: boom" {
		t.Fatalf("error %q", got.Error)
	}
	wantAt := clk.now().Add(time.Second).UnixMilli()
	if got.RetryAt != wantAt || tm.d != time.Second {
		t.Fatalf("RetryAt %d want %d (delay %v)", got.RetryAt, wantAt, tm.d)
	}
	// While waiting, the host rejects operations.
	be, _ := m.Backend(h.ID)
	if _, err := be.List(context.Background()); !errors.Is(err, session.ErrUnavailable) {
		t.Fatalf("list: %v", err)
	}
	// First retry fails (refused): backoff doubles.
	f.mu.Lock()
	f.dialErr["me@box"] = fmt.Errorf("dial tcp 127.0.0.1:22: %w", syscall.ECONNREFUSED)
	f.mu.Unlock()
	clk.fire(tm)
	tm = clk.waitPending(t, 1)[0]
	got = waitState(t, m, h.ID, session.HostDisconnected)
	if tm.d != 2*time.Second || got.RetryAt == 0 || got.Error == "" {
		t.Fatalf("after refused: delay %v host %+v", tm.d, got)
	}
	// Second retry succeeds.
	f.mu.Lock()
	delete(f.dialErr, "me@box")
	release := make(chan struct{})
	f.block = release
	f.mu.Unlock()
	clk.fire(tm)
	got = waitState(t, m, h.ID, session.HostConnecting)
	if got.RetryAt != 0 {
		t.Fatalf("connecting host has RetryAt %d", got.RetryAt)
	}
	for dl := time.Now().Add(2 * time.Second); time.Now().Before(dl) && !endsWith(seq(), "connecting"); {
		time.Sleep(2 * time.Millisecond)
	}
	close(release)
	got = waitState(t, m, h.ID, session.HostConnected)
	if got.RetryAt != 0 || got.Error != "" {
		t.Fatalf("connected host %+v", got)
	}
	if n := len(clk.pending()); n != 0 {
		t.Fatalf("%d timers pending after success", n)
	}
	// Notifications may coalesce a short-lived "connecting", so require the
	// full cycle as an ordered subsequence.
	want := []string{"connected", "disconnected", "connecting", "connected"}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !subseq(seq(), want) {
		time.Sleep(5 * time.Millisecond)
	}
	if !subseq(seq(), want) {
		t.Fatalf("states %v lack %v", seq(), want)
	}
	// A connection that drops right away doesn't count as success: backoff
	// keeps growing (two failures so far, so 4s), so a host that accepts and
	// immediately drops isn't retried every second forever.
	f.client(len(f.clients) - 1).drop(nil)
	tm = clk.waitPending(t, 1)[0]
	if tm.d != 4*time.Second {
		t.Fatalf("delay after unstable connection %v, want 4s", tm.d)
	}
	waitState(t, m, h.ID, session.HostDisconnected)
	clk.fire(tm)
	waitState(t, m, h.ID, session.HostConnected)
	// A connection that stayed up for stableAfter resets the backoff to 1s.
	clk.advance(stableAfter)
	f.client(len(f.clients) - 1).drop(nil)
	if tm := clk.waitPending(t, 1)[0]; tm.d != time.Second {
		t.Fatalf("delay after stable connection %v, want 1s", tm.d)
	}
}

func TestBackoffScheduleCapAndJitterBounds(t *testing.T) {
	f := newFakes()
	f.dialErr["me@box"] = syscall.ECONNREFUSED
	clk := newFakeClock()
	m := newClockTest(t, f, clk)
	m.Add(context.Background(), "me@box", "")
	var got []time.Duration
	for i := 0; i < 9; i++ {
		tm := clk.waitPending(t, 1)[0]
		got = append(got, tm.d)
		clk.fire(tm)
	}
	want := []time.Duration{1, 2, 4, 8, 16, 32, 60, 60, 60}
	for i := range want {
		if got[i] != want[i]*time.Second {
			t.Fatalf("schedule %v", got)
		}
	}
	// With extreme jitter, the delay stays within +/-20% of the 60s cap.
	clk.setJit(1)
	tm := clk.waitPending(t, 1)[0]
	clk.fire(tm)
	tm = clk.waitPending(t, 1)[0]
	if tm.d != 72*time.Second {
		t.Fatalf("max jitter delay %v", tm.d)
	}
	clk.setJit(-1)
	clk.fire(tm)
	tm = clk.waitPending(t, 1)[0]
	if tm.d != 48*time.Second {
		t.Fatalf("min jitter delay %v", tm.d)
	}
}

func TestConnectCancelsRetryAndResetsBackoff(t *testing.T) {
	f := newFakes()
	f.dialErr["me@box"] = syscall.ECONNREFUSED
	clk := newFakeClock()
	m := newClockTest(t, f, clk)
	h, _ := m.Add(context.Background(), "me@box", "")
	for i := 0; i < 4; i++ { // delays 1,2,4,8 -> next would be 16
		clk.fire(clk.waitPending(t, 1)[0])
	}
	old := clk.waitPending(t, 1)[0]
	if old.d != 16*time.Second {
		t.Fatalf("delay %v", old.d)
	}
	dials := f.nDials()
	if err := m.Connect(context.Background(), h.ID); err != nil {
		t.Fatal(err)
	}
	// The pending timer was cancelled; the manual attempt fails again and the
	// backoff restarts at 1s.
	tm := clk.waitPending(t, 1)[0]
	if tm == old || !old.stopped || tm.d != time.Second {
		t.Fatalf("stopped=%v delay=%v", old.stopped, tm.d)
	}
	if f.nDials() != dials+1 {
		t.Fatalf("dials %d want %d", f.nDials(), dials+1)
	}
	// A stale firing of the cancelled timer is ignored.
	old.f()
	time.Sleep(20 * time.Millisecond)
	if f.nDials() != dials+1 {
		t.Fatal("stale timer started an attempt")
	}
}

func TestNoRetryForUserActionErrors(t *testing.T) {
	cases := map[string]struct {
		err   error
		state string
	}{
		"unknown key": {unknownKeyErr(t), session.HostKeyUnknown},
		"changed key": {&sshconn.HostKeyChangedError{Address: "box", KeyType: "ssh-ed25519", Fingerprint: "SHA256:x"}, session.HostError},
		"auth":        {errors.New("ssh box:22: ssh: handshake failed: ssh: unable to authenticate, attempted methods [none publickey]"), session.HostError},
		"no methods":  {fmt.Errorf("box: %w", sshconn.ErrNoAuthMethods), session.HostError},
		"unknown":     {errors.New("kaboom"), session.HostError},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFakes()
			f.dialErr["me@box"] = tc.err
			clk := newFakeClock()
			m := newClockTest(t, f, clk)
			h, _ := m.Add(context.Background(), "me@box", "")
			got := waitState(t, m, h.ID, tc.state)
			time.Sleep(30 * time.Millisecond)
			if n := len(clk.delays()); n != 0 {
				t.Fatalf("%d timers scheduled", n)
			}
			if got.RetryAt != 0 {
				t.Fatalf("RetryAt %d", got.RetryAt)
			}
		})
	}
}

func TestRemoveDuringBackoffStopsTimer(t *testing.T) {
	f := newFakes()
	f.dialErr["me@box"] = syscall.ECONNREFUSED
	clk := newFakeClock()
	m := newClockTest(t, f, clk)
	h, _ := m.Add(context.Background(), "me@box", "")
	tm := clk.waitPending(t, 1)[0]
	if err := m.Remove(context.Background(), h.ID); err != nil {
		t.Fatal(err)
	}
	if !tm.stopped {
		t.Fatal("timer not stopped by Remove")
	}
	n := f.nDials()
	tm.f() // even a late firing does nothing
	time.Sleep(20 * time.Millisecond)
	if f.nDials() != n {
		t.Fatal("removed host redialed")
	}
}

func TestCloseDuringBackoffStopsTimer(t *testing.T) {
	f := newFakes()
	f.dialErr["me@box"] = syscall.ECONNREFUSED
	clk := newFakeClock()
	m := newClockTest(t, f, clk)
	m.Add(context.Background(), "me@box", "")
	tm := clk.waitPending(t, 1)[0]
	m.Close()
	if !tm.stopped {
		t.Fatal("timer not stopped by Close")
	}
}

func TestStartupRetryableFailureBacksOff(t *testing.T) {
	f := newFakes()
	f.dialErr["me@box"] = syscall.ECONNREFUSED
	file := t.TempDir() + "/hosts.json"
	m := newTest(t, newFakes(), file)
	m.Add(context.Background(), "me@box", "")
	m.Close()
	// Re-open: the saved host is dialed in the background and, failing
	// retryably, enters backoff (real timers here; cleanup stops them).
	m2 := newTest(t, f, file)
	got := waitState(t, m2, "box", session.HostDisconnected)
	if got.RetryAt == 0 || got.Error == "" {
		t.Fatalf("host %+v", got)
	}
}

func TestConfigHostsAdded(t *testing.T) {
	dir := t.TempDir()
	cfg := dir + "/config"
	os.WriteFile(cfg, []byte("Host dev prod\n  HostName 10.0.0.1\n  User bob\n  Port 2200\nHost *\n  User nobody\nHost other\n"), 0o600)
	f := newFakes()
	m := newTest(t, f, "")
	m.opts.SSH = sshconn.Options{ConfigFile: cfg, KnownHostsFile: dir + "/kh"}
	m.Add(context.Background(), "dev", "")
	m.Add(context.Background(), "bob@other", "") // not an exact alias match
	got, err := m.ConfigHosts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []session.ConfigHost{
		{Alias: "dev", HostName: "10.0.0.1", User: "bob", Port: 2200, Added: true},
		{Alias: "prod", HostName: "10.0.0.1", User: "bob", Port: 2200},
		{Alias: "other", HostName: "other", User: "nobody", Port: 22},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	m2 := newTest(t, newFakes(), "")
	m2.opts.SSH = sshconn.Options{ConfigFile: dir + "/missing", KnownHostsFile: dir + "/kh"}
	got, err = m2.ConfigHosts(context.Background())
	if err != nil || len(got) != 0 {
		t.Fatalf("missing config: %v %v", got, err)
	}
}

func subseq(got, want []string) bool {
	i := 0
	for _, g := range got {
		if i < len(want) && g == want[i] {
			i++
		}
	}
	return i == len(want)
}

func endsWith(got []string, s string) bool { return len(got) > 0 && got[len(got)-1] == s }
