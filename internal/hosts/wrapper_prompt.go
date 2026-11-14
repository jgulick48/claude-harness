package hosts

import (
	"context"
	"fmt"

	"github.com/jgulick48/claude-harness/internal/session"
)

var _ session.PromptSender = (*sshHost)(nil)

// SendPrompt delegates to the current inner backend.
func (h *sshHost) SendPrompt(ctx context.Context, id, text string, allowWaiting bool) error {
	b, err := h.current()
	if err != nil {
		return err
	}
	ps, ok := b.(session.PromptSender)
	if !ok {
		return fmt.Errorf("%w: host %s cannot send prompts", session.ErrUnavailable, h.id)
	}
	return ps.SendPrompt(ctx, id, text, allowWaiting)
}
