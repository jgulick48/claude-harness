package tmuxbackend

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jgulick48/claude-harness/internal/session"
)

// newTestBackend returns a backend on a private tmux socket. It never touches
// the user's default tmux server.
func newTestBackend(t *testing.T) *Backend {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	t.Setenv("SHELL", "/bin/sh") // inherited by the tmux server we start
	t.Setenv("TMUX", "")
	rb := make([]byte, 4)
	rand.Read(rb)
	opts := Options{
		Socket: "harness-test-" + hex.EncodeToString(rb), Config: "/dev/null",
		Home: t.TempDir(), PollInterval: 100 * time.Millisecond,
	}
	b := NewLocal(opts)
	if h := b.Host(context.Background()); h.State != session.HostConnected {
		t.Skipf("tmux unusable: %+v", h)
	}
	t.Cleanup(func() {
		b.run.Output(context.Background(), "kill-server")
		dir := os.Getenv("TMUX_TMPDIR")
		if dir == "" {
			dir = "/tmp"
		}
		// tmux leaves the socket file behind.
		os.Remove(filepath.Join(dir, fmt.Sprintf("tmux-%d", os.Getuid()), opts.Socket))
	})
	t.Cleanup(func() { b.Close() })
	return b
}

func tm(t *testing.T, b *Backend, args ...string) string {
	t.Helper()
	out, err := b.run.Output(context.Background(), args...)
	if err != nil {
		t.Fatalf("tmux %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func mustCreate(t *testing.T, b *Backend, o session.CreateOptions) session.Session {
	t.Helper()
	s, err := b.Create(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestHostConnected(t *testing.T) {
	b := newTestBackend(t)
	h := b.Host(context.Background())
	if h.State != session.HostConnected || h.TmuxVersion == "" || h.Home == "" || h.ID != "local" || h.Name != "This computer" {
		t.Errorf("%+v", h)
	}
}

func TestListNoServer(t *testing.T) {
	b := newTestBackend(t)
	list, err := b.List(context.Background())
	if err != nil || list == nil || len(list) != 0 {
		t.Errorf("list = %v, err = %v", list, err)
	}
}

func TestCreate(t *testing.T) {
	b := newTestBackend(t)
	dir := t.TempDir()
	// macOS-style symlinked temp dirs: tmux reports the resolved path.
	real, _ := filepath.EvalSymlinks(dir)

	s := mustCreate(t, b, session.CreateOptions{Dir: dir, Name: "My Project!"})
	if s.Name != "My-Project" || s.HarnessName != "My Project!" || !s.Managed || s.Cwd != real || s.Windows != 1 || s.Created == 0 {
		t.Errorf("named: %+v", s)
	}
	s2 := mustCreate(t, b, session.CreateOptions{Dir: dir, Name: "My Project!"})
	if s2.Name != "My-Project-2" || s2.ID == s.ID {
		t.Errorf("duplicate name: %+v", s2)
	}
	s3 := mustCreate(t, b, session.CreateOptions{Dir: dir})
	if want := "h-" + filepath.Base(dir); !strings.HasPrefix(s3.Name, "h-") || s3.Name != sanitizeName(want) || s3.HarnessName != "" || !s3.Managed {
		t.Errorf("unnamed: %+v (want name %q)", s3, want)
	}
	s4 := mustCreate(t, b, session.CreateOptions{Dir: dir})
	if s4.Name != s3.Name+"-2" {
		t.Errorf("unnamed duplicate: %q vs %q", s4.Name, s3.Name)
	}
	if got := tm(t, b, "show-options", "-v", "-t", s.ID, "window-size"); got != "latest" {
		t.Errorf("window-size = %q", got)
	}

	home := mustCreate(t, b, session.CreateOptions{Dir: "~"})
	if realHome, _ := filepath.EvalSymlinks(b.home); home.Cwd != realHome {
		t.Errorf("home cwd %q", home.Cwd)
	}

	list, err := b.List(context.Background())
	if err != nil || len(list) != 5 {
		t.Fatalf("list: %v %v", list, err)
	}
	for i := 1; i < len(list); i++ {
		if list[i].Created < list[i-1].Created {
			t.Error("list not sorted by created")
		}
	}
}

func TestCreateBadRequests(t *testing.T) {
	b := newTestBackend(t)
	ctx := context.Background()
	_, err := b.Create(ctx, session.CreateOptions{Dir: filepath.Join(t.TempDir(), "missing")})
	if !errors.Is(err, session.ErrBadRequest) || !strings.Contains(err.Error(), "directory not found") {
		t.Errorf("bad dir: %v", err)
	}
	_, err = b.Create(ctx, session.CreateOptions{Dir: t.TempDir(), Command: "vim"})
	if !errors.Is(err, session.ErrBadRequest) {
		t.Errorf("bad command: %v", err)
	}
	if list, _ := b.List(ctx); len(list) != 0 {
		t.Errorf("failed creates left sessions: %v", list)
	}
}

func TestCreateClaudeCommand(t *testing.T) {
	b := newTestBackend(t)
	// Login shells reset PATH, so stub claude as a function in a throwaway
	// $HOME/.profile (and never run the real binary).
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".profile"), []byte("claude() { echo stub-claude-ran; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	s := mustCreate(t, b, session.CreateOptions{Dir: t.TempDir(), Command: session.CommandClaude})
	waitFor(t, func() bool {
		return strings.Contains(tm(t, b, "capture-pane", "-p", "-t", s.ID), "stub-claude-ran")
	})
	// /bin/sh is bash on macOS and dash on Debian/Ubuntu; either way a shell, not claude.
	if got := tm(t, b, "display", "-p", "-t", s.ID, "#{pane_current_command}"); got != "sh" && got != "bash" && got != "dash" {
		t.Errorf("pane command = %q, want the shell to survive", got)
	}
}

func TestExternalSessionNotManaged(t *testing.T) {
	b := newTestBackend(t)
	tm(t, b, "new-session", "-d", "-s", "raw", "-c", t.TempDir())
	list, err := b.List(context.Background())
	if err != nil || len(list) != 1 {
		t.Fatalf("%v %v", list, err)
	}
	if s := list[0]; s.Managed || s.HarnessName != "" || s.Name != "raw" {
		t.Errorf("%+v", s)
	}
}

func TestRenameKill(t *testing.T) {
	b := newTestBackend(t)
	ctx := context.Background()
	s := mustCreate(t, b, session.CreateOptions{Dir: t.TempDir(), Name: "first"})

	if err := b.Rename(ctx, s.ID, "Second name"); err != nil {
		t.Fatal(err)
	}
	list, _ := b.List(ctx)
	if list[0].HarnessName != "Second name" || list[0].Name != "first" {
		t.Errorf("%+v", list[0])
	}
	if err := b.Rename(ctx, s.ID, ""); err != nil {
		t.Fatal(err)
	}
	list, _ = b.List(ctx)
	if list[0].HarnessName != "" || !list[0].Managed {
		t.Errorf("after clear: %+v", list[0])
	}
	if err := b.Rename(ctx, s.ID, ""); err != nil { // clearing twice is fine
		t.Errorf("second clear: %v", err)
	}
	if err := b.Rename(ctx, "$999", "x"); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("rename unknown: %v", err)
	}

	if err := b.Kill(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if list, err := b.List(ctx); err != nil || len(list) != 0 {
		t.Errorf("after kill: %v %v", list, err)
	}
	if err := b.Kill(ctx, s.ID); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("kill unknown: %v", err)
	}
}

func TestSubscribe(t *testing.T) {
	b := newTestBackend(t)
	var mu sync.Mutex
	var calls [][]session.Session
	ch := make(chan struct{}, 32)
	unsub := b.Subscribe(func(l []session.Session) {
		mu.Lock()
		calls = append(calls, l)
		mu.Unlock()
		ch <- struct{}{}
	})
	defer unsub()
	expect := func(n int, what string) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatalf("no notification: %s", what)
		}
		mu.Lock()
		defer mu.Unlock()
		if got := len(calls[len(calls)-1]); got != n {
			t.Errorf("%s: %d sessions, want %d", what, got, n)
		}
	}
	expect(0, "initial")
	s := mustCreate(t, b, session.CreateOptions{Dir: t.TempDir()})
	expect(1, "create")
	if err := b.Kill(context.Background(), s.ID); err != nil {
		t.Fatal(err)
	}
	expect(0, "kill")

	// No change, no notification.
	select {
	case <-ch:
		t.Error("spurious notification")
	case <-time.After(400 * time.Millisecond):
	}
	// External changes are caught by polling.
	tm(t, b, "new-session", "-d", "-s", "ext")
	expect(1, "external create")

	unsub()
	unsub() // idempotent
	b.subsMu.Lock()
	running := b.stopPoll != nil
	b.subsMu.Unlock()
	if running {
		t.Error("poller still running with no subscribers")
	}
}

// attach helpers

func attach(t *testing.T, b *Backend, s session.Session, cols, rows int) session.Terminal {
	t.Helper()
	term, err := b.Attach(context.Background(), s.ID, cols, rows)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { term.Close() })
	return term
}

