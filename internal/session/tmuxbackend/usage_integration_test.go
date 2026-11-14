package tmuxbackend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jgulick48/claude-harness/internal/session"
)

func TestListUsageIntegration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	cfg := filepath.Join(t.TempDir(), "claude")
	guardConfigDir(t, cfg)
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	b := newTestBackend(t)
	ctx := context.Background()

	now := time.Now()
	old := usageNow
	usageNow = func() time.Time { return now }
	t.Cleanup(func() { usageNow = old })

	mk := func(name string) session.Session {
		s := mustCreate(t, b, session.CreateOptions{Dir: t.TempDir(), Name: name})
		tm(t, b, "send-keys", "-t", s.ID, "exec sleep 600", "Enter")
		waitPaneCmd(t, b, s.ID, "sleep")
		tm(t, b, "set-option", "-t", s.ID, "@claude_status", "working")
		return s
	}
	s1, s2 := mk("one"), mk("two")
	pid := tm(t, b, "display-message", "-p", "#{pid}")
	pane := func(id string) string { return tm(t, b, "display-message", "-p", "-t", id, "#{pane_id}")[1:] }
	usageDir := filepath.Join(cfg, "claude-harness", "usage")
	write := func(pn string, at int64, body string) {
		if err := os.MkdirAll(usageDir, 0o700); err != nil {
			t.Fatal(err)
		}
		line := fmt.Sprintf("%d\t%s\n", at, body)
		if err := os.WriteFile(filepath.Join(usageDir, pid+"-"+pn), []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	byID := func() map[string]session.Session {
		l, err := b.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]session.Session{}
		for _, s := range l {
			m[s.ID] = s
		}
		return m
	}
	reset := strconv.FormatInt(now.Unix()+3600, 10)
	body := func(model string) string {
		return `{"model":{"display_name":"` + model + `"},"cost":{"total_cost_usd":1.5},"context_window":{"used_percentage":30},` +
			`"rate_limits":{"five_hour":{"used_percentage":20,"resets_at":` + reset + `}}}`
	}

	// No usage dir yet: nothing, and no error.
	if m := byID(); m[s1.ID].Usage != nil {
		t.Fatalf("usage without files: %+v", m[s1.ID].Usage)
	}

	// A reading for s1's pane, plus a foreign server's file for s2's pane.
	write(pane(s1.ID), now.Unix()-5, body("Opus"))
	if err := os.WriteFile(filepath.Join(usageDir, "999999-"+pane(s2.ID)), []byte(fmt.Sprintf("%d\t%s\n", now.Unix(), body("Other"))), 0o600); err != nil {
		t.Fatal(err)
	}
	if m := byID(); m[s1.ID].Usage != nil {
		t.Fatal("read again within the throttle interval")
	}
	now = now.Add(usageEvery + time.Second)
	m := byID()
	u := m[s1.ID].Usage
	if u == nil || u.Model != "Opus" || u.CostUSD == nil || *u.CostUSD != 1.5 || u.ContextPct == nil || *u.ContextPct != 30 ||
		u.Limits == nil || u.Limits.FiveHour == nil || u.Limits.FiveHour.UsedPct != 20 || u.At != now.Unix()-5-int64(usageEvery/time.Second)-1 {
		t.Fatalf("s1 usage: %+v", u)
	}
	if m[s2.ID].Usage != nil {
		t.Errorf("another server's file leaked into s2: %+v", m[s2.ID].Usage)
	}

	// Between reads the cached reading stays; a file change shows up only
	// after the interval.
	write(pane(s1.ID), now.Unix(), body("Sonnet"))
	if m := byID(); m[s1.ID].Usage == nil || m[s1.ID].Usage.Model != "Opus" {
		t.Errorf("cache not kept: %+v", m[s1.ID].Usage)
	}
	now = now.Add(usageEvery + time.Second)
	if m := byID(); m[s1.ID].Usage == nil || m[s1.ID].Usage.Model != "Sonnet" {
		t.Errorf("refresh missed: %+v", m[s1.ID].Usage)
	}

	// Change detection: the list differs by value when the reading does.
	l1, _ := b.List(ctx)
	l2, _ := b.List(ctx)
	if !reflect.DeepEqual(l1, l2) {
		t.Error("identical readings compare unequal")
	}

	// Usage only while Claude runs.
	tm(t, b, "set-option", "-u", "-t", s1.ID, "@claude_status")
	tm(t, b, "respawn-pane", "-k", "-t", s1.ID, "sh")
	waitPaneCmdIn(t, b, s1.ID, "sh", "bash", "dash")
	if m := byID(); m[s1.ID].ClaudeStatus != "" || m[s1.ID].Usage != nil {
		t.Errorf("usage while Claude is not running: %+v", m[s1.ID])
	}

	// Run-as sessions get none (phase 1).
	tm(t, b, "set-option", "-t", s2.ID, "@harness_run_as", "bob")
	write(pane(s2.ID), now.Unix(), body("RunAs"))
	now = now.Add(usageEvery + time.Second)
	if m := byID(); m[s2.ID].ClaudeStatus != "working" || m[s2.ID].RunAs != "bob" || m[s2.ID].Usage != nil {
		t.Errorf("run-as session: %+v", m[s2.ID])
	}
}

