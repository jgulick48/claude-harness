package api

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/jgulick48/claude-harness/internal/session"
)

type hookBackend struct {
	*fakeBackend
	installErr, removeErr error
	statusList            []session.Session
}

func (h *hookBackend) HooksStatus(context.Context) (string, error) { return session.HooksMissing, nil }
func (h *hookBackend) InstallHooks(_ context.Context, usage bool) error {
	h.rec(fmt.Sprintf("install usage=%v", usage))
	return h.installErr
}
func (h *hookBackend) RemoveHooks(context.Context) error {
	h.rec("remove")
	return h.removeErr
}
func (h *hookBackend) List(ctx context.Context) ([]session.Session, error) {
	if h.statusList != nil {
		return h.statusList, nil
	}
	return h.fakeBackend.List(ctx)
}
func (h *hookBackend) Subscribe(fn func([]session.Session)) func() {
	h.mu.Lock()
	h.subFn = fn
	h.mu.Unlock()
	fn(h.statusList)
	return func() {}
}

func hookConnect(t *testing.T, b session.Backend) (hm *fakeHM, run func(string) map[string]any) {
	t.Helper()
	hm = &fakeHM{subs: map[int]func([]session.Host){}, backends: []session.Backend{b}}
	c, ctx := connect2(t, hm)
	return hm, func(req string) map[string]any { return rpc(t, ctx, c, req) }
}

func TestHooksMethods(t *testing.T) {
	hb := &hookBackend{fakeBackend: newFakeBackend()}
	_, run := hookConnect(t, hb)
	for i, m := range []string{"hosts.installHooks", "hosts.removeHooks"} {
		r := run(fmt.Sprintf(`{"type":%q,"id":%d,"host":"local"}`, m, i+1))
		if r["type"] != "result" || r["result"] != nil {
			t.Fatalf("%s: %v", m, r)
		}
	}
	if got := hb.calls; len(got) != 2 || got[0] != "install usage=true" || got[1] != "remove" {
		t.Fatalf("calls %v", got) // usage defaults to true
	}
	wantErr(t, run(`{"type":"hosts.installHooks","id":3}`), "bad_request")
	wantErr(t, run(`{"type":"hosts.removeHooks","id":4,"host":"nope"}`), "not_found")
}

func TestHooksUsageParam(t *testing.T) {
	hb := &hookBackend{fakeBackend: newFakeBackend()}
	_, run := hookConnect(t, hb)
	for i, p := range []string{`"usage":false`, `"usage":true`, `"usage":null`} {
		r := run(fmt.Sprintf(`{"type":"hosts.installHooks","id":%d,"host":"local",%s}`, i+1, p))
		if r["type"] != "result" {
			t.Fatalf("%s: %v", p, r)
		}
	}
	for i, p := range []string{`"usage":"yes"`, `"usage":1`, `"usage":{}`} {
		wantErr(t, run(fmt.Sprintf(`{"type":"hosts.installHooks","id":%d,"host":"local",%s}`, i+10, p)), "bad_request")
	}
	want := []string{"install usage=false", "install usage=true", "install usage=true"}
	if got := hb.calls; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("calls %v, want %v", got, want)
	}
}

func TestHooksNotSupported(t *testing.T) {
	_, run := hookConnect(t, newFakeBackend())
	r := run(`{"type":"hosts.installHooks","id":1,"host":"local"}`)
	wantErr(t, r, "bad_request")
	if msg, _ := r["message"].(string); !strings.Contains(msg, "hooks not supported on this host") {
		t.Fatal(r)
	}
	wantErr(t, run(`{"type":"hosts.removeHooks","id":2,"host":"local"}`), "bad_request")
}

func TestHooksErrorMapping(t *testing.T) {
	hb := &hookBackend{fakeBackend: newFakeBackend(),
		installErr: fmt.Errorf("%w: x", session.ErrUnavailable),
		removeErr:  fmt.Errorf("%w: y", session.ErrNotFound)}
	_, run := hookConnect(t, hb)
	wantErr(t, run(`{"type":"hosts.installHooks","id":1,"host":"local"}`), "unavailable")
	wantErr(t, run(`{"type":"hosts.removeHooks","id":2,"host":"local"}`), "not_found")
}

func TestSessionsChangedCarriesClaudeStatus(t *testing.T) {
	hb := &hookBackend{fakeBackend: newFakeBackend(),
		statusList: []session.Session{{ID: "$1", Name: "a"}}}
	hm := &fakeHM{subs: map[int]func([]session.Host){}, backends: []session.Backend{hb}}
	c, ctx := connect2(t, hm)
	hb.mu.Lock()
	fn := hb.subFn
	hb.mu.Unlock()
	fn([]session.Session{{ID: "$1", Name: "a", ClaudeStatus: "waiting", ClaudeStatusAt: 1794420400}})
	m := readMsg(t, ctx, c)
	if m["event"] != "sessions.changed" {
		t.Fatal(m)
	}
	s := m["sessions"].([]any)[0].(map[string]any)
	if s["claudeStatus"] != "waiting" || s["claudeStatusAt"] != float64(1794420400) {
		t.Fatalf("%v", s)
	}
}

// notifyHM is a fakeHM that also implements session.HostsNotifier.
type notifyHM struct {
	*fakeHM
	mu       sync.Mutex
	notified int
}

func (n *notifyHM) NotifyHostsChanged() {
	n.mu.Lock()
	n.notified++
	n.mu.Unlock()
}

// Installing hooks on the local host (no SSH wrapper to announce it) must make
// the manager re-send hosts, or the UI keeps showing "hooks aren't installed".
func TestHooksNotifyHostsChanged(t *testing.T) {
	hb := &hookBackend{fakeBackend: newFakeBackend(), removeErr: fmt.Errorf("%w: y", session.ErrNotFound)}
	hm := &notifyHM{fakeHM: &fakeHM{subs: map[int]func([]session.Host){}, backends: []session.Backend{hb}}}
	c, ctx := connect2(t, hm)
	run := func(req string) map[string]any { return rpc(t, ctx, c, req) }
	if r := run(`{"type":"hosts.installHooks","id":1,"host":"local"}`); r["type"] != "result" {
		t.Fatal(r)
	}
	hm.mu.Lock()
	n := hm.notified
	hm.mu.Unlock()
	if n != 1 {
		t.Fatalf("NotifyHostsChanged called %d times after install, want 1", n)
	}
	wantErr(t, run(`{"type":"hosts.removeHooks","id":2,"host":"local"}`), "not_found")
	hm.mu.Lock()
	n = hm.notified
	hm.mu.Unlock()
	if n != 1 {
		t.Fatalf("NotifyHostsChanged called after a failed remove (%d)", n)
	}
}
