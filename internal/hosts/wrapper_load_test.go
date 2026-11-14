package hosts

import (
	"context"
	"errors"
	"testing"

	"github.com/jgulick48/claude-harness/internal/session"
)

type loadInner struct{ stubInner }

func (loadInner) ReadLoad(context.Context) (session.HostLoad, error) {
	return session.HostLoad{Cores: 4, MemTotal: 9}, nil
}

// The wrapper must expose the inner backend's LoadReader, or the load view
// silently shows nothing for SSH hosts.
func TestSSHHostDelegatesLoad(t *testing.T) {
	var b session.Backend = newSSHHost(nil, "devbox", "devbox", "devbox")
	if _, ok := b.(session.LoadReader); !ok {
		t.Fatal("sshHost is not a session.LoadReader")
	}
	h := newSSHHost(nil, "devbox", "devbox", "devbox")
	ctx := context.Background()
	if _, err := h.ReadLoad(ctx); !errors.Is(err, session.ErrUnavailable) {
		t.Fatalf("disconnected: %v", err)
	}
	h.state = session.HostConnected
	h.inner = stubInner{}
	if _, err := h.ReadLoad(ctx); !errors.Is(err, session.ErrUnsupported) {
		t.Fatalf("inner without capability: %v", err)
	}
	h.inner = loadInner{}
	l, err := h.ReadLoad(ctx)
	if err != nil || l.Cores != 4 || l.MemTotal != 9 {
		t.Fatalf("delegation: %v %v", l, err)
	}
}