// ReadTranscripts through a real tmux backend (own socket), with the config
// dir taken from CLAUDE_CONFIG_DIR.
func TestReadTranscriptsIntegration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	cfg := filepath.Join(t.TempDir(), "claude")
	guardConfigDir(t, cfg)
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	b := newTestBackend(t)
	ctx := context.Background()
	if _, ok := session.Backend(b).(session.TranscriptReader); !ok {
		t.Fatal("Backend is not a TranscriptReader")
	}

	proj := filepath.Join(cfg, "projects", "-home-u-proj")
	if err := os.MkdirAll(filepath.Join(proj, "s1", "subagents"), 0o700); err != nil {
		t.Fatal(err)
	}
	ts := "2026-11-14T19:37:01.614Z"
	main := filepath.Join(proj, "s1.jsonl")
	agent := filepath.Join(proj, "s1", "subagents", "agent-x.jsonl")
	a := asstLine("msg_1", "claude-opus-5-5", ts, "/home/u/proj", 2, 264, 14394, 24768)
	if err := os.WriteFile(main, []byte(a+a+`{"type":"summary"}`+"\n"+asstLine("msg_syn", "<synthetic>", ts, "/x", 1, 1, 1, 1)+
		asstLine("msg_2", "claude-opus-5-5", ts, "/home/u/proj", 3, 4, 5, 6)+strings.TrimSuffix(a, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agent, []byte(asstLine("msg_3", "claude-haiku-5", ts, "/home/u/proj", 10, 20, 30, 40)), 0o600); err != nil {
		t.Fatal(err)
	}

	// Mimic the store: count each message id once, remember the offsets.
	known := map[string]int64{}
	seen := map[string]bool{}
	var in, out, cc, cr int64
	scan := func() {
		cs, err := b.ReadTranscripts(ctx, known)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cs {
			known[c.Path] = c.End
			for _, r := range c.Records {
				if seen[r.MsgID] {
					continue
				}
				seen[r.MsgID] = true
				in, out, cc, cr = in+r.Input, out+r.Output, cc+r.CacheCreate, cr+r.CacheRead
			}
		}
	}
	scan()
	// msg_1 (once), msg_2, msg_3; the trailing partial line is not read yet.
	if in != 2+3+10 || out != 264+4+20 || cc != 14394+5+30 || cr != 24768+6+40 || len(seen) != 3 {
		t.Fatalf("first scan: in=%d out=%d cc=%d cr=%d seen=%v", in, out, cc, cr, seen)
	}
	// Finish the partial line (it repeats msg_1) and add a new message.
	f, err := os.OpenFile(main, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("\n" + asstLine("msg_4", "claude-opus-5-5", ts, "/home/u/proj", 100, 200, 300, 400))
	f.Close()
	scan()
	// The completed partial line repeats msg_1 and is deduplicated; msg_4 is new.
	if in != 115 || out != 488 || len(seen) != 4 {
		t.Fatalf("second scan: in=%d out=%d seen=%v", in, out, seen)
	}
	scan()
	if in != 115 || len(seen) != 4 {
		t.Fatalf("idle scan changed totals: in=%d", in)
	}
}