// readUntil accumulates output until it contains want.
func readUntil(t *testing.T, term session.Terminal, acc *bytes.Buffer, want string) {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for !strings.Contains(acc.String(), want) {
		select {
		case d, ok := <-term.Output():
			if !ok {
				t.Fatalf("output closed (err %v) before %q; got %q", term.Err(), want, acc.String())
			}
			acc.Write(d)
		case <-timeout:
			t.Fatalf("timeout waiting for %q; got %q", want, acc.String())
		}
	}
}

func TestAttachSeedWriteResizeClose(t *testing.T) {
	b := newTestBackend(t)
	s := mustCreate(t, b, session.CreateOptions{Dir: t.TempDir()})
	term := attach(t, b, s, 100, 30)

	seed := <-term.Output()
	if len(seed) == 0 {
		t.Fatal("empty seed")
	}
	if got := tm(t, b, "display", "-p", "-t", s.ID, "#{window_width}x#{window_height}"); got != "100x30" {
		t.Errorf("size after attach = %s", got)
	}

	var acc bytes.Buffer
	if err := term.Write([]byte("echo harness-$((6*7))\r")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, term, &acc, "harness-42")

	if err := term.Resize(120, 40); err != nil {
		t.Fatal(err)
	}
	if got := tm(t, b, "display", "-p", "-t", s.ID, "#{window_width}x#{window_height}"); got != "120x40" {
		t.Errorf("size after resize = %s", got)
	}

	if err := term.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := <-term.Output(); ok {
		// drain anything buffered, then it must close
		for range term.Output() {
		}
	}
	if term.Err() != nil {
		t.Errorf("Err after Close = %v", term.Err())
	}
	if list, _ := b.List(context.Background()); len(list) != 1 || list[0].ID != s.ID {
		t.Errorf("session did not survive Close: %v", list)
	}
}

