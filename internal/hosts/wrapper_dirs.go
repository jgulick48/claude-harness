package hosts

import (
	"context"
	"fmt"

	"github.com/jgulick48/claude-harness/internal/session"
)

var _ session.DirCompleter = (*sshHost)(nil)

// completer returns the current inner backend as a DirCompleter, or
// ErrUnavailable when the host is not connected or its backend can't browse.
func (h *sshHost) completer() (session.DirCompleter, error) {
	b, err := h.current()
	if err != nil {
		return nil, err
	}
	dc, ok := b.(session.DirCompleter)
	if !ok {
		return nil, fmt.Errorf("%w: host %s cannot browse directories", session.ErrUnavailable, h.id)
	}
	return dc, nil
}

// CompleteDir delegates to the current inner backend.
func (h *sshHost) CompleteDir(ctx context.Context, prefix string) ([]session.DirEntry, error) {
	dc, err := h.completer()
	if err != nil {
		return nil, err
	}
	return dc.CompleteDir(ctx, prefix)
}

// FindRepos delegates to the current inner backend.
func (h *sshHost) FindRepos(ctx context.Context) ([]string, error) {
	dc, err := h.completer()
	if err != nil {
		return nil, err
	}
	return dc.FindRepos(ctx)
}
