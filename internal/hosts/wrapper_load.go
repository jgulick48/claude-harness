package hosts

import (
	"context"
	"fmt"

	"github.com/jgulick48/claude-harness/internal/session"
)

var _ session.LoadReader = (*sshHost)(nil)

// ReadLoad delegates to the current inner backend. A host that is not
// connected is ErrUnavailable; an inner backend that can't read load is
// ErrUnsupported.
func (h *sshHost) ReadLoad(ctx context.Context) (session.HostLoad, error) {
	b, err := h.current()
	if err != nil {
		return session.HostLoad{}, err
	}
	lr, ok := b.(session.LoadReader)
	if !ok {
		return session.HostLoad{}, fmt.Errorf("%w: host %s cannot report load", session.ErrUnsupported, h.id)
	}
	return lr.ReadLoad(ctx)
}
