package hosts_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/jgulick48/claude-harness/internal/hosts"
	"github.com/jgulick48/claude-harness/internal/session"
	"github.com/jgulick48/claude-harness/internal/session/tmuxbackend"
	"github.com/jgulick48/claude-harness/internal/sshconn"
	"github.com/jgulick48/claude-harness/internal/sshconn/sshtest"
)

// TestIntegrationThreeHostsAtOnce is M3's done criterion: three SSH hosts
// connected at the same time, each with its own sessions, all attached and
// usable concurrently. Each test server gets its own TMUX_TMPDIR, so each
// "host" runs a separate tmux server.
func TestIntegrationThreeHostsAtOnce(t *testing.T) {
	tmuxOK(t)
	t.Setenv("TMUX", "")
	const n = 3
	const socket = "harness-multihost"
	key := sshtest.NewKey(t)
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}

	servers := make([]*sshtest.Server, n)
	for i := range servers {
		home := t.TempDir()
		// Short path: unix socket paths are limited to ~104 bytes on macOS.
		tmpdir, err := os.MkdirTemp("", "hm")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			kill := exec.Command("tmux", "-L", socket, "kill-server")
			kill.Env = append(os.Environ(), "TMUX_TMPDIR="+tmpdir)
			_ = kill.Run()
			os.RemoveAll(tmpdir)
		})
		servers[i] = sshtest.Start(t, sshtest.Options{
			AuthorizedKeys: []ssh.PublicKey{key.PublicKey()},
			Env:            []string{"SHELL=/bin/sh", "HOME=" + home, "TMUX_TMPDIR=" + tmpdir},
		})
	}

	dir := t.TempDir()
	config := filepath.Join(dir, "config")
	if err := os.WriteFile(config, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := hosts.New(hosts.Options{
		File: filepath.Join(dir, "hosts.json"),
		SSH: sshconn.Options{ConfigFile: config, KnownHostsFile: filepath.Join(dir, "known_hosts"),
			AgentSocket: sshtest.StartAgent(t, key), Timeout: 10 * time.Second},
		TmuxOptions: tmuxbackend.Options{Socket: socket, Config: "/dev/null", PollInterval: 100 * time.Millisecond},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	ctx := context.Background()

	ids := make([]string, n)
	for i, srv := range servers {
		h, err := m.Add(ctx, fmt.Sprintf("%s@127.0.0.1:%d", u.Username, srv.Port), fmt.Sprintf("box%d", i))
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = h.ID
		h = waitHost(t, m, h.ID, session.HostKeyUnknown)
		if err := m.TrustHostKey(ctx, h.ID, h.HostKey.Fingerprint); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range ids {
		waitHost(t, m, id, session.HostConnected)
	}
	if got := len(m.Backends()); got != n { // no Local backend configured here
		t.Fatalf("%d backends, want %d", got, n)
	}

	// One session per host; each host lists only its own.
	sess := make([]session.Session, n)
	for i, id := range ids {
		b, _ := m.Backend(id)
		s, err := b.Create(ctx, session.CreateOptions{Name: fmt.Sprintf("work-%d", i)})
		if err != nil {
			t.Fatalf("create on %s: %v", id, err)
		}
		sess[i] = s
	}
	for i, id := range ids {
		b, _ := m.Backend(id)
		list, err := b.List(ctx)
		if err != nil || len(list) != 1 || list[0].ID != sess[i].ID || list[0].HarnessName != fmt.Sprintf("work-%d", i) {
			t.Fatalf("host %s lists %+v (err %v), want only work-%d", id, list, err, i)
		}
	}

	// Attach to all three at once and run a command in each concurrently.
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, _ := m.Backend(id)
			term, err := b.Attach(ctx, sess[i].ID, 80, 24)
			if err != nil {
				errs <- fmt.Errorf("attach %s: %w", id, err)
				return
			}
			defer term.Close()
			want := fmt.Sprintf("host-%d-ok-%d", i, 6*7)
			if err := term.Write([]byte(fmt.Sprintf("echo host-%d-ok-$((6*7))\r", i))); err != nil {
				errs <- err
				return
			}
			var acc bytes.Buffer
			timeout := time.After(10 * time.Second)
			for !strings.Contains(acc.String(), want) {
				select {
				case chunk, ok := <-term.Output():
					if !ok {
						errs <- fmt.Errorf("%s: output closed early: %v", id, term.Err())
						return
					}
					acc.Write(chunk)
				case <-timeout:
					errs <- fmt.Errorf("%s: no %q in %q", id, want, acc.String())
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
