package tmuxbackend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
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