func TestAttachSeedContainsHistory(t *testing.T) {
	b := newTestBackend(t)
	s := mustCreate(t, b, session.CreateOptions{Dir: t.TempDir()})
	tm(t, b, "send-keys", "-t", s.ID, "-l", `printf 'line1\nline2\n'`)
	tm(t, b, "send-keys", "-t", s.ID, "Enter")
	waitFor(t, func() bool {
		return strings.Contains(tm(t, b, "capture-pane", "-p", "-t", s.ID), "line2\n")
	})
	term := attach(t, b, s, 80, 24)
	seed := string(<-term.Output())
	if !strings.Contains(seed, "line1") || !strings.Contains(seed, "line2") {
		t.Errorf("seed lacks lines: %q", seed)
	}
	// Seed ends with an absolute cursor position.
	if !strings.Contains(seed[len(seed)-12:], "H") {
		t.Errorf("seed tail %q", seed[len(seed)-12:])
	}
	if n := strings.Count(seed, "\r\n") + 1; n != 24 {
		t.Errorf("seed has %d lines, want exactly 24 for a fresh 24-row window", n)
	}
}

func TestAttachDropsOutputBeforeSeed(t *testing.T) {
	b := newTestBackend(t)
	s := mustCreate(t, b, session.CreateOptions{Dir: t.TempDir()})
	tm(t, b, "send-keys", "-t", s.ID, "-l", "echo before-attach")
	tm(t, b, "send-keys", "-t", s.ID, "Enter")
	var pane string
	deadline := time.Now().Add(10 * time.Second)
	for {
		// -J joins wrapped lines: a long prompt (e.g. CI hostnames) can wrap the
		// typed command so "before-attach" is split across two screen lines.
		pane = tm(t, b, "capture-pane", "-p", "-J", "-t", s.ID)
		if strings.Count(pane, "before-attach") >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("command output never appeared; pane: %q; cmd=%q", pane,
				tm(t, b, "display", "-p", "-t", s.ID, "#{pane_current_command} #{cursor_x},#{cursor_y}"))
		}
		time.Sleep(50 * time.Millisecond)
	}
	term := attach(t, b, s, 80, 24)
	seed := string(<-term.Output())
	if !strings.Contains(seed, "before-attach") {
		t.Fatalf("seed: %q", seed)
	}
	if err := term.Write([]byte("echo after-attach\r")); err != nil {
		t.Fatal(err)
	}
	var acc bytes.Buffer
	readUntil(t, term, &acc, "after-attach")
	if strings.Contains(acc.String(), "before-attach") {
		t.Errorf("pre-seed output was replayed: %q", acc.String())
	}
}

func TestAttachAlternateScreen(t *testing.T) {
	b := newTestBackend(t)
	s := mustCreate(t, b, session.CreateOptions{Dir: t.TempDir()})
	tm(t, b, "send-keys", "-t", s.ID, "-l", `printf '\033[?1049hALTSCREEN'`)
	tm(t, b, "send-keys", "-t", s.ID, "Enter")
	waitFor(t, func() bool { return tm(t, b, "display", "-p", "-t", s.ID, "#{alternate_on}") == "1" })
	term := attach(t, b, s, 80, 24)
	seed := string(<-term.Output())
	if !strings.HasPrefix(seed, "\x1b[?1049h") || !strings.Contains(seed, "ALTSCREEN") {
		t.Errorf("seed: %q", seed)
	}
	if n := strings.Count(seed, "\r\n") + 1; n != 24 {
		t.Errorf("alt seed has %d lines", n)
	}
}

