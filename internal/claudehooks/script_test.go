package claudehooks

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
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

func TestScriptFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	work := t.TempDir()
	scriptPath := filepath.Join(work, "status.sh")
	if err := os.WriteFile(scriptPath, Script(), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(work, "st")
	run := func(t *testing.T, json string) {
		t.Helper()
		cmd := exec.Command("sh", scriptPath)
		cmd.Env = cleanEnv(work, "HARNESS_STATUS_FILE="+file)
		cmd.Stdin = strings.NewReader(json)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Run(); err != nil {
			t.Fatalf("exit: %v", err)
		}
		if out.Len() != 0 {
			t.Fatalf("printed %q", out.String())
		}
	}
	ev := func(name string) string { return `{"hook_event_name":"` + name + `"}` }
	notif := func(typ string) string {
		return `{"hook_event_name":"Notification","notification_type":"` + typ + `"}`
	}
	cases := []struct{ name, json, pre, want string }{
		{"working", ev("UserPromptSubmit"), "", "working"},
		{"waiting", ev("PermissionRequest"), "working 1", "waiting"},
		{"notif waiting", notif("permission_prompt"), "working 1", "waiting"},
		{"idle", ev("Stop"), "working 1", "idle"},
		{"SessionEnd truncates", ev("SessionEnd"), "working 1", ""},
		{"notif other unchanged", notif("idle_prompt"), "working 1", "working"},
		{"unknown event unchanged", ev("Whatever"), "idle 1", "idle"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			os.Remove(file)
			if c.pre != "" {
				if err := os.WriteFile(file, []byte(c.pre+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			run(t, c.json)
			data, err := os.ReadFile(file)
			if c.pre == "" && c.want == "" {
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			f := strings.Fields(string(data))
			if c.want == "" {
				if len(f) != 0 {
					t.Errorf("file = %q, want empty", data)
				}
				return
			}
			if len(f) != 2 || f[0] != c.want {
				t.Fatalf("file = %q, want state %q", data, c.want)
			}
			if c.pre == "" || c.want != strings.Fields(c.pre)[0] {
				if at, err := strconv.ParseInt(f[1], 10, 64); err != nil || at < time.Now().Unix()-60 {
					t.Errorf("bad epoch in %q", data)
				}
				if fi, _ := os.Stat(file); fi.Mode().Perm() != 0o644 {
					t.Errorf("mode = %v", fi.Mode().Perm())
				}
			}
		})
	}
}
