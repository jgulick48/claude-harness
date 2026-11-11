package hosts_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/jgulick48/claude-harness/internal/hosts"
	"github.com/jgulick48/claude-harness/internal/session"
	"github.com/jgulick48/claude-harness/internal/session/tmuxbackend"
	"github.com/jgulick48/claude-harness/internal/sshconn"
	"github.com/jgulick48/claude-harness/internal/sshconn/sshtest"
)

func tmuxOK(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	out, err := exec.Command("tmux", "-V").Output()
	if err != nil {
		t.Skipf("tmux -V: %v", err)
	}
	m := regexp.MustCompile(`(\d+)\.(\d+)`).FindStringSubmatch(string(out))
	if m == nil {
		t.Skipf("unparseable tmux version %q", out)
	}
	maj, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	if maj < 3 || (maj == 3 && min < 2) {
		t.Skipf("tmux >= 3.2 required: %q", out)
	}
}

func waitHost(t *testing.T, m *hosts.Manager, id, state string) session.Host {
	t.Helper()
	var h session.Host
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if b, ok := m.Backend(id); ok {
			h = b.Host(context.Background())
			if h.State == state {
				return h
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("host %s: state %q (err %q), want %q", id, h.State, h.Error, state)
	return h
}

func TestIntegrationSSHHostLifecycle(t *testing.T) {
	tmuxOK(t)
	t.Setenv("TMUX", "")
	rb := make([]byte, 4)
	rand.Read(rb)
	socket := "harness-hosts-test-" + hex.EncodeToString(rb)
	home := t.TempDir()
	key := sshtest.NewKey(t)
	server := sshtest.Start(t, sshtest.Options{
		AuthorizedKeys: []ssh.PublicKey{key.PublicKey()},
		Env:            []string{"SHELL=/bin/sh", "HOME=" + home},
	})
	t.Cleanup(func() {
		exec.Command("tmux", "-L", socket, "kill-server").Run()
		d := os.Getenv("TMUX_TMPDIR")
		if d == "" {
			d = "/tmp"
		}
		os.Remove(filepath.Join(d, fmt.Sprintf("tmux-%d", os.Getuid()), socket))
	})
	dir := t.TempDir()
	config := filepath.Join(dir, "config")
	known := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(config, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}

	m, err := hosts.New(hosts.Options{
		File: filepath.Join(dir, "hosts.json"),
		SSH: sshconn.Options{ConfigFile: config, KnownHostsFile: known,
			AgentSocket: sshtest.StartAgent(t, key), Timeout: 10 * time.Second},
		TmuxOptions: tmuxbackend.Options{Socket: socket, Config: "/dev/null", Home: home,
			PollInterval: 100 * time.Millisecond},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	ctx := context.Background()

	added, err := m.Add(ctx, fmt.Sprintf("%s@127.0.0.1:%d", u.Username, server.Port), "")
	if err != nil {
		t.Fatal(err)
	}
	h := waitHost(t, m, added.ID, session.HostKeyUnknown)
	if h.HostKey == nil || h.HostKey.Fingerprint != ssh.FingerprintSHA256(server.HostKey) {
		t.Fatalf("host key %+v", h.HostKey)
	}
	if err := m.TrustHostKey(ctx, added.ID, h.HostKey.Fingerprint); err != nil {
		t.Fatal(err)
	}
	waitHost(t, m, added.ID, session.HostConnected)

	b, _ := m.Backend(added.ID)
	s, err := b.Create(ctx, session.CreateOptions{Name: "hosts-it"})
	if err != nil {
		t.Fatal(err)
	}

	server.DropConnections()
	h = waitHost(t, m, added.ID, session.HostDisconnected)
	if h.Error == "" {
		t.Fatal("expected a disconnect reason")
	}
	if _, err := b.List(ctx); err == nil {
		t.Fatal("List succeeded while disconnected")
	}
	if err := m.Connect(ctx, added.ID); err != nil {
		t.Fatal(err)
	}
	waitHost(t, m, added.ID, session.HostConnected)
	list, err := b.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, x := range list {
		found = found || x.ID == s.ID
	}
	if !found {
		t.Fatalf("session %v not in %v after reconnect", s, list)
	}
}