func TestAttachUnknownSession(t *testing.T) {
	b := newTestBackend(t)
	if _, err := b.Attach(context.Background(), "$42", 80, 24); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("no server: %v", err)
	}
	mustCreate(t, b, session.CreateOptions{Dir: t.TempDir()})
	if _, err := b.Attach(context.Background(), "$42", 80, 24); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("with server: %v", err)
	}
}

func TestAttachSessionKilled(t *testing.T) {
	b := newTestBackend(t)
	s := mustCreate(t, b, session.CreateOptions{Dir: t.TempDir()})
	term := attach(t, b, s, 80, 24)
	<-term.Output() // seed
	if err := b.Kill(context.Background(), s.ID); err != nil {
		t.Fatal(err)
	}
	timeout := time.After(10 * time.Second)
	for {
		select {
		case _, ok := <-term.Output():
			if ok {
				continue
			}
			if err := term.Err(); err == nil || err.Error() != "session ended" {
				t.Errorf("Err = %v", err)
			}
			return
		case <-timeout:
			t.Fatal("Output not closed after kill")
		}
	}
}

func TestCloseDoesNotDeadlockOnFullOutput(t *testing.T) {
	b := newTestBackend(t)
	s := mustCreate(t, b, session.CreateOptions{Dir: t.TempDir()})
	term := attach(t, b, s, 80, 24)
	// Generate far more output than the channel buffer and never read it.
	term.Write([]byte("yes | head -c 2000000\r"))
	waitFor(t, func() bool { return len(term.(*terminal).out) == cap(term.(*terminal).out) })
	done := make(chan struct{})
	go func() { term.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Close deadlocked")
	}
	if term.Err() != nil {
		t.Errorf("Err = %v", term.Err())
	}
}

func TestBackendCloseClosesTerminals(t *testing.T) {
	b := newTestBackend(t)
	s := mustCreate(t, b, session.CreateOptions{Dir: t.TempDir()})
	term := attach(t, b, s, 80, 24)
	b.Close()
	for range term.Output() {
	}
	if term.Err() != nil {
		t.Errorf("Err = %v", term.Err())
	}
	if _, err := b.Attach(context.Background(), s.ID, 80, 24); !errors.Is(err, session.ErrUnavailable) {
		t.Errorf("attach after close: %v", err)
	}
}

func TestWritePreservesOrderAcrossChunks(t *testing.T) {
	b := newTestBackend(t)
	s := mustCreate(t, b, session.CreateOptions{Dir: t.TempDir()})
	term := attach(t, b, s, 200, 30)
	payload := "echo " + strings.Repeat("0123456789", 60) + "\r" // 600+ bytes: several chunks
	if err := term.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	var acc bytes.Buffer
	readUntil(t, term, &acc, fmt.Sprintf("\r\n%s\r\n", strings.Repeat("0123456789", 60)))
}

func TestCreateRunAs(t *testing.T) {
	b := newTestBackend(t)
	bin := t.TempDir()
	rec := filepath.Join(t.TempDir(), "user")
	fake := "#!/bin/sh\n[ \"$1\" = -iu ] && [ \"$3\" = -- ] || exit 97\necho \"$2\" > " + rec + "\nshift 3\nexec \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "sudo"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	// An absolute path, not a PATH entry: macOS login shells (path_helper)
	// put the system directories, and the real sudo, first.
	b.sudo = filepath.Join(bin, "sudo")
	dir := t.TempDir()
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	s := mustCreate(t, b, session.CreateOptions{Dir: dir, Command: session.CommandShell, RunAs: "bob"})
	if s.RunAs != "bob" {
		t.Errorf("RunAs = %q", s.RunAs)
	}
	waitFor(t, func() bool {
		data, _ := os.ReadFile(rec)
		return strings.TrimSpace(string(data)) == "bob"
	})
	waitFor(t, func() bool {
		return tm(t, b, "display-message", "-p", "-t", s.ID, "#{pane_current_path}") == dir
	})
	list, err := b.List(context.Background())
	if err != nil || len(list) != 1 || list[0].RunAs != "bob" || list[0].Cwd != dir {
		t.Errorf("list = %+v, err = %v", list, err)
	}
	if _, err := b.Create(context.Background(), session.CreateOptions{RunAs: "bob;rm"}); !errors.Is(err, session.ErrBadRequest) {
		t.Errorf("bad user: err = %v", err)
	}
}
