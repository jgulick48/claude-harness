package tmuxbackend

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jgulick48/claude-harness/internal/session"
)

// fakeBranchOps is a hostOps whose only working method is shell.
type fakeBranchOps struct {
	hostOps
	scripts []string
	reply   func(script string) (string, error)
}

func (f *fakeBranchOps) shell(_ context.Context, script string) ([]byte, error) {
	f.scripts = append(f.scripts, script)
	out, err := f.reply(script)
	return []byte(out), err
}

func TestBranchScriptQuoting(t *testing.T) {
	dirs := []string{"/a b", "/it's", "/x\ny", "/t\tab"}
	s := branchScript(dirs)
	for _, want := range []string{
		"command -v git",
		"GIT_OPTIONAL_LOCKS=0",
		`'/a b'`,
		`'/it'\''s'`,
		"'/x\ny'",
		"'/t\tab'",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q:\n%s", want, s)
		}
	}
	if !strings.Contains(s, "printf '%s\\t%s\\n' 3 ") {
		t.Errorf("no index 3:\n%s", s)
	}
	if sh, err := exec.LookPath("sh"); err == nil {
		if out, err := exec.Command(sh, "-n", "-c", s).CombinedOutput(); err != nil {
			t.Errorf("sh -n: %v %s", err, out)
		}
	}
}

