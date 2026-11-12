package claudehooks

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jgulick48/claude-harness/internal/session"
)

const sp = "/home/u/.claude/claude-harness/claude-status.sh"

func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, b)
	}
	return m
}

func ourGroups(t *testing.T, b []byte, ev, path string) int {
	n := 0
	h, _ := decode(t, b)["hooks"].(map[string]any)
	for _, g := range h[ev].([]any) {
		for _, hd := range g.(map[string]any)["hooks"].([]any) {
			if strings.Contains(hd.(map[string]any)["command"].(string), path) {
				n++
			}
		}
	}
	return n
}

func TestScriptHeader(t *testing.T) {
	s := string(Script())
	if !strings.HasPrefix(s, "#!/bin/sh\n# claude-harness-status v2\n") {
		t.Fatalf("bad header: %q", s[:50])
	}
}

func TestMergeFresh(t *testing.T) {
	for name, in := range map[string]string{"empty": "", "blank": " \n", "object": "{}"} {
		t.Run(name, func(t *testing.T) {
			out, err := Merge([]byte(in), sp)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.HasSuffix(out, []byte("\n")) || !bytes.Contains(out, []byte("\n  ")) {
				t.Errorf("want indented JSON with trailing newline: %q", out)
			}
			for _, ev := range Events {
				if n := ourGroups(t, out, ev, sp); n != 1 {
					t.Errorf("%s: %d handlers", ev, n)
				}
			}
			if Status(out, Script(), sp) != session.HooksInstalled {
				t.Error("want installed")
			}
		})
	}
}

const existing = `{
  "model": "opus",
  "env": {"A": "1"},
  "big": 12345678901234567890,
  "permissions": {"allow": ["Bash(ls)"]},
  "hooks": {
    "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "echo hi", "timeout": 9}]}],
    "Custom": [{"hooks": [{"type": "command", "command": "x"}]}]
  }
}`

func TestMergePreservesOthers(t *testing.T) {
	out, err := Merge([]byte(existing), sp)
	if err != nil {
		t.Fatal(err)
	}
	got, want := decode(t, out), decode(t, []byte(existing))
	for _, k := range []string{"model", "env", "permissions"} {
		if !reflect.DeepEqual(got[k], want[k]) {
			t.Errorf("%s changed", k)
		}
	}
	if !strings.Contains(string(out), "12345678901234567890") {
		t.Error("large number mangled")
	}
	h := got["hooks"].(map[string]any)
	if !reflect.DeepEqual(h["Custom"], want["hooks"].(map[string]any)["Custom"]) {
		t.Error("Custom event changed")
	}
	pre := h["PreToolUse"].([]any)
	if len(pre) != 2 || !reflect.DeepEqual(pre[0], want["hooks"].(map[string]any)["PreToolUse"].([]any)[0]) {
		t.Errorf("PreToolUse: %v", pre)
	}
}

func TestMergeIdempotent(t *testing.T) {
	a, _ := Merge([]byte(existing), sp)
	b, err := Merge(a, sp)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("second merge changed output:\n%s\n---\n%s", a, b)
	}
}

func TestMergeReplacesOutdated(t *testing.T) {
	old := `{"hooks":{"Stop":[{"hooks":[
	  {"type":"command","command":"` + sp + ` --old","timeout":30},
	  {"type":"command","command":"keep-me"}]}],
	  "SessionEnd":[{"hooks":[{"type":"command","command":"sh ` + sp + `"}]}]}}`
	out, err := Merge([]byte(old), sp)
	if err != nil {
		t.Fatal(err)
	}
	if Status([]byte(old), Script(), sp) != session.HooksOutdated {
		t.Error("old settings should be outdated")
	}
	for _, ev := range Events {
		if n := ourGroups(t, out, ev, sp); n != 1 {
			t.Errorf("%s: %d handlers", ev, n)
		}
	}
	if strings.Contains(string(out), "--old") || strings.Contains(string(out), "sh "+sp) {
		t.Errorf("outdated entry survived:\n%s", out)
	}
	if !strings.Contains(string(out), "keep-me") {
		t.Error("foreign handler lost")
	}
	if Status(out, Script(), sp) != session.HooksInstalled {
		t.Error("want installed")
	}
}

