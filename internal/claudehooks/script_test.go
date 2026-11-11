package claudehooks

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// cleanEnv is os.Environ() without TMUX/TMUX_PANE (and a private HOME).
func cleanEnv(home string, extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_PANE=") ||
			strings.HasPrefix(kv, "HOME=") || strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") ||
			strings.HasPrefix(kv, "TMUX_TMPDIR=") || strings.HasPrefix(kv, "SHELL=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, append([]string{"HOME=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, "claude"), "SHELL=/bin/sh"}, extra...)...)
}

func TestScriptBehaviour(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	tmuxBin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "claude"))
	tmpdir, err := os.MkdirTemp("", "ht")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpdir) })
	rb := make([]byte, 4)
	rand.Read(rb)
	sock := "harness-hooktest-" + hex.EncodeToString(rb)
	env := cleanEnv(home, "TMUX_TMPDIR="+tmpdir)
	tm := func(args ...string) (string, error) {
		cmd := exec.Command(tmuxBin, append([]string{"-L", sock, "-f", "/dev/null"}, args...)...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	if out, err := tm("new-session", "-d", "-s", "t", "-x", "200", "-y", "50"); err != nil {
		t.Skipf("cannot start private tmux: %v %s", err, out)
	}
	t.Cleanup(func() { tm("kill-server") }) // private -L server only

	work := t.TempDir()
	scriptPath := filepath.Join(work, "status.sh")
	if err := os.WriteFile(scriptPath, Script(), 0o755); err != nil {
		t.Fatal(err)
	}
	stdoutF, rcF, jsonF := filepath.Join(work, "out"), filepath.Join(work, "rc"), filepath.Join(work, "in.json")

	status := func() string {
		out, err := tm("show-options", "-t", "t", "-v", "@claude_status")
		if err != nil {
			return "" // unset
		}
		return out
	}
	runHook := func(t *testing.T, json string) {
		t.Helper()
		os.Remove(rcF)
		os.Remove(stdoutF)
		if err := os.WriteFile(jsonF, []byte(json), 0o600); err != nil {
			t.Fatal(err)
		}
		// printf feeds the JSON like Claude does; the script runs in the pane.
		line := "printf '%s' \"$(cat " + jsonF + ")\" | sh " + scriptPath + " >" + stdoutF + " 2>&1; echo $? >" + rcF
		if out, err := tm("send-keys", "-t", "t", line, "Enter"); err != nil {
			t.Fatalf("send-keys: %v %s", err, out)
		}
		deadline := time.Now().Add(10 * time.Second)
		for {
			if b, err := os.ReadFile(rcF); err == nil && strings.HasSuffix(string(b), "\n") {
				if strings.TrimSpace(string(b)) != "0" {
					t.Fatalf("exit status %q", b)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("hook did not finish")
			}
			time.Sleep(20 * time.Millisecond)
		}
		if b, _ := os.ReadFile(stdoutF); len(b) != 0 {
			t.Fatalf("script printed output: %q", b)
		}
	}

	ev := func(name string) string { return `{"session_id":"x","hook_event_name":"` + name + `","cwd":"/a b"}` }
	notif := func(typ string) string {
		return `{"session_id":"x","hook_event_name": "Notification" ,"message":"hello \"there\"","notification_type" : "` + typ + `"}`
	}
	cases := []struct {
		name, json, pre, want string
	}{
		{"UserPromptSubmit", ev("UserPromptSubmit"), "", "working"},
		{"PreToolUse", ev("PreToolUse"), "idle", "working"},
		{"PostToolUse", ev("PostToolUse"), "waiting", "working"},
		{"PostToolUseFailure", ev("PostToolUseFailure"), "idle", "working"},
		{"PermissionRequest", ev("PermissionRequest"), "working", "waiting"},
		{"notif permission_prompt", notif("permission_prompt"), "working", "waiting"},
		{"notif elicitation_dialog", notif("elicitation_dialog"), "working", "waiting"},
		{"notif elicitation_url_dialog", notif("elicitation_url_dialog"), "working", "waiting"},
		{"notif agent_needs_input", notif("agent_needs_input"), "working", "waiting"},
		{"notif idle_prompt no change", notif("idle_prompt"), "working", "working"},
		{"notif auth_success no change", notif("auth_success"), "waiting", "waiting"},
		{"notif missing type no change", `{"hook_event_name":"Notification"}`, "working", "working"},
		{"Stop", ev("Stop"), "working", "idle"},
		{"StopFailure", ev("StopFailure"), "working", "idle"},
		{"SessionStart", ev("SessionStart"), "", "idle"},
		{"multiline JSON", "{\n  \"hook_event_name\":\n \"Stop\"\n}", "working", "idle"},
		{"unknown event no change", ev("Whatever"), "working", "working"},
		{"malformed no change", `{not json`, "working", "working"},
		{"empty input no change", ``, "working", "working"},
		{"SessionEnd clears", ev("SessionEnd"), "working", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.pre == "" {
				tm("set-option", "-u", "-t", "t", "@claude_status")
			} else if out, err := tm("set-option", "-t", "t", "@claude_status", c.pre); err != nil {
				t.Fatalf("%v %s", err, out)
			}
			runHook(t, c.json)
			if got := status(); got != c.want {
				t.Errorf("status = %q, want %q", got, c.want)
			}
		})
	}

	t.Run("timestamp set and cleared", func(t *testing.T) {
		runHook(t, ev("Stop"))
		at, err := tm("show-options", "-t", "t", "-v", "@claude_status_at")
		if err != nil || len(at) < 10 || strings.Trim(at, "0123456789") != "" {
			t.Fatalf("@claude_status_at = %q (%v)", at, err)
		}
		runHook(t, ev("SessionEnd"))
		if out, err := tm("show-options", "-t", "t", "-v", "@claude_status_at"); err == nil && out != "" {
			t.Errorf("@claude_status_at not cleared: %q", out)
		}
	})

	t.Run("outside tmux", func(t *testing.T) {
		tm("set-option", "-t", "t", "@claude_status", "working")
		cmd := exec.Command("sh", scriptPath)
		cmd.Env = cleanEnv(home, "TMUX_TMPDIR="+tmpdir)
		cmd.Stdin = strings.NewReader(ev("Stop"))
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Run(); err != nil {
			t.Fatalf("exit: %v", err)
		}
		if out.Len() != 0 {
			t.Errorf("printed %q", out.String())
		}
		if got := status(); got != "working" {
			t.Errorf("status changed to %q", got)
		}
	})
}
