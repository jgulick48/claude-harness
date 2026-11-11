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
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/jgulick48/claude-harness/internal/session"
	"github.com/jgulick48/claude-harness/internal/sshconn"
	"github.com/jgulick48/claude-harness/internal/sshconn/sshtest"
	"github.com/jgulick48/claude-harness/internal/tmux"
)

// sshRig is an in-process SSH server whose exec'd commands run locally with
// HOME set to a temp dir, plus an isolated tmux socket.
type sshRig struct {
	t      *testing.T
	server *sshtest.Server
	key    ssh.Signer
	home   string
	socket string
	known  string
	config string
	agent  string
	target string
}

func newSSHRig(t *testing.T, extraEnv ...string) *sshRig {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	if out, err := exec.Command("tmux", "-V").Output(); err != nil || !tmuxAtLeast32(string(out)) {
		t.Skipf("tmux >= 3.2 required: %q %v", out, err)
	}
	t.Setenv("TMUX", "")
	rb := make([]byte, 4)
	rand.Read(rb)
	r := &sshRig{t: t, socket: "harness-ssh-test-" + hex.EncodeToString(rb), home: t.TempDir(), key: sshtest.NewKey(t)}
	r.server = sshtest.Start(t, sshtest.Options{
		AuthorizedKeys: []ssh.PublicKey{r.key.PublicKey()},
		Env:            append([]string{"SHELL=/bin/sh", "HOME=" + r.home}, extraEnv...),
	})
	dir := t.TempDir()
	r.config = filepath.Join(dir, "config")
	r.known = filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(r.config, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := sshconn.TrustHostKey(r.known, fmt.Sprintf("[127.0.0.1]:%d", r.server.Port), r.server.HostKey); err != nil {
		t.Fatal(err)
	}
	r.agent = sshtest.StartAgent(t, r.key)
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	r.target = fmt.Sprintf("%s@127.0.0.1:%d", u.Username, r.server.Port)

	t.Cleanup(func() {
		exec.Command("tmux", "-L", r.socket, "kill-server").Run()
		d := os.Getenv("TMUX_TMPDIR")
		if d == "" {
			d = "/tmp"
		}
		os.Remove(filepath.Join(d, fmt.Sprintf("tmux-%d", os.Getuid()), r.socket))
	})
	return r
}

func tmuxAtLeast32(v string) bool {
	m := versionRe.FindStringSubmatch(v)
	if m == nil {
		return true
	}
	var maj, min int
	fmt.Sscan(m[1], &maj)
	fmt.Sscan(m[2], &min)
	return maj > 3 || maj == 3 && min >= 2
}

func (r *sshRig) dial() *sshconn.Client {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, err := sshconn.Dial(ctx, r.target, sshconn.Options{ConfigFile: r.config, KnownHostsFile: r.known, AgentSocket: r.agent})
	if err != nil {
		r.t.Fatalf("dial: %v", err)
	}
	r.t.Cleanup(func() { c.Close() })
	return c
}

func (r *sshRig) backend(c *sshconn.Client, opts Options) *Backend {
	r.t.Helper()
	opts.Socket, opts.Config = r.socket, "/dev/null"
	if opts.PollInterval == 0 {
		opts.PollInterval = 100 * time.Millisecond
	}
	b, err := NewSSH(context.Background(), "box", "Box", c, opts)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { b.Close() })
	return b
}

func TestSSHHostAndLocate(t *testing.T) {
	r := newSSHRig(t)
	b := r.backend(r.dial(), Options{})
	h := b.Host(context.Background())
	if h.State != session.HostConnected || h.Kind != session.KindSSH || h.ID != "box" || h.Name != "Box" || h.Home != r.home || h.TmuxVersion == "" {
		t.Errorf("%+v", h)
	}
	bin := b.run.(*tmux.SSHRunner).Bin
	if !strings.HasPrefix(bin, "/") {
		t.Errorf("tmux not located as absolute path: %q", bin)
	}
}

func TestSSHLocateProbePaths(t *testing.T) {
	r := newSSHRig(t, "PATH=/nonexistent")
	real, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip(err)
	}
	old := tmuxProbePaths
	tmuxProbePaths = []string{"/nonexistent/tmux", real}
	t.Cleanup(func() { tmuxProbePaths = old })
	b := r.backend(r.dial(), Options{})
	if got := b.run.(*tmux.SSHRunner).Bin; got != real {
		t.Errorf("Bin = %q, want %q", got, real)
	}
	if h := b.Host(context.Background()); h.State != session.HostConnected {
		t.Errorf("%+v", h)
	}
}

func TestSSHTmuxNotFound(t *testing.T) {
	r := newSSHRig(t, "PATH=/nonexistent")
	old := tmuxProbePaths
	tmuxProbePaths = []string{"/nonexistent/tmux"}
	t.Cleanup(func() { tmuxProbePaths = old })
	b := r.backend(r.dial(), Options{})
	ctx := context.Background()
	h := b.Host(ctx)
	if h.State != session.HostUnavailable || h.Error != "tmux not found on Box" || h.Kind != session.KindSSH {
		t.Errorf("%+v", h)
	}
	if _, err := b.List(ctx); !errors.Is(err, session.ErrUnavailable) {
		t.Errorf("List: %v", err)
	}
	if _, err := b.Create(ctx, session.CreateOptions{Dir: "~"}); !errors.Is(err, session.ErrUnavailable) {
		t.Errorf("Create: %v", err)
	}
	if _, err := b.Attach(ctx, "x", 80, 24); !errors.Is(err, session.ErrUnavailable) {
		t.Errorf("Attach: %v", err)
	}
}

