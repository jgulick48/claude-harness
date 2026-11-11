package tmuxbackend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jgulick48/claude-harness/internal/claudehooks"
	"github.com/jgulick48/claude-harness/internal/session"
)

const origSettings = `{
  "model": "opus",
  "env": {"FOO": "bar"},
  "permissions": {"allow": ["Bash(ls)"]},
  "hooks": {
    "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "echo mine", "timeout": 3}]}],
    "Custom": [{"hooks": [{"type": "command", "command": "other"}]}]
  }
}
`

// guardConfigDir fails the test if dir is the real user's ~/.claude.
func guardConfigDir(t *testing.T, dir string) {
	t.Helper()
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		real := filepath.Join(u.HomeDir, ".claude")
		if filepath.Clean(dir) == real || strings.HasPrefix(filepath.Clean(dir), real+string(filepath.Separator)) {
			t.Fatalf("refusing to touch the real Claude config dir %s", real)
		}
	}
}

func jsonOf(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, b)
	}
	return m
}

func backups(t *testing.T, dir string) []string {
	m, _ := filepath.Glob(filepath.Join(dir, "settings.json.claude-harness-backup-*"))
	return m
}

func read(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// hooksScenario exercises a backend whose config dir is cfg.
func hooksScenario(t *testing.T, b *Backend, cfg string) {
	guardConfigDir(t, cfg)
	ctx := context.Background()
	settings := filepath.Join(cfg, "settings.json")
	script := filepath.Join(cfg, "claude-harness", "claude-status.sh")
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(origSettings), 0o640); err != nil {
		t.Fatal(err)
	}
	hostHooks := func() string { return b.Host(ctx).ClaudeHooks }

	if st, err := b.HooksStatus(ctx); err != nil || st != session.HooksMissing {
		t.Fatalf("initial status = %q, %v", st, err)
	}
	if err := b.InstallHooks(ctx); err != nil {
		t.Fatal(err)
	}
	if st, _ := b.HooksStatus(ctx); st != session.HooksInstalled {
		t.Fatalf("status after install = %q", st)
	}
	if got := hostHooks(); got != session.HooksInstalled {
		t.Errorf("Host().ClaudeHooks = %q", got)
	}
	if !bytes.Equal(read(t, script), claudehooks.Script()) {
		t.Error("script content differs")
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(script); fi.Mode().Perm() != 0o755 {
			t.Errorf("script mode %v", fi.Mode().Perm())
		}
		if fi, _ := os.Stat(settings); fi.Mode().Perm() != 0o640 {
			t.Errorf("settings mode %v (want existing mode kept)", fi.Mode().Perm())
		}
	}
	bk := backups(t, cfg)
	if len(bk) != 1 || string(read(t, bk[0])) != origSettings {
		t.Fatalf("backups = %v", bk)
	}
	merged := read(t, settings)
	m, orig := jsonOf(t, merged), jsonOf(t, []byte(origSettings))
	for _, k := range []string{"model", "env", "permissions"} {
		if !reflect.DeepEqual(m[k], orig[k]) {
			t.Errorf("%s not preserved", k)
		}
	}
	h := m["hooks"].(map[string]any)
	if !reflect.DeepEqual(h["Custom"], orig["hooks"].(map[string]any)["Custom"]) {
		t.Error("Custom hook not preserved")
	}
	if pre := h["PreToolUse"].([]any); len(pre) != 2 || !reflect.DeepEqual(pre[0], orig["hooks"].(map[string]any)["PreToolUse"].([]any)[0]) {
		t.Errorf("PreToolUse = %v", pre)
	}
	for _, ev := range claudehooks.Events {
		if _, ok := h[ev]; !ok {
			t.Errorf("event %s not registered", ev)
		}
	}

	// Second install: no change, no new backup.
	before := read(t, settings)
	fi1, _ := os.Stat(settings)
	time.Sleep(1100 * time.Millisecond)
	if err := b.InstallHooks(ctx); err != nil {
		t.Fatal(err)
	}
	fi2, _ := os.Stat(settings)
	if !bytes.Equal(before, read(t, settings)) || !fi1.ModTime().Equal(fi2.ModTime()) || len(backups(t, cfg)) != 1 {
		t.Error("second install changed something")
	}

	// Outdated script is detected, then repaired.
	if err := os.WriteFile(script, []byte("#!/bin/sh\n# old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if st, _ := b.HooksStatus(ctx); st != session.HooksOutdated {
		t.Errorf("stale script status = %q", st)
	}
	if err := b.InstallHooks(ctx); err != nil {
		t.Fatal(err)
	}
	if st, _ := b.HooksStatus(ctx); st != session.HooksInstalled {
		t.Errorf("status after repair = %q", st)
	}

	// Remove.
	if err := b.RemoveHooks(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(jsonOf(t, read(t, settings)), orig) {
		t.Errorf("remove did not restore original:\n%s", read(t, settings))
	}
	if _, err := os.Stat(script); !os.IsNotExist(err) {
		t.Errorf("script still present: %v", err)
	}
	if st, _ := b.HooksStatus(ctx); st != session.HooksMissing {
		t.Errorf("status after remove = %q", st)
	}
	if got := hostHooks(); got != session.HooksMissing {
		t.Errorf("Host().ClaudeHooks after remove = %q", got)
	}
	if err := b.RemoveHooks(ctx); err != nil { // idempotent
		t.Errorf("second remove: %v", err)
	}

	// Invalid JSON is refused and left alone.
	bad := []byte(`{"model": "opus",,}`)
	if err := os.WriteFile(settings, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	nb := len(backups(t, cfg))
	for name, fn := range map[string]func(context.Context) error{"install": b.InstallHooks, "remove": b.RemoveHooks} {
		err := fn(ctx)
		if !errors.Is(err, session.ErrBadRequest) || !strings.Contains(err.Error(), "settings.json") {
			t.Errorf("%s on invalid JSON: %v", name, err)
		}
		if !bytes.Equal(read(t, settings), bad) || len(backups(t, cfg)) != nb {
			t.Errorf("%s touched the invalid file", name)
		}
	}
	if _, err := os.Stat(script); !os.IsNotExist(err) {
		t.Error("script written despite invalid settings")
	}
}

func TestHooksLocal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	home := t.TempDir()
	cfg := filepath.Join(home, "claude")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	t.Setenv("TMUX", "")
	b := NewLocal(Options{Home: home, Socket: "harness-hooks-unused", Config: "/dev/null", PollInterval: time.Hour})
	t.Cleanup(func() { b.Close() })
	hooksScenario(t, b, cfg)
}

func TestHooksLocalDefaultDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	b := NewLocal(Options{Home: home, Socket: "harness-hooks-unused", Config: "/dev/null", PollInterval: time.Hour})
	t.Cleanup(func() { b.Close() })
	if err := b.InstallHooks(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "claude-harness", "claude-status.sh")); err != nil {
		t.Error(err)
	}
	if st, _ := b.HooksStatus(context.Background()); st != session.HooksInstalled {
		t.Errorf("status %q", st)
	}
}

