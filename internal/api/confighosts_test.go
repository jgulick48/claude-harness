package api

import (
	"context"
	"errors"
	"testing"

	"github.com/coder/websocket"

	"github.com/jgulick48/claude-harness/internal/session"
)

// configListerHM is a fakeHM that also implements session.ConfigHostLister.
type configListerHM struct {
	*fakeHM
	list []session.ConfigHost
	err  error
}

func (h *configListerHM) ConfigHosts(context.Context) ([]session.ConfigHost, error) {
	return h.list, h.err
}

func TestConfigHostsWithLister(t *testing.T) {
	hm := &configListerHM{fakeHM: newFakeHM(newFakeBackend()), list: []session.ConfigHost{
		{Alias: "dev", HostName: "10.0.0.1", User: "bob", Port: 2200, Added: true},
		{Alias: "prod", HostName: "prod", User: "bob", Port: 22},
	}}
	c2, ctx2 := dialConfigHM(t, hm)
	m := rpc(t, ctx2, c2, `{"type":"hosts.configHosts","id":1}`)
	if m["type"] != "result" {
		t.Fatal(m)
	}
	res := m["result"].([]any)
	if len(res) != 2 {
		t.Fatalf("result %v", res)
	}
	first := res[0].(map[string]any)
	if first["alias"] != "dev" || first["hostName"] != "10.0.0.1" || first["user"] != "bob" ||
		first["port"] != float64(2200) || first["added"] != true {
		t.Fatalf("first %v", first)
	}
	if res[1].(map[string]any)["added"] != false {
		t.Fatalf("second %v", res[1])
	}
}

func TestConfigHostsNilListIsEmptyArray(t *testing.T) {
	hm := &configListerHM{fakeHM: newFakeHM(newFakeBackend())}
	c, ctx := dialConfigHM(t, hm)
	m := rpc(t, ctx, c, `{"type":"hosts.configHosts","id":1}`)
	res, ok := m["result"].([]any)
	if !ok || len(res) != 0 {
		t.Fatalf("result %#v (want [])", m["result"])
	}
}

func TestConfigHostsWithoutLister(t *testing.T) {
	var hm session.HostManager = newFakeHM(newFakeBackend())
	if _, ok := hm.(session.ConfigHostLister); ok {
		t.Fatal("fakeHM unexpectedly implements ConfigHostLister")
	}
	c, ctx := dialConfigHM(t, hm)
	m := rpc(t, ctx, c, `{"type":"hosts.configHosts","id":1}`)
	res, ok := m["result"].([]any)
	if !ok || len(res) != 0 {
		t.Fatalf("result %#v (want [])", m["result"])
	}
}

func TestConfigHostsError(t *testing.T) {
	hm := &configListerHM{fakeHM: newFakeHM(newFakeBackend()), err: errors.New("bad config")}
	c, ctx := dialConfigHM(t, hm)
	m := rpc(t, ctx, c, `{"type":"hosts.configHosts","id":1}`)
	if m["type"] != "error" {
		t.Fatalf("got %v", m)
	}
}

// dialConfigHM connects and consumes hello, hosts.changed and the single
// backend's sessions.changed.
func dialConfigHM(t *testing.T, hm session.HostManager) (*websocket.Conn, context.Context) {
	t.Helper()
	c, ctx := dial(t, newHMTS(t, hm))
	for i := 0; i < 3; i++ {
		readMsg(t, ctx, c)
	}
	return c, ctx
}
