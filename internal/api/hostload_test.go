package api

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jgulick48/claude-harness/internal/session"
)

type loadBackend struct {
	*fakeBackend
	res session.HostLoad
	err error
}

func (l loadBackend) ReadLoad(context.Context) (session.HostLoad, error) { return l.res, l.err }

func TestHostLoad(t *testing.T) {
	cpu := 23.5
	lb := loadBackend{fakeBackend: newFakeBackend(), res: session.HostLoad{
		At: 1794701234567, CPUPct: &cpu, Cores: 8, Load1: 0.5, Load5: 0.6, Load15: 0.7, MemTotal: 1000, MemUsed: 400}}
	hm := newFakeHM()
	hm.backends = []session.Backend{lb}
	c, ctx := connectHistory(t, hm, nil)

	m := rpc(t, ctx, c, `{"type":"hosts.load","id":1,"host":"local"}`)
	if m["type"] != "result" {
		t.Fatal(m)
	}
	r := m["result"].(map[string]any)
	if r["at"] != 1794701234567.0 || r["cpuPct"] != 23.5 || r["cores"] != 8.0 || r["load15"] != 0.7 ||
		r["memTotal"] != 1000.0 || r["memUsed"] != 400.0 {
		t.Fatalf("%v", r)
	}
	wantErr(t, rpc(t, ctx, c, `{"type":"hosts.load","id":2}`), "bad_request")
	wantErr(t, rpc(t, ctx, c, `{"type":"hosts.load","id":3,"host":"nope"}`), "not_found")

	for i, tc := range []struct {
		err  error
		code string
	}{
		{fmt.Errorf("%w: freebsd", session.ErrUnsupported), "unsupported"},
		{session.ErrUnavailable, "unavailable"},
		{errors.New("boom"), "internal"},
	} {
		lb.err = tc.err
		hm.backends = []session.Backend{lb}
		wantErr(t, rpc(t, ctx, c, fmt.Sprintf(`{"type":"hosts.load","id":%d,"host":"local"}`, 10+i)), tc.code)
	}
}

func TestHostLoadWithoutCapability(t *testing.T) {
	hm := newFakeHM(newFakeBackend())
	c, ctx := connectHistory(t, hm, nil)
	wantErr(t, rpc(t, ctx, c, `{"type":"hosts.load","id":1,"host":"local"}`), "unsupported")
}
