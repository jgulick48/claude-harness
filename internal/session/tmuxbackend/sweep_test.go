package tmuxbackend

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jgulick48/claude-harness/internal/session"
)

// writeAged creates dir/name with an mtime age ago.
func writeAged(t *testing.T, dir, name string, age time.Duration) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("idle 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-age)
	if err := os.Chtimes(p, at, at); err != nil {
		t.Fatal(err)
	}
	return p
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func TestSweepStatusFiles(t *testing.T) {
	b := newTestBackend(t)
	dir := b.statusDir
	if n, err := b.sweepStatusFiles(context.Background(), nil); err != nil || n != 0 {
		t.Fatalf("missing dir: n=%d err=%v", n, err)
	}
	if err := os.Mkdir(dir, 0o1733); err != nil {
		t.Fatal(err)
	}
	old, fresh := 2*time.Hour, time.Minute
	const a, c, d = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "cccccccccccccccccccccccccccccccc", "dddddddddddddddddddddddddddddddd"
	orphan := writeAged(t, dir, a, old)
	orphanTmp := writeAged(t, dir, a+".123", old)
	live := writeAged(t, dir, c, old) // referenced: kept however old
	liveTmp := writeAged(t, dir, c+".9", old)
	young := writeAged(t, dir, d, fresh) // in its grace period
	hook := writeAged(t, dir, "hook-v2.sh", old)
	other := writeAged(t, dir, "notes.txt", old)
	// No lowercase twin among the files above: macOS filesystems ignore case.
	upper := writeAged(t, dir, "FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF", old)
	// A symlink with a status name: its target must survive, and the link
	// itself is skipped (only regular files are swept).
	target := writeAged(t, t.TempDir(), "target", old)
	link := filepath.Join(dir, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	n, err := b.sweepStatusFiles(context.Background(), []string{"", live})
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("removed %d files, want 3", n)
	}
	for _, p := range []string{orphan, orphanTmp, liveTmp} {
		if exists(p) {
			t.Errorf("%s not removed", filepath.Base(p))
		}
	}
	for _, p := range []string{live, young, hook, other, upper, link, target, sub} {
		if !exists(p) {
			t.Errorf("%s removed", filepath.Base(p))
		}
	}
}

func TestSweepSkipsForeignDir(t *testing.T) {
	b := newTestBackend(t)
	// A symlinked status dir is not ours to sweep.
	real := t.TempDir()
	victim := writeAged(t, real, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 2*time.Hour)
	if err := os.Symlink(real, b.statusDir); err != nil {
		t.Fatal(err)
	}
	if n, err := b.sweepStatusFiles(context.Background(), nil); err != nil || n != 0 {
		t.Errorf("n=%d err=%v", n, err)
	}
	if !exists(victim) {
		t.Error("file behind a symlinked dir removed")
	}
}

// List starts a sweep in the background, at most once per interval, and keeps
// the files of live sessions.
func TestListSweepsStatusFiles(t *testing.T) {
	b := newTestBackend(t)
	if err := os.Mkdir(b.statusDir, 0o1733); err != nil {
		t.Fatal(err)
	}
	old := 2 * time.Hour
	s := mustCreate(t, b, session.CreateOptions{Dir: t.TempDir()})
	live := writeAged(t, b.statusDir, "cccccccccccccccccccccccccccccccc", old)
	tm(t, b, "set-option", "-t", s.ID, "@harness_status_file", live)
	orphan := writeAged(t, b.statusDir, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", old)
	// Create may already have listed (and swept): start from a clean slate.
	idle := func() bool {
		b.statusMu.Lock()
		defer b.statusMu.Unlock()
		return !b.sweeping
	}
	waitFor(t, idle)
	b.statusMu.Lock()
	b.lastSweep = time.Time{}
	b.statusMu.Unlock()

	if _, err := b.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !exists(orphan) })
	waitFor(t, idle)
	if !exists(live) {
		t.Error("live session's status file removed")
	}

	// Not due again yet: a new orphan stays.
	orphan2 := writeAged(t, b.statusDir, "dddddddddddddddddddddddddddddddd", old)
	if _, err := b.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if !exists(orphan2) {
		t.Error("swept again before the interval")
	}

	// With the server gone nothing is referenced.
	b.statusMu.Lock()
	b.lastSweep = time.Time{}
	b.statusMu.Unlock()
	b.run.Output(context.Background(), "kill-server") // may report "server exited unexpectedly"
	// While the server shuts down, List fails (and doesn't sweep); then it
	// reports no sessions.
	waitFor(t, func() bool { _, err := b.List(context.Background()); return err == nil })
	waitFor(t, func() bool { return !exists(live) && !exists(orphan2) })
}