func TestSSHLifecycle(t *testing.T) {
	r := newSSHRig(t)
	b := r.backend(r.dial(), Options{})
	ctx := context.Background()
	if err := os.Mkdir(filepath.Join(r.home, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	realSub, _ := filepath.EvalSymlinks(filepath.Join(r.home, "sub"))

	s, err := b.Create(ctx, session.CreateOptions{Dir: "~/sub", Name: "Remote One"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Cwd != realSub || s.HarnessName != "Remote One" || !s.Managed {
		t.Errorf("%+v (want cwd %s)", s, realSub)
	}

	// The directory check runs on the host: a dir that exists only locally
	// under a name the remote lacks must be rejected. Here both are one
	// machine, so use a path that exists nowhere.
	_, err = b.Create(ctx, session.CreateOptions{Dir: "~/missing"})
	if !errors.Is(err, session.ErrBadRequest) {
		t.Errorf("bad dir: %v", err)
	}
	_, err = b.Create(ctx, session.CreateOptions{Dir: "/it's a \"dir\"; $(false)"})
	if !errors.Is(err, session.ErrBadRequest) {
		t.Errorf("quoting: %v", err)
	}

	list, err := b.List(ctx)
	if err != nil || len(list) != 1 || list[0].ID != s.ID {
		t.Fatalf("list: %v %v", list, err)
	}
	if err := b.Rename(ctx, s.ID, "Renamed"); err != nil {
		t.Fatal(err)
	}
	if list, _ = b.List(ctx); list[0].HarnessName != "Renamed" {
		t.Errorf("%+v", list[0])
	}

	term, err := b.Attach(ctx, s.ID, 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	if seed := <-term.Output(); len(seed) == 0 {
		t.Fatal("empty seed")
	}
	var acc bytes.Buffer
	if err := term.Write([]byte("echo harness-$((6*7))\r")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, term, &acc, "harness-42")
	if err := term.Resize(120, 40); err != nil {
		t.Fatal(err)
	}
	out, err := b.run.Output(ctx, "display", "-p", "-t", s.ID, "#{window_width}x#{window_height}")
	if err != nil || strings.TrimSpace(string(out)) != "120x40" {
		t.Errorf("size = %q %v", out, err)
	}

	if err := term.Close(); err != nil {
		t.Fatal(err)
	}
	for range term.Output() {
	}
	if term.Err() != nil {
		t.Errorf("Err after Close = %v", term.Err())
	}
	if list, err = b.List(ctx); err != nil || len(list) != 1 {
		t.Errorf("detach must keep the session: %v %v", list, err)
	}

	if err := b.Kill(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if list, err = b.List(ctx); err != nil || len(list) != 0 {
		t.Errorf("after kill: %v %v", list, err)
	}
	if err := b.Kill(ctx, s.ID); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("kill unknown: %v", err)
	}
}

func TestSSHKillEndsTerminalAsSessionEnded(t *testing.T) {
	r := newSSHRig(t)
	b := r.backend(r.dial(), Options{})
	ctx := context.Background()
	s, err := b.Create(ctx, session.CreateOptions{Dir: "~"})
	if err != nil {
		t.Fatal(err)
	}
	term, err := b.Attach(ctx, s.ID, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	<-term.Output()
	if err := b.Kill(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	for range term.Output() {
	}
	if e := term.Err(); e == nil || e.Error() != "session ended" {
		t.Errorf("Err = %v", e)
	}
}

func TestSSHConnectionLoss(t *testing.T) {
	r := newSSHRig(t)
	c := r.dial()
	b := r.backend(c, Options{})
	ctx := context.Background()
	s, err := b.Create(ctx, session.CreateOptions{Dir: "~", Name: "survivor"})
	if err != nil {
		t.Fatal(err)
	}
	term, err := b.Attach(ctx, s.ID, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	<-term.Output()

	got := make(chan []session.Session, 8)
	unsub := b.Subscribe(func(l []session.Session) { got <- l })
	defer unsub()

	r.server.DropConnections()

	timeout := time.After(15 * time.Second)
drain:
	for {
		select {
		case _, ok := <-term.Output():
			if !ok {
				break drain
			}
		case <-timeout:
			t.Fatal("terminal output never closed")
		}
	}
	if e := term.Err(); e == nil || e.Error() != "connection lost" {
		t.Errorf("Err = %v", e)
	}
	waitFor(t, func() bool { return b.isLost() })
	if _, err := b.List(ctx); !errors.Is(err, session.ErrUnavailable) {
		t.Errorf("List: %v", err)
	}
	if _, err := b.Create(ctx, session.CreateOptions{Dir: "~"}); !errors.Is(err, session.ErrUnavailable) {
		t.Errorf("Create: %v", err)
	}
	if err := b.Kill(ctx, s.ID); !errors.Is(err, session.ErrUnavailable) {
		t.Errorf("Kill: %v", err)
	}
	if err := b.Rename(ctx, s.ID, "x"); !errors.Is(err, session.ErrUnavailable) {
		t.Errorf("Rename: %v", err)
	}
	if _, err := b.Attach(ctx, s.ID, 80, 24); !errors.Is(err, session.ErrUnavailable) {
		t.Errorf("Attach: %v", err)
	}
	if h := b.Host(ctx); h.State == session.HostConnected || h.Error != "connection lost" {
		t.Errorf("%+v", h)
	}
	b.subsMu.Lock()
	polling := b.stopPoll != nil
	b.subsMu.Unlock()
	if polling {
		t.Error("still polling after connection loss")
	}

	// The tmux session lives on; a fresh connection and backend see it.
	b2 := r.backend(r.dial(), Options{})
	list, err := b2.List(ctx)
	if err != nil || len(list) != 1 || list[0].Name != "survivor" {
		t.Errorf("after reconnect: %v %v", list, err)
	}
}
