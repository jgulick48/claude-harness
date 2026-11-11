package tmux

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testRunner(t *testing.T) *LocalRunner {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	b := make([]byte, 4)
	rand.Read(b)
	r := &LocalRunner{Socket: "harness-test-" + hex.EncodeToString(b), Config: "/dev/null"}
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("TMUX", "")
	t.Cleanup(func() { killServer(r) })
	return r
}

func TestLocalRunnerOutputError(t *testing.T) {
	r := testRunner(t)
	_, err := r.Output(context.Background(), "has-session", "-t", "nope")
	if err == nil || !strings.Contains(err.Error(), "no server running") && !strings.Contains(err.Error(), "error connecting") {
		t.Errorf("err = %v", err)
	}
	_, err = (&LocalRunner{Bin: "tmux-does-not-exist"}).Output(context.Background(), "-V")
	if !isNotFound(err) {
		t.Errorf("missing binary: %v", err)
	}
}

func isNotFound(err error) bool {
	return errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist)
}

func TestQuoteRoundTrip(t *testing.T) {
	r := testRunner(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := r.Output(ctx, "new-session", "-d", "-s", "q"); err != nil {
		t.Fatal(err)
	}
	proc, err := r.Start(ctx, "-C", "attach-session", "-t", "q")
	if err != nil {
		t.Fatal(err)
	}
	c := NewControlClient(proc, nil)
	defer c.Close()

	for _, s := range []string{
		"plain", "with space", "it's", "''", "'", `a\b`, `\`, `\'`, "$HOME ${X} $(id)", "a;b", "a ; b",
		"#{session_name} #(id) # hash", `"double"`, "~/tilde", "a{b}c", "unicode é 日本語 ✓ 🚀", "tab\there", "%percent", "",
	} {
		if _, err := c.Do(ctx, "set-option -g @x "+Quote(s)); err != nil {
			t.Errorf("set %q: %v", s, err)
			continue
		}
		out, err := r.Output(ctx, "show-options", "-gqv", "@x")
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSuffix(string(out), "\n"); got != s {
			t.Errorf("round trip %q -> %q", s, got)
		}
	}
}

// killServer stops the test server and removes its socket, which tmux leaves
// behind in $TMUX_TMPDIR (default /tmp)/tmux-<uid>/.
func killServer(r *LocalRunner) {
	r.Output(context.Background(), "kill-server")
	dir := os.Getenv("TMUX_TMPDIR")
	if dir == "" {
		dir = "/tmp"
	}
	os.Remove(filepath.Join(dir, fmt.Sprintf("tmux-%d", os.Getuid()), r.Socket))
}
