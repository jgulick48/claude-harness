package hosts

import (
	"context"
	"errors"
	"testing"

	"github.com/jgulick48/claude-harness/internal/session"
	"github.com/jgulick48/claude-harness/internal/session/tmuxbackend"
)

type promptInner struct {
	*fakeInner
	got  string
	busy bool
}

func (p *promptInner) SendPrompt(_ context.Context, id, text string, allowWaiting bool) error {
	if p.busy {
		return session.ErrBusy
	}
	p.got = id + "|" + text
	if allowWaiting {
		p.got += "|w"
	}
	return nil
}

func TestWrapperSendPrompt(t *testing.T) {
	f := newFakes()
	m := newTest(t, f, "")
	pi := &promptInner{}
	m.newBackend = func(ctx context.Context, id, name string, c sshClient, o tmuxbackend.Options) (session.Backend, error) {
		pi.fakeInner = &fakeInner{id: id}
		return pi, nil
	}
	h, _ := m.Add(context.Background(), "me@box", "")
	waitState(t, m, h.ID, session.HostConnected)
	be, _ := m.Backend(h.ID)
	ps, ok := be.(session.PromptSender)
	if !ok {
		t.Fatal("sshHost is not a PromptSender")
	}
	if err := ps.SendPrompt(context.Background(), "$1", "hi", true); err != nil || pi.got != "$1|hi|w" {
		t.Fatalf("got %q, %v", pi.got, err)
	}
	pi.busy = true
	if err := ps.SendPrompt(context.Background(), "$1", "hi", false); !errors.Is(err, session.ErrBusy) {
		t.Fatalf("err %v", err)
	}
}
