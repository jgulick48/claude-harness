package claudehooks

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jgulick48/claude-harness/internal/session"
)

const slp = "/home/u/.claude/claude-harness/statusline.sh"

func slOf(t *testing.T, b []byte) map[string]any {
	t.Helper()
	m, _ := decode(t, b)["statusLine"].(map[string]any)
	return m
}

func TestStatusLineCommand(t *testing.T) {
	if got := StatusLineCommand("/a b/it's/s.sh"); got != `sh '/a b/it'\''s/s.sh'` {
		t.Fatal(got)
	}
}

func TestMergeStatusLine(t *testing.T) {
	ours := StatusLineCommand(slp)
	for _, c := range []struct {
		name, in         string
		wantOrig         string
		wantHad, wantAlr bool
		want             map[string]any
	}{
		{"empty", ``, "", false, false, map[string]any{"type": "command", "command": ours}},
		{"no statusLine", `{"model":"opus"}`, "", false, false, map[string]any{"type": "command", "command": ours}},
		{"null statusLine", `{"statusLine":null}`, "", false, false, map[string]any{"type": "command", "command": ours}},
		{"user command with padding", `{"statusLine":{"type":"command","command":"echo mine","padding":2,"x":[1]}}`,
			"echo mine", true, false, map[string]any{"type": "command", "command": ours, "padding": float64(2), "x": []any{float64(1)}}},
		{"object without command", `{"statusLine":{"padding":1}}`, "", false, false,
			map[string]any{"type": "command", "command": ours, "padding": float64(1)}},
		{"already ours", `{"statusLine":{"type":"command","command":` + jsq(ours) + `,"padding":3}}`, "", false, true,
			map[string]any{"type": "command", "command": ours, "padding": float64(3)}},
		{"ours, other form", `{"statusLine":{"type":"command","command":"/bin/sh ` + slp + `"}}`, "", false, true,
			map[string]any{"type": "command", "command": ours}},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, orig, had, already, err := MergeStatusLine([]byte(c.in), slp)
			if err != nil {
				t.Fatal(err)
			}
			if orig != c.wantOrig || had != c.wantHad || already != c.wantAlr {
				t.Errorf("orig=%q had=%v already=%v", orig, had, already)
			}
			if got := slOf(t, out); !reflect.DeepEqual(got, c.want) {
				t.Errorf("statusLine = %v, want %v", got, c.want)
			}
			if c.name == "no statusLine" && decode(t, out)["model"] != "opus" {
				t.Error("other keys lost")
			}
			// Idempotent: merging the result again changes nothing.
			out2, _, _, again, err := MergeStatusLine(out, slp)
			if err != nil || !again || string(out2) != string(out) {
				t.Errorf("not idempotent: again=%v err=%v", again, err)
			}
		})
	}
}

func jsq(s string) string { b, _ := json.Marshal(s); return string(b) }

func TestMergeStatusLineErrors(t *testing.T) {
	for _, in := range []string{`{"statusLine":"echo hi"}`, `{"statusLine":[1]}`, `{"statusLine":5}`, `{bad`} {
		if out, _, _, _, err := MergeStatusLine([]byte(in), slp); err == nil || out != nil {
			t.Errorf("%s: err=%v out=%s", in, err, out)
		}
		if _, err := UnmergeStatusLine([]byte(in), slp, "", false); err == nil {
			t.Errorf("unmerge %s: no error", in)
		}
	}
}

func TestUnmergeStatusLine(t *testing.T) {
	user := `{"model":"opus","statusLine":{"type":"command","command":"echo mine","padding":2}}`
	merged, orig, had, _, err := MergeStatusLine([]byte(user), slp)
	if err != nil || !had {
		t.Fatal(err, had)
	}
	out, err := UnmergeStatusLine(merged, slp, orig, had)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decode(t, out), decode(t, []byte(user))) {
		t.Errorf("not restored: %s", out)
	}

	none := `{"model":"opus"}`
	merged, orig, had, _, _ = MergeStatusLine([]byte(none), slp)
	out, _ = UnmergeStatusLine(merged, slp, orig, had)
	if !reflect.DeepEqual(decode(t, out), decode(t, []byte(none))) {
		t.Errorf("statusLine not removed: %s", out)
	}

	// Not ours: untouched.
	foreign := []byte(user)
	out, err = UnmergeStatusLine(foreign, slp, "zzz", true)
	if err != nil || string(out) != user {
		t.Errorf("foreign changed: %v %s", err, out)
	}
	out, _ = UnmergeStatusLine([]byte(`{}`), slp, "", false)
	if !reflect.DeepEqual(decode(t, out), map[string]any{}) {
		t.Error(string(out))
	}
}

