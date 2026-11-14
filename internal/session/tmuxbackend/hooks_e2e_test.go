package tmuxbackend

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jgulick48/claude-harness/internal/session"
)

// TestHooksEndToEnd wires the whole chain together: install the hooks, run the
// exact hook command Claude Code would run (taken from the installed
// settings.json) inside a tmux pane with a permission_prompt notification on
// stdin, keep a "claude" process in the foreground, and check that the
// session list now reports waiting.
func TestHooksEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	// Build before changing HOME: go build would otherwise use a fresh, empty
	// build cache under the temp HOME and compile the runtime from scratch.
	bin := fakeClaude(t)
	home := t.TempDir()
	cfg := filepath.Join(home, "claude")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	guardConfigDir(t, cfg)

	b := newTestBackend(t)
	ctx := context.Background()
	if err := b.InstallHooks(ctx, true); err != nil {
		t.Fatal(err)
	}
	if st, err := b.HooksStatus(ctx); err != nil || st != session.HooksInstalled {
		t.Fatalf("hooks status %q, %v", st, err)
	}

	// The command Claude Code would run for a Notification, from the real file.
	settings := jsonOf(t, read(t, filepath.Join(cfg, "settings.json")))
	groups := settings["hooks"].(map[string]any)["Notification"].([]any)
	cmd := groups[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)["command"].(string)

	s := mustCreate(t, b, session.CreateOptions{Dir: home})
	if s.ClaudeStatus != "" {
		t.Fatalf("fresh session status %q", s.ClaudeStatus)
	}
	line := `printf '%s' '{"hook_event_name":"Notification","notification_type":"permission_prompt","message":"Claude needs your permission"}' | ` +
		cmd + "; exec " + bin + " 600"
	tm(t, b, "send-keys", "-t", s.ID, "-l", line)
	tm(t, b, "send-keys", "-t", s.ID, "Enter")

	got := waitStatus(t, b, session.ClaudeWaiting)
	if got.ClaudeStatusAt == 0 {
		t.Fatalf("no ClaudeStatusAt: %+v", got)
	}
}
