package tmuxbackend

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jgulick48/claude-harness/internal/session"
)

// After a resize, tmux and xterm.js re-wrap existing lines differently and the
// shell redraws its prompt, so the view must be re-seeded from tmux's screen;
// otherwise lines get overwritten (seen with long prompts after dragging a
// split divider). The re-seed is a clear followed by a fresh capture.
func TestResizeReseedsScreen(t *testing.T) {
	b := newTestBackend(t)
	s := mustCreate(t, b, session.CreateOptions{Dir: t.TempDir()})
	term := attach(t, b, s, 80, 24)
	<-term.Output() // initial seed

	var acc bytes.Buffer
	if err := term.Write([]byte("echo resize-$((2*3))\r")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, term, &acc, "resize-6")

	acc.Reset()
	if err := term.Resize(40, 20); err != nil {
		t.Fatal(err)
	}
	const clear = "\x1b[H\x1b[2J\x1b[3J"
	readUntil(t, term, &acc, clear)
	// The fresh capture after the clear still contains the earlier output.
	readUntil(t, term, &acc, "resize-6")
	after := acc.String()[strings.Index(acc.String(), clear)+len(clear):]
	if !strings.Contains(after, "resize-6") {
		t.Fatalf("re-seed after resize lacks earlier output: %q", after)
	}
}
