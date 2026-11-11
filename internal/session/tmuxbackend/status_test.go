package tmuxbackend

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/jgulick48/claude-harness/internal/session"
)

func TestDeriveClaudeStatus(t *testing.T) {
	for _, c := range []struct{ opt, cmd, want string }{
		{"waiting", "bash", ""}, {"working", "-zsh", ""}, {"idle", "fish", ""},
		{"waiting", "sh", ""}, {"waiting", "dash", ""}, {"waiting", "ksh", ""}, {"waiting", "tcsh", ""},
		{"", "claude", "unknown"}, {"bogus", "claude", "unknown"},
		{"waiting", "claude", "waiting"}, {"working", "node", "working"}, {"idle", "sleep", "idle"},
		{"", "sleep", ""}, {"bogus", "vim", ""}, {"waiting", "", "waiting"}, {"", "", ""},
		{"waiting", "claude", "waiting"},
	} {
		if got := deriveClaudeStatus(c.opt, c.cmd); got != c.want {
			t.Errorf("deriveClaudeStatus(%q,%q)=%q want %q", c.opt, c.cmd, got, c.want)
		}
	}
}

func listOne(t *testing.T, b *Backend) session.Session {
	t.Helper()
	l, err := b.List(context.Background())
	if err != nil || len(l) != 1 {
		t.Fatalf("list: %v %v", l, err)
	}
	return l[0]
}

// waitStatus polls List until the status matches (send-keys is async).
func waitStatus(t *testing.T, b *Backend, want string) session.Session {
	t.Helper()
	var s session.Session
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if s = listOne(t, b); s.ClaudeStatus == want {
			return s
		}
	}
	t.Fatalf("status %q, want %q (pane cmd %q)", s.ClaudeStatus, want, "")
	return s
}

func waitPaneCmd(t *testing.T, b *Backend, target, want string) {
	t.Helper()
	var got string
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if got = tm(t, b, "display-message", "-p", "-t", target, "#{pane_current_command}"); got == want {
			return
		}
	}
	t.Fatalf("pane command %q, want %q", got, want)
}

func TestClaudeStatusFromTmux(t *testing.T) {
	b := newTestBackend(t)
	s := mustCreate(t, b, session.CreateOptions{Dir: t.TempDir()})
	// Fresh shell: nothing.
	if got := listOne(t, b); got.ClaudeStatus != "" {
		t.Fatalf("shell status %q", got.ClaudeStatus)
	}
	tm(t, b, "set-option", "-t", s.ID, "@claude_status", "waiting")
	tm(t, b, "set-option", "-t", s.ID, "@claude_status_at", "1794420400")
	// Still a shell: stale-guard.
	if got := listOne(t, b); got.ClaudeStatus != "" || got.ClaudeStatusAt != 0 {
		t.Fatalf("shell guard: %+v", got)
	}
	tm(t, b, "send-keys", "-t", s.ID, "exec sleep 600", "Enter")
	waitPaneCmd(t, b, s.ID, "sleep")
	got := listOne(t, b)
	if got.ClaudeStatus != "waiting" || got.ClaudeStatusAt != 1794420400 {
		t.Fatalf("waiting: %+v", got)
	}
	tm(t, b, "set-option", "-u", "-t", s.ID, "@claude_status")
	if got := listOne(t, b); got.ClaudeStatus != "" {
		t.Fatalf("cleared: %+v", got)
	}
}

func TestClaudeStatusShellGuardAfterExec(t *testing.T) {
	b := newTestBackend(t)
	s := mustCreate(t, b, session.CreateOptions{Dir: t.TempDir()})
	tm(t, b, "send-keys", "-t", s.ID, "exec sleep 600", "Enter")
	waitPaneCmd(t, b, s.ID, "sleep")
	tm(t, b, "respawn-pane", "-k", "-t", s.ID, "sh")
	// /bin/sh is dash on Debian/Ubuntu and bash on macOS; tmux reports the real name.
	waitPaneCmdIn(t, b, s.ID, "sh", "bash", "dash")
	tm(t, b, "set-option", "-t", s.ID, "@claude_status", "waiting")
	if got := listOne(t, b); got.ClaudeStatus != "" {
		t.Fatalf("shell guard: %+v", got)
	}
}

func waitPaneCmdIn(t *testing.T, b *Backend, target string, want ...string) {
	t.Helper()
	var got string
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		got = tm(t, b, "display-message", "-p", "-t", target, "#{pane_current_command}")
		if slices.Contains(want, got) {
			return
		}
	}
	t.Fatalf("pane command %q, want one of %v", got, want)
}

// fakeClaude builds a tiny Go program named "claude" that just sleeps, so tmux
// reports pane_current_command "claude". (Copying the sleep binary doesn't
// work everywhere: newer Ubuntu ships a multi-call coreutils that dispatches on
// its own name and exits when it's called "claude".) The real claude is never
// run.
func fakeClaude(t *testing.T) string {
	t.Helper()
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte(fakeClaudeSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "claude")
	cmd := exec.Command(gobin, "build", "-o", p, src)
	cmd.Env = append(os.Environ(), "GOFLAGS=", "GO111MODULE=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake claude: %v\n%s", err, out)
	}
	return p
}

const fakeClaudeSrc = `package main

import (
	"os"
	"strconv"
	"time"
)

func main() {
	n := 600
	if len(os.Args) > 1 {
		if v, err := strconv.Atoi(os.Args[1]); err == nil {
			n = v
		}
	}
	time.Sleep(time.Duration(n) * time.Second)
}
`

func TestClaudeStatusUnknownForClaudeCommand(t *testing.T) {
	b := newTestBackend(t)
	bin := fakeClaude(t)
	s := mustCreate(t, b, session.CreateOptions{Dir: t.TempDir()})
	tm(t, b, "send-keys", "-t", s.ID, "exec "+bin+" 600", "Enter")
	waitPaneCmd(t, b, s.ID, "claude")
	waitStatus(t, b, "unknown")
	tm(t, b, "set-option", "-t", s.ID, "@claude_status", "working")
	waitStatus(t, b, "working")
}

func TestSubscribeStatusChange(t *testing.T) {
	b := newTestBackend(t)
	s := mustCreate(t, b, session.CreateOptions{Dir: t.TempDir()})
	tm(t, b, "send-keys", "-t", s.ID, "exec sleep 600", "Enter")
	waitPaneCmd(t, b, s.ID, "sleep")
	ch := make(chan []session.Session, 16)
	unsub := b.Subscribe(func(l []session.Session) { ch <- l })
	defer unsub()
	<-ch // initial
	tm(t, b, "set-option", "-t", s.ID, "@claude_status", "working")
	for end := time.After(5 * time.Second); ; {
		select {
		case l := <-ch:
			if len(l) == 1 && l[0].ClaudeStatus == "working" {
				return
			}
		case <-end:
			t.Fatal("no notification for status change")
		}
	}
}
