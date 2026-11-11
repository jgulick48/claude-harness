package api

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jgulick48/claude-harness/internal/session"
)

type checkBackend struct {
	*fakeBackend
	rep session.HostReport
	err error
}

func (c *checkBackend) Check(context.Context) (session.HostReport, error) {
	c.rec("check")
	return c.rep, c.err
}

func TestCheckMethod(t *testing.T) {
	cb := &checkBackend{fakeBackend: newFakeBackend(), rep: session.HostReport{Host: "local", CheckedAt: 5,
		Items: []session.CheckItem{
			{ID: "tmux", Label: "tmux", Status: "ok", Detail: "3.4 at /usr/bin/tmux"},
			{ID: "claude", Label: "Claude Code", Status: "warn", Detail: "x", Fix: "f", FixCommand: "c"},
		}}}
	_, run := hookConnect(t, cb)
	r := run(`{"type":"hosts.check","id":1,"host":"local"}`)
	if r["type"] != "result" || r["id"] != float64(1) {
		t.Fatal(r)
	}
	res := r["result"].(map[string]any)
	if res["host"] != "local" || res["checkedAt"] != float64(5) {
		t.Fatal(res)
	}
	items := res["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["id"] != "tmux" {
		t.Fatal(items)
	}
	if _, has := items[0].(map[string]any)["fix"]; has {
		t.Errorf("ok item has fix: %v", items[0])
	}
	if c := items[1].(map[string]any); c["fix"] != "f" || c["fixCommand"] != "c" {
		t.Errorf("%v", c)
	}
	if len(cb.calls) != 1 || cb.calls[0] != "check" {
		t.Errorf("calls %v", cb.calls)
	}
}

func TestCheckEmptyItemsIsArray(t *testing.T) {
	_, run := hookConnect(t, &checkBackend{fakeBackend: newFakeBackend()})
	r := run(`{"type":"hosts.check","id":1,"host":"local"}`)
	res := r["result"].(map[string]any)
	items, ok := res["items"].([]any)
	if !ok || len(items) != 0 {
		t.Fatalf("items = %#v", res["items"])
	}
}

func TestCheckNotSupported(t *testing.T) {
	_, run := hookConnect(t, newFakeBackend())
	r := run(`{"type":"hosts.check","id":1,"host":"local"}`)
	wantErr(t, r, "bad_request")
	if msg, _ := r["message"].(string); !strings.Contains(msg, "setup check not supported on this host") {
		t.Fatal(r)
	}
}

func TestCheckParamsAndErrors(t *testing.T) {
	cb := &checkBackend{fakeBackend: newFakeBackend(), err: fmt.Errorf("%w: connection lost", session.ErrUnavailable)}
	_, run := hookConnect(t, cb)
	wantErr(t, run(`{"type":"hosts.check","id":1}`), "bad_request")
	wantErr(t, run(`{"type":"hosts.check","id":2,"host":"nope"}`), "not_found")
	wantErr(t, run(`{"type":"hosts.check","id":3,"host":"local"}`), "unavailable")
	cb.err = fmt.Errorf("%w: x", session.ErrNotFound)
	wantErr(t, run(`{"type":"hosts.check","id":4,"host":"local"}`), "not_found")
	cb.err = fmt.Errorf("%w: y", session.ErrBadRequest)
	wantErr(t, run(`{"type":"hosts.check","id":5,"host":"local"}`), "bad_request")
	cb.err = fmt.Errorf("weird")
	wantErr(t, run(`{"type":"hosts.check","id":6,"host":"local"}`), "internal")
}