func TestMergeInvalid(t *testing.T) {
	for _, in := range []string{`{`, `[]`, `"x"`, `{"a":1} {`, `{"hooks": 3}`, `{"hooks":{"Stop":{}}}`, `{"a":}`} {
		if out, err := Merge([]byte(in), sp); err == nil || out != nil {
			t.Errorf("Merge(%q) = %q, %v; want error", in, out, err)
		}
	}
	if _, err := Merge([]byte(`{`), sp); err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Errorf("error should say invalid JSON: %v", err)
	}
	if _, err := Unmerge([]byte(`{`), sp); err == nil {
		t.Error("Unmerge accepted invalid JSON")
	}
	if Status([]byte(`{`), Script(), sp) != session.HooksUnknown {
		t.Error("want unknown")
	}
}

func TestUnmerge(t *testing.T) {
	merged, _ := Merge([]byte(existing), sp)
	out, err := Unmerge(merged, sp)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decode(t, out), decode(t, []byte(existing))) {
		t.Errorf("unmerge did not restore the original:\n%s", out)
	}
	if Status(out, nil, sp) != session.HooksMissing {
		t.Error("want missing")
	}

	only, _ := Merge(nil, sp)
	out, err = Unmerge(only, sp)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decode(t, out), map[string]any{}) {
		t.Errorf("want {}, got %s", out)
	}

	// Our handler shares a group with a foreign one: only ours goes.
	shared := `{"hooks":{"Stop":[{"matcher":"m","hooks":[{"type":"command","command":"'` + sp + `'","timeout":5},{"type":"command","command":"other"}]}]}}`
	out, _ = Unmerge([]byte(shared), sp)
	want := `{"hooks":{"Stop":[{"matcher":"m","hooks":[{"type":"command","command":"other"}]}]}}`
	if !reflect.DeepEqual(decode(t, out), decode(t, []byte(want))) {
		t.Errorf("shared group: %s", out)
	}

	// Another script path is left alone.
	out, _ = Unmerge(merged, "/elsewhere/x.sh")
	if !reflect.DeepEqual(decode(t, out), decode(t, merged)) {
		t.Error("unmerge touched entries for another path")
	}
}

func TestStatus(t *testing.T) {
	full, _ := Merge(nil, sp)
	if got := Status(full, []byte("old script"), sp); got != session.HooksOutdated {
		t.Errorf("script differs: %s", got)
	}
	if got := Status(full, Script(), "/other.sh"); got != session.HooksMissing {
		t.Errorf("other path: %s", got)
	}
	// Partial: drop one event.
	m := decode(t, full)
	delete(m["hooks"].(map[string]any), "Stop")
	partial, _ := json.Marshal(m)
	if got := Status(partial, Script(), sp); got != session.HooksOutdated {
		t.Errorf("partial: %s", got)
	}
	for _, in := range []string{"", "{}", existing} {
		if got := Status([]byte(in), Script(), sp); got != session.HooksMissing {
			t.Errorf("%q: %s", in, got)
		}
	}
}

func TestPathWithSpacesAndQuotes(t *testing.T) {
	for _, p := range []string{"/Users/a b/.claude/claude-harness/s.sh", "/tmp/it's here/s.sh"} {
		out, err := Merge(nil, p)
		if err != nil {
			t.Fatal(err)
		}
		h := decode(t, out)["hooks"].(map[string]any)["Stop"].([]any)[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
		if h["command"] != Command(p) || h["type"] != "command" || h["timeout"] != float64(5) {
			t.Errorf("%s: %v", p, h)
		}
		if p == "/Users/a b/.claude/claude-harness/s.sh" && h["command"] != "'/Users/a b/.claude/claude-harness/s.sh'" {
			t.Errorf("not quoted: %v", h["command"])
		}
		if Status(out, Script(), p) != session.HooksInstalled {
			t.Errorf("%s: not installed", p)
		}
		again, _ := Merge(out, p)
		if !bytes.Equal(out, again) {
			t.Errorf("%s: not idempotent", p)
		}
	}
}
