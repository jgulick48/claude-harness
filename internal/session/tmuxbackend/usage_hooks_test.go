package tmuxbackend

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jgulick48/claude-harness/internal/claudehooks"
	"github.com/jgulick48/claude-harness/internal/session"
)

const userStatusLine = `{
  "model": "opus",
  "statusLine": {"type": "command", "command": "echo mine", "padding": 2}
}
`

// usageScenario exercises the status line wrapper install on a backend whose
// config dir is cfg.
func usageScenario(t *testing.T, b *Backend, cfg string) {
	guardConfigDir(t, cfg)
	ctx := context.Background()
	settings := filepath.Join(cfg, "settings.json")
	dir := filepath.Join(cfg, "claude-harness")
	script, origFile := filepath.Join(dir, "statusline.sh"), filepath.Join(dir, "statusline-orig")
	ours := claudehooks.StatusLineCommand(script)
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	statusLine := func() map[string]any {
		sl, _ := jsonOf(t, read(t, settings))["statusLine"].(map[string]any)
		return sl
	}
	reset := func(content string) {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(settings, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// 1. A user status line is wrapped, and kept in the orig file.
	reset(userStatusLine)
	if err := b.InstallHooks(ctx, true); err != nil {
		t.Fatal(err)
	}
	if want := (map[string]any{"type": "command", "command": ours, "padding": float64(2)}); !reflect.DeepEqual(statusLine(), want) {
		t.Fatalf("statusLine = %v", statusLine())
	}
	if string(read(t, origFile)) != "echo mine" {
		t.Errorf("orig file = %q", read(t, origFile))
	}
	if !bytes.Equal(read(t, script), claudehooks.StatusLineScript()) {
		t.Error("wrapper script content differs")
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(script); fi.Mode().Perm() != 0o755 {
			t.Errorf("script mode %v", fi.Mode().Perm())
		}
		if fi, _ := os.Stat(origFile); fi.Mode().Perm() != 0o600 {
			t.Errorf("orig mode %v", fi.Mode().Perm())
		}
	}
	if bk := backups(t, cfg); len(bk) != 1 {
		t.Errorf("want exactly one backup (hooks and statusLine written together), got %v", bk)
	}
	if h := b.Host(ctx); h.ClaudeUsage != session.HooksInstalled || h.ClaudeHooks != session.HooksInstalled {
		t.Errorf("host = hooks %q usage %q", h.ClaudeHooks, h.ClaudeUsage)
	}

	// 2. Reinstall is idempotent and does not overwrite orig with our command.
	before := read(t, settings)
	if err := b.InstallHooks(ctx, true); err != nil {
		t.Fatal(err)
	}
	if string(read(t, origFile)) != "echo mine" || !bytes.Equal(before, read(t, settings)) || len(backups(t, cfg)) != 1 {
		t.Errorf("reinstall changed things: orig=%q", read(t, origFile))
	}

	// An outdated wrapper is detected and refreshed without touching orig.
	if err := os.WriteFile(script, []byte("#!/bin/sh\n# old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if h := b.Host(ctx); h.ClaudeUsage != session.HooksOutdated {
		t.Errorf("stale wrapper: usage %q", h.ClaudeUsage)
	}
	b.invalidateHostCache()
	if err := b.InstallHooks(ctx, true); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(read(t, script), claudehooks.StatusLineScript()) || string(read(t, origFile)) != "echo mine" {
		t.Error("refresh failed")
	}

	// 3. usage=false restores the user's command and removes our files.
	if err := b.InstallHooks(ctx, false); err != nil {
		t.Fatal(err)
	}
	if want := (map[string]any{"type": "command", "command": "echo mine", "padding": float64(2)}); !reflect.DeepEqual(statusLine(), want) {
		t.Fatalf("statusLine after usage=false = %v", statusLine())
	}
	for _, f := range []string{script, origFile} {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("%s still present", f)
		}
	}
	if h := b.Host(ctx); h.ClaudeUsage != session.HooksMissing || h.ClaudeHooks != session.HooksInstalled {
		t.Errorf("host after usage=false = hooks %q usage %q", h.ClaudeHooks, h.ClaudeUsage)
	}

	// 4. RemoveHooks restores too.
	if err := b.InstallHooks(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := b.RemoveHooks(ctx); err != nil {
		t.Fatal(err)
	}
	want := jsonOf(t, []byte(userStatusLine))
	if !reflect.DeepEqual(jsonOf(t, read(t, settings)), want) {
		t.Errorf("remove did not restore:\n%s", read(t, settings))
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) && runtime.GOOS != "windows" {
		if m, _ := filepath.Glob(filepath.Join(dir, "*")); len(m) != 0 {
			t.Errorf("files left after remove: %v", m)
		}
	}

	// 5. No prior statusLine: installing adds ours; removing drops the key.
	reset(`{"model":"opus"}`)
	if err := b.InstallHooks(ctx, true); err != nil {
		t.Fatal(err)
	}
	if want := (map[string]any{"type": "command", "command": ours}); !reflect.DeepEqual(statusLine(), want) {
		t.Fatalf("statusLine = %v", statusLine())
	}
	if _, err := os.Stat(origFile); !os.IsNotExist(err) {
		t.Error("orig file written though there was no original")
	}
	if err := b.InstallHooks(ctx, true); err != nil { // idempotent
		t.Fatal(err)
	}
	if err := b.RemoveHooks(ctx); err != nil {
		t.Fatal(err)
	}
	if m := jsonOf(t, read(t, settings)); !reflect.DeepEqual(m, map[string]any{"model": "opus"}) {
		t.Errorf("after remove: %v", m)
	}

	// 6. A stale orig file from an earlier install is replaced, not reused.
	reset(userStatusLine)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(origFile, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.InstallHooks(ctx, true); err != nil {
		t.Fatal(err)
	}
	if string(read(t, origFile)) != "echo mine" {
		t.Errorf("orig = %q", read(t, origFile))
	}
	if err := b.RemoveHooks(ctx); err != nil {
		t.Fatal(err)
	}

	// 7. A malformed statusLine is refused and nothing is written.
	reset(`{"statusLine":"echo hi"}`)
	if err := b.InstallHooks(ctx, true); err == nil || !strings.Contains(err.Error(), "statusLine") {
		t.Errorf("malformed statusLine: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "claude-status.sh")); !os.IsNotExist(err) {
		t.Error("script written despite a bad statusLine")
	}
	if string(read(t, settings)) != `{"statusLine":"echo hi"}` {
		t.Error("settings touched")
	}
}

func TestUsageHooksLocal(t *testing.T) {
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
	usageScenario(t, b, cfg)
}

func TestUsageHooksSSH(t *testing.T) {
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "claude cfg")
	t.Setenv("HOME", tmp)
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	r := newSSHRig(t, "CLAUDE_CONFIG_DIR="+cfg)
	usageScenario(t, r.backend(r.dial(), Options{}), cfg)
}