func TestParseBranches(t *testing.T) {
	got := parseBranches("0\tmain\n1\t\n2\tfeat/x\r\n junk\n9\tout\n-1\tneg\nx\ty\n3\t  pad  \n", 4)
	want := map[int]string{0: "main", 1: "", 2: "feat/x", 3: "pad"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
	if len(parseBranches("", 3)) != 0 {
		t.Error("empty output")
	}
}

func branchRig(t *testing.T) (*Backend, *fakeBranchOps, *time.Time) {
	t.Helper()
	now := time.Unix(1_800_000_000, 0)
	old := branchNow
	branchNow = func() time.Time { return now }
	t.Cleanup(func() { branchNow = old })
	f := &fakeBranchOps{}
	f.reply = func(script string) (string, error) {
		// answer "br-<index>" for every dir
		var sb strings.Builder
		for i := 0; i < strings.Count(script, "printf '%s\\t%s\\n' "); i++ {
			sb.WriteString(strconv.Itoa(i) + "\tbr-" + strconv.Itoa(i) + "\n")
		}
		return sb.String(), nil
	}
	b := &Backend{ops: f, log: slog.New(slog.DiscardHandler)}
	return b, f, &now
}

func TestApplyBranchesCache(t *testing.T) {
	b, f, now := branchRig(t)
	ctx := context.Background()
	mk := func(dirs ...string) []session.Session {
		var l []session.Session
		for _, d := range dirs {
			l = append(l, session.Session{Cwd: d})
		}
		return l
	}

	l := mk("/b", "/a", "/a")
	b.applyBranches(ctx, l)
	if len(f.scripts) != 1 || l[0].GitBranch != "br-1" || l[1].GitBranch != "br-0" || l[2].GitBranch != "br-0" {
		t.Fatalf("first: %d scripts, %+v", len(f.scripts), l)
	}

	// Cached: no lookup before 30 s.
	*now = now.Add(29 * time.Second)
	l = mk("/a", "/b")
	b.applyBranches(ctx, l)
	if len(f.scripts) != 1 || l[0].GitBranch != "br-0" {
		t.Fatalf("cached: %d scripts", len(f.scripts))
	}

	// A new dir is looked up at once, and only it.
	l = mk("/a", "/c")
	b.applyBranches(ctx, l)
	if len(f.scripts) != 2 || strings.Contains(f.scripts[1], "'/a'") || !strings.Contains(f.scripts[1], "'/c'") {
		t.Fatalf("new dir: %v", f.scripts)
	}
	if _, ok := b.branch.branches["/b"]; ok {
		t.Error("/b not pruned")
	}

	// Refresh after 30 s: all dirs.
	*now = now.Add(31 * time.Second)
	b.applyBranches(ctx, l)
	if len(f.scripts) != 3 || !strings.Contains(f.scripts[2], "'/a'") || !strings.Contains(f.scripts[2], "'/c'") {
		t.Fatalf("refresh: %v", f.scripts)
	}
	b.applyBranches(ctx, l)
	if len(f.scripts) != 3 {
		t.Error("refresh not throttled")
	}

	// Clock going backwards refreshes.
	*now = now.Add(-time.Hour)
	b.applyBranches(ctx, l)
	if len(f.scripts) != 4 {
		t.Error("no refresh on backwards clock")
	}
}

func TestApplyBranchesRunAsAndRelative(t *testing.T) {
	b, f, _ := branchRig(t)
	l := []session.Session{{Cwd: "/a", RunAs: "bob"}, {Cwd: "rel"}, {Cwd: ""}}
	b.applyBranches(context.Background(), l)
	if len(f.scripts) != 0 {
		t.Fatalf("looked up: %v", f.scripts)
	}
	for _, s := range l {
		if s.GitBranch != "" {
			t.Errorf("%+v", s)
		}
	}
	l = []session.Session{{Cwd: "/a", RunAs: "bob"}, {Cwd: "/a"}}
	b.applyBranches(context.Background(), l)
	if l[0].GitBranch != "" || l[1].GitBranch != "br-0" {
		t.Errorf("%+v", l)
	}
}

func TestApplyBranchesFailureThrottled(t *testing.T) {
	b, f, now := branchRig(t)
	ctx := context.Background()
	good := f.reply
	f.reply = func(string) (string, error) { return "", errors.New("boom") }
	l := []session.Session{{Cwd: "/a"}}
	b.applyBranches(ctx, l)
	b.applyBranches(ctx, l)
	if len(f.scripts) != 1 || l[0].GitBranch != "" {
		t.Fatalf("failure not throttled: %d", len(f.scripts))
	}
	*now = now.Add(31 * time.Second)
	f.reply = good
	b.applyBranches(ctx, l)
	if len(f.scripts) != 2 || l[0].GitBranch != "br-0" {
		t.Fatalf("retry: %d %+v", len(f.scripts), l)
	}
	// A failed refresh leaves known branches alone.
	*now = now.Add(31 * time.Second)
	f.reply = func(string) (string, error) { return "", errors.New("boom") }
	b.applyBranches(ctx, l)
	if len(f.scripts) != 3 || l[0].GitBranch != "br-0" {
		t.Fatalf("kept: %d %+v", len(f.scripts), l)
	}
}

func TestApplyBranchesCap(t *testing.T) {
	b, f, _ := branchRig(t)
	var l []session.Session
	for i := 0; i < 70; i++ {
		l = append(l, session.Session{Cwd: "/d" + string(rune('A'+i%26)) + strconv.Itoa(i/26)})
	}
	b.applyBranches(context.Background(), l)
	if n := strings.Count(f.scripts[0], "git -C"); n != branchMaxDirs {
		t.Errorf("%d dirs in one command", n)
	}
	b.applyBranches(context.Background(), l)
	if len(f.scripts) != 2 {
		t.Errorf("rest not looked up next call: %d", len(f.scripts))
	}
}

func TestListGitBranchIntegration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	b := newTestBackend(t)
	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git init: %v %s", err, out)
	}
	if out, err := exec.Command("git", "-C", repo, "symbolic-ref", "HEAD", "refs/heads/feat/x").CombinedOutput(); err != nil {
		t.Fatalf("symbolic-ref: %v %s", err, out)
	}
	plain := t.TempDir()
	// t.TempDir may sit inside a repo on odd setups; make sure it doesn't.
	if err := os.MkdirAll(filepath.Join(plain, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	s1 := mustCreate(t, b, session.CreateOptions{Dir: repo, Name: "repo"})
	s2 := mustCreate(t, b, session.CreateOptions{Dir: plain, Name: "plain"})
	l, err := b.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range l {
		switch s.ID {
		case s1.ID:
			if s.GitBranch != "feat/x" {
				t.Errorf("repo: %q", s.GitBranch)
			}
		case s2.ID:
			if s.GitBranch != "" {
				t.Errorf("plain: %q", s.GitBranch)
			}
		}
	}
}
