package hosts

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jgulick48/claude-harness/internal/session"
	"github.com/jgulick48/claude-harness/internal/session/tmuxbackend"
)

type checkInner struct {
	*fakeInner
	rep session.HostReport
	err error
}

func (c *checkInner) Check(context.Context) (session.HostReport, error) { return c.rep, c.err }

func newCheckTest(t *testing.T, ci func(id string) session.Backend) (*Manager, *fakes) {
	t.Helper()
	f := newFakes()
	m := newTest(t, f, "")
	m.newBackend = func(ctx context.Context, id, name string, c sshClient, o tmuxbackend.Options) (session.Backend, error) {
		return ci(id), nil
	}
	return m, f
}

func TestWrapperCheckConnected(t *testing.T) {
	m, _ := newCheckTest(t, func(id string) session.Backend {
		return &checkInner{fakeInner: &fakeInner{id: id}, rep: session.HostReport{Host: "inner", CheckedAt: 7,
			Items: []session.CheckItem{{ID: "tmux", Status: session.CheckOK}}}}
	})
	h, _ := m.Add(context.Background(), "me@box", "")
	waitState(t, m, h.ID, session.HostConnected)
	be, _ := m.Backend(h.ID)
	hc, ok := be.(session.HostChecker)
	if !ok {
		t.Fatal("sshHost is not a HostChecker")
	}
	rep, err := hc.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Host != h.ID || rep.CheckedAt != 7 || len(rep.Items) != 2 {
		t.Fatalf("%+v", rep)
	}
	if c := rep.Items[0]; c.ID != "connection" || c.Status != session.CheckOK || c.Detail != "connected to me@box" || c.Fix != "" {
		t.Errorf("connection item %+v", c)
	}
	if rep.Items[1].ID != "tmux" {
		t.Errorf("%+v", rep.Items)
	}
}

func TestWrapperCheckInnerError(t *testing.T) {
	boom := errors.New("boom")
	m, _ := newCheckTest(t, func(id string) session.Backend {
		return &checkInner{fakeInner: &fakeInner{id: id}, err: boom}
	})
	h, _ := m.Add(context.Background(), "me@box", "")
	waitState(t, m, h.ID, session.HostConnected)
	be, _ := m.Backend(h.ID)
	if _, err := be.(session.HostChecker).Check(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("err %v", err)
	}
}

func TestWrapperCheckInnerWithoutChecker(t *testing.T) {
	m, _ := newCheckTest(t, func(id string) session.Backend { return &fakeInner{id: id} })
	h, _ := m.Add(context.Background(), "me@box", "")
	waitState(t, m, h.ID, session.HostConnected)
	be, _ := m.Backend(h.ID)
	if _, err := be.(session.HostChecker).Check(context.Background()); !errors.Is(err, session.ErrBadRequest) {
		t.Fatalf("err %v", err)
	}
}

func TestWrapperCheckDisconnected(t *testing.T) {
	m, f := newCheckTest(t, func(id string) session.Backend {
		return &checkInner{fakeInner: &fakeInner{id: id}, rep: session.HostReport{Items: []session.CheckItem{{ID: "tmux"}}}}
	})
	h, _ := m.Add(context.Background(), "me@box", "")
	waitState(t, m, h.ID, session.HostConnected)
	f.client(0).drop(errors.New("boom"))
	waitState(t, m, h.ID, session.HostDisconnected)
	be, _ := m.Backend(h.ID)
	rep, err := be.(session.HostChecker).Check(context.Background())
	if err != nil {
		t.Fatalf("disconnected check must not error: %v", err)
	}
	if rep.Host != h.ID || len(rep.Items) != 1 {
		t.Fatalf("%+v", rep)
	}
	it := rep.Items[0]
	if it.ID != "connection" || it.Status != session.CheckError || !strings.Contains(it.Detail, "connection lost: boom") ||
		it.Fix != "Reconnect the host (or check the SSH target and keys)" {
		t.Errorf("%+v", it)
	}
}

func TestWrapperCheckNeverConnected(t *testing.T) {
	f := newFakes()
	f.dialErr["me@box"] = errors.New("no route to host")
	m := newTest(t, f, "")
	h, _ := m.Add(context.Background(), "me@box", "")
	waitState(t, m, h.ID, session.HostError)
	be, _ := m.Backend(h.ID)
	rep, err := be.(session.HostChecker).Check(context.Background())
	if err != nil || len(rep.Items) != 1 || rep.Items[0].Status != session.CheckError ||
		!strings.Contains(rep.Items[0].Detail, "no route to host") {
		t.Fatalf("%+v %v", rep, err)
	}
}
