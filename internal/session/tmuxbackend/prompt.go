package tmuxbackend

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/jgulick48/claude-harness/internal/session"
)

var _ session.PromptSender = (*Backend)(nil)

// promptEnterDelay is how long SendPrompt waits between pasting the text and
// pressing Enter, so the app has processed the paste first. Tests lower it.
var promptEnterDelay = 150 * time.Millisecond

// SendPrompt types text into the session's active pane as one paste, then
// presses Enter. See session.PromptSender for the safety rules; the status
// check uses a fresh List, not a cached view.
func (b *Backend) SendPrompt(ctx context.Context, id, text string, allowWaiting bool) error {
	text, err := session.CleanPrompt(text)
	if err != nil {
		return err
	}
	if err := b.gate(); err != nil {
		return err
	}
	list, err := b.List(ctx)
	if err != nil {
		return err
	}
	status, found := "", false
	for _, s := range list {
		if s.ID == id {
			status, found = s.ClaudeStatus, true
			break
		}
	}
	if !found {
		return fmt.Errorf("%w: %s", session.ErrNotFound, id)
	}
	if err := session.PromptAllowed(status, allowWaiting); err != nil {
		return err
	}

	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return err
	}
	buf := "harness-prompt-" + hex.EncodeToString(rnd[:])
	if _, err := b.run.Output(ctx, "set-buffer", "-b", buf, "--", tmuxDataArg(text)); err != nil {
		return b.mapError(err, id)
	}
	// -p: bracketed paste when the app asked for it (Claude Code does), so
	// newlines don't submit early. -r: keep LF as LF (tmux would send CR).
	// -d: delete the buffer afterwards.
	if _, err := b.run.Output(ctx, "paste-buffer", "-p", "-r", "-d", "-b", buf, "-t", id); err != nil {
		// Best effort: the buffer survives a failed paste. Not ctx: it may be cancelled.
		dctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = b.run.Output(dctx, "delete-buffer", "-b", buf)
		return b.mapError(err, id)
	}
	select {
	case <-time.After(promptEnterDelay):
	case <-ctx.Done():
		return ctx.Err()
	}
	if _, err := b.run.Output(ctx, "send-keys", "-t", id, "Enter"); err != nil {
		return b.mapError(err, id)
	}
	b.kick()
	return nil
}

// tmuxDataArg protects a data argument from tmux's command-line parsing: an
// argument ending in ";" is taken as a command separator (the ";" is lost),
// and in one ending in `\;` the backslash is dropped. Escaping the final ";"
// as `\;` makes tmux keep the text exactly as given.
func tmuxDataArg(s string) string {
	if strings.HasSuffix(s, ";") {
		return s[:len(s)-1] + `\;`
	}
	return s
}
