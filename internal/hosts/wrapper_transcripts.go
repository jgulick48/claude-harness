package hosts

import (
	"context"
	"fmt"

	"github.com/jgulick48/claude-harness/internal/session"
)

var _ session.TranscriptReader = (*sshHost)(nil)

// ReadTranscripts delegates to the current inner backend, or fails with
// ErrUnavailable when the host is not connected or its backend can't read
// transcripts.
func (h *sshHost) ReadTranscripts(ctx context.Context, known map[string]int64) ([]session.TranscriptChunk, error) {
	b, err := h.current()
	if err != nil {
		return nil, err
	}
	tr, ok := b.(session.TranscriptReader)
	if !ok {
		return nil, fmt.Errorf("%w: host %s cannot read transcripts", session.ErrUnavailable, h.id)
	}
	return tr.ReadTranscripts(ctx, known)
}