func TestStatusLineStatus(t *testing.T) {
	ours := `{"statusLine":{"type":"command","command":` + jsq(StatusLineCommand(slp)) + `}}`
	for _, c := range []struct {
		name, in string
		body     []byte
		want     string
	}{
		{"installed", ours, StatusLineScript(), session.HooksInstalled},
		{"script differs", ours, []byte("old"), session.HooksOutdated},
		{"script missing", ours, nil, session.HooksOutdated},
		{"old command form", `{"statusLine":{"type":"command","command":"/bin/sh ` + slp + `"}}`, StatusLineScript(), session.HooksOutdated},
		{"user's", `{"statusLine":{"type":"command","command":"echo"}}`, StatusLineScript(), session.HooksMissing},
		{"none", `{}`, nil, session.HooksMissing},
		{"bad json", `{`, nil, session.HooksUnknown},
		{"bad statusLine", `{"statusLine":3}`, nil, session.HooksUnknown},
	} {
		if got := StatusLineStatus([]byte(c.in), c.body, slp); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestStatusLineScriptHeader(t *testing.T) {
	s := string(StatusLineScript())
	if !strings.HasPrefix(s, "#!/bin/sh\n# claude-harness-statusline v1\n") || StatusLineVersion != 1 {
		t.Fatalf("bad header: %q", s[:50])
	}
	if strings.Contains(s, "set -e") {
		t.Error("must not set -e")
	}
}

// runScript runs the wrapper in dir with the given extra env and stdin.
func runScript(t *testing.T, dir, stdin string, env ...string) (stdout string, code int) {
	t.Helper()
	cmd := exec.Command("sh", filepath.Join(dir, "statusline.sh"))
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir}, env...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	if ee, ok := err.(*exec.ExitError); ok {
		return string(out), ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(out), 0
}

func scriptDir(t *testing.T, orig string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "claude harness")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "statusline.sh"), StatusLineScript(), 0o755); err != nil {
		t.Fatal(err)
	}
	if orig != "" {
		if err := os.WriteFile(filepath.Join(dir, "statusline-orig"), []byte(orig), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestStatusLineScriptRecordsAndPassesThrough(t *testing.T) {
	dir := scriptDir(t, "cat >/dev/null; printf 'ORIG-OUT'; exit 3\n")
	in := "{\"model\":{\"display_name\":\"Opus\"},\r\n \"x\":1}\n"
	out, code := runScript(t, dir, in, "TMUX=/tmp/sock,4242,0", "TMUX_PANE=%7")
	if out != "ORIG-OUT" || code != 3 {
		t.Fatalf("stdout %q exit %d", out, code)
	}
	b, err := os.ReadFile(filepath.Join(dir, "usage", "4242-7"))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(b), "\t", 2)
	if len(parts) != 2 || strings.Trim(parts[0], "0123456789") != "" || parts[0] == "" {
		t.Fatalf("bad line %q", b)
	}
	if want := "{\"model\":{\"display_name\":\"Opus\"}, \"x\":1}\n"; parts[1] != want {
		t.Errorf("payload %q, want %q", parts[1], want)
	}
	if strings.Count(string(b), "\n") != 1 || strings.Contains(string(b), "\r") {
		t.Errorf("not a single line: %q", b)
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "usage", "*.*")); len(m) != 0 {
		t.Errorf("temp files left: %v", m)
	}
	// The original command received the same input.
	dir2 := scriptDir(t, "cat\n")
	out, code = runScript(t, dir2, "hello", "TMUX=/s,1,0", "TMUX_PANE=%1")
	if out != "hello" || code != 0 {
		t.Errorf("orig did not get stdin: %q %d", out, code)
	}
}

func TestStatusLineScriptNoOrig(t *testing.T) {
	dir := scriptDir(t, "")
	out, code := runScript(t, dir, "{}", "TMUX=/tmp/sock,9,1", "TMUX_PANE=%2")
	if out != "" || code != 0 {
		t.Fatalf("stdout %q exit %d", out, code)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "usage", "9-2")); err != nil || !strings.HasSuffix(string(b), "\t{}\n") {
		t.Errorf("usage file %q %v", b, err)
	}
	// An empty orig file counts as none.
	if err := os.WriteFile(filepath.Join(dir, "statusline-orig"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, code = runScript(t, dir, "{}", "TMUX=/tmp/sock,9,1", "TMUX_PANE=%2"); out != "" || code != 0 {
		t.Errorf("empty orig: %q %d", out, code)
	}
}

func TestStatusLineScriptNoTmux(t *testing.T) {
	dir := scriptDir(t, "printf 'still runs'\n")
	for _, env := range [][]string{
		nil,
		{"TMUX=/s,1,0"},
		{"TMUX_PANE=%1"},
		{"TMUX=/s,abc,0", "TMUX_PANE=%1"},
		{"TMUX=/s,1,0", "TMUX_PANE=1"},
		{"TMUX=/s,1,0", "TMUX_PANE=%x"},
		{"TMUX=/s,../../x,0", "TMUX_PANE=%1"},
	} {
		out, code := runScript(t, dir, "{}", env...)
		if out != "still runs" || code != 0 {
			t.Errorf("%v: %q %d", env, out, code)
		}
		if _, err := os.Stat(filepath.Join(dir, "usage")); err == nil {
			t.Errorf("%v: usage dir created", env)
		}
	}
}

func TestStatusLineScriptRecordFailureIsSilent(t *testing.T) {
	dir := scriptDir(t, "printf ok\n")
	// A file where the usage dir should be makes the recording fail.
	if err := os.WriteFile(filepath.Join(dir, "usage"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	out, code := runScript(t, dir, "{}", "TMUX=/s,1,0", "TMUX_PANE=%1")
	if out != "ok" || code != 0 {
		t.Errorf("%q %d", out, code)
	}
}
