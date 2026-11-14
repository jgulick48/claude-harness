package hosts

import (
	"context"
	"testing"

	"github.com/jgulick48/claude-harness/internal/session"
)

// stubInner is a connected inner backend that only answers Host.
type stubInner struct {
	session.Backend
	h session.Host
}

func (s stubInner) Host(context.Context) session.Host { return s.h }

// The wrapper must pass the inner backend's Claude hook and usage states
// through; without them the UI hides the hook and usage menu items for SSH
// hosts.
func TestSSHHostPassesHookStatus(t *testing.T) {
	h := newSSHHost(nil, "devbox", "devbox", "devbox")
	h.state = session.HostConnected
	h.inner = stubInner{h: session.Host{
		State: session.HostConnected, Home: "/home/u", TmuxVersion: "3.4",
		ClaudeHooks: session.HooksInstalled, ClaudeUsage: session.HooksMissing,
	}}
	got := h.Host(context.Background())
	if got.ClaudeHooks != session.HooksInstalled || got.ClaudeUsage != session.HooksMissing {
		t.Fatalf("claudeHooks=%q claudeUsage=%q, want installed/missing", got.ClaudeHooks, got.ClaudeUsage)
	}
	if got.Kind != session.KindSSH || got.ID != "devbox" || got.Home != "/home/u" {
		t.Fatalf("unexpected host %+v", got)
	}

	// Not connected: no inner state leaks through.
	h.state = session.HostDisconnected
	if got := h.Host(context.Background()); got.ClaudeHooks != "" || got.ClaudeUsage != "" {
		t.Fatalf("disconnected host reports hooks %q usage %q", got.ClaudeHooks, got.ClaudeUsage)
	}
}