func TestHooksSSH(t *testing.T) {
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "claude cfg") // a space exercises quoting
	t.Setenv("HOME", tmp)
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	r := newSSHRig(t, "CLAUDE_CONFIG_DIR="+cfg)
	b := r.backend(r.dial(), Options{})
	hooksScenario(t, b, cfg)
}

func TestHooksSSHDefaultDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	r := newSSHRig(t, "CLAUDE_CONFIG_DIR=") // empty: falls back to $HOME/.claude
	guardConfigDir(t, filepath.Join(r.home, ".claude"))
	b := r.backend(r.dial(), Options{})
	ctx := context.Background()
	if err := b.InstallHooks(ctx); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(r.home, ".claude", "claude-harness", "claude-status.sh")
	if !bytes.Equal(read(t, p), claudehooks.Script()) {
		t.Error("script not at $HOME/.claude")
	}
	fi, _ := os.Stat(filepath.Join(r.home, ".claude", "settings.json"))
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("new settings mode %v", fi.Mode().Perm())
	}
	if len(backups(t, filepath.Join(r.home, ".claude"))) != 0 {
		t.Error("backup made for a file that did not exist")
	}
}

func TestHooksSSHConnectionLost(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(tmp, "c"))
	r := newSSHRig(t, "CLAUDE_CONFIG_DIR="+filepath.Join(tmp, "c"))
	c := r.dial()
	b := r.backend(c, Options{})
	c.Close()
	waitFor(t, func() bool {
		_, err := b.HooksStatus(context.Background())
		return errors.Is(err, session.ErrUnavailable)
	})
	if err := b.InstallHooks(context.Background()); !errors.Is(err, session.ErrUnavailable) {
		t.Errorf("install: %v", err)
	}
}
