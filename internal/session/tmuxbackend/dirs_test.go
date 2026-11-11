package tmuxbackend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/jgulick48/claude-harness/internal/session"
)

func mkdirs(t *testing.T, home string, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func paths(es []session.DirEntry) []string {
	out := []string{}
	for _, e := range es {
		out = append(out, e.Path)
	}
	return out
}

// exerciseCompletion runs the shared CompleteDir/FindRepos checks against b,
// whose home directory is home (an empty temp dir).
func exerciseCompletion(t *testing.T, b *Backend, home string) {
	ctx := context.Background()
	mkdirs(t, home,
		"src/app/.git", "src/api", "src/apple", "src/.hidden", "src/.hooks", "src/node_modules",
		"src/sp ace", "src/q'uote", "src/d$ollar", `src/dq"x`, "src/semi;colon", "src/glob*star",
		"src/-dash", "top/.git", "a/b/c/.git", "p/q/r/s/.git",
		"node_modules/x/.git", ".hid/repo/.git", "many")
	os.WriteFile(filepath.Join(home, "src", "afile"), nil, 0o644)
	os.WriteFile(filepath.Join(home, "wt"), nil, 0o644)
	mkdirs(t, home, "wtrepo")
	os.WriteFile(filepath.Join(home, "wtrepo", ".git"), []byte("gitdir: x"), 0o644) // worktree-style
	for i := 0; i < 120; i++ {
		mkdirs(t, home, fmt.Sprintf("many/d%03d", i))
	}

	complete := func(prefix string) []session.DirEntry {
		t.Helper()
		es, err := b.CompleteDir(ctx, prefix)
		if err != nil {
			t.Fatalf("CompleteDir(%q): %v", prefix, err)
		}
		if es == nil {
			t.Fatalf("CompleteDir(%q) returned nil, want empty slice", prefix)
		}
		return es
	}

	es := complete("~/src/")
	want := []string{"~/src/-dash/", "~/src/api/", "~/src/app/", "~/src/apple/", `~/src/d$ollar/`, `~/src/dq"x/`,
		"~/src/glob*star/", "~/src/node_modules/", "~/src/q'uote/", "~/src/semi;colon/", "~/src/sp ace/"}
	if got := paths(es); !reflect.DeepEqual(got, want) {
		t.Errorf("~/src/ =\n%q\nwant\n%q", got, want)
	}
	for _, e := range es {
		if e.GitRepo != (e.Path == "~/src/app/") {
			t.Errorf("gitRepo wrong for %s: %v", e.Path, e.GitRepo)
		}
	}
	if got := paths(complete("~/src/ap")); !reflect.DeepEqual(got, []string{"~/src/api/", "~/src/app/", "~/src/apple/"}) {
		t.Errorf("name prefix: %q", got)
	}
	if got := paths(complete("~/src/.")); !reflect.DeepEqual(got, []string{"~/src/.hidden/", "~/src/.hooks/"}) {
		t.Errorf("hidden: %q", got)
	}
	if got := paths(complete("~/src/.ho")); !reflect.DeepEqual(got, []string{"~/src/.hooks/"}) {
		t.Errorf("hidden prefix: %q", got)
	}
	for prefix, want := range map[string]string{
		"~/src/sp ace/": "", "~/src/sp": "~/src/sp ace/", `~/src/d$`: `~/src/d$ollar/`, "~/src/q'": "~/src/q'uote/",
		`~/src/dq"`: `~/src/dq"x/`, "~/src/semi;": "~/src/semi;colon/", "~/src/glob*": "~/src/glob*star/",
		"~/src/*": "", "~/src/a*": "", "~/src/-": "~/src/-dash/",
	} {
		got := paths(complete(prefix))
		switch {
		case want == "" && len(got) != 0:
			t.Errorf("%q = %q, want none", prefix, got)
		case want != "" && !reflect.DeepEqual(got, []string{want}):
			t.Errorf("%q = %q, want [%q]", prefix, got, want)
		}
	}
	if got := paths(complete("~/missing/")); len(got) != 0 {
		t.Errorf("missing dir: %q", got)
	}
	if got := paths(complete("~/src/afile/")); len(got) != 0 {
		t.Errorf("file as dir: %q", got)
	}
	// "", "~" and "~/" list home; hidden entries are left out.
	homeWant := []string{"~/a/", "~/many/", "~/node_modules/", "~/p/", "~/src/", "~/top/", "~/wtrepo/"}
	for _, p := range []string{"", "~", "~/"} {
		if got := paths(complete(p)); !reflect.DeepEqual(got, homeWant) {
			t.Errorf("%q = %q", p, got)
		}
	}
	if es := complete("~/to"); len(es) != 1 || !es[0].GitRepo || es[0].Path != "~/top/" {
		t.Errorf("~/to = %+v", es)
	}
	if es := complete("~/wt"); len(es) != 1 || es[0].Path != "~/wtrepo/" || !es[0].GitRepo {
		t.Errorf("~/wt = %+v (file .git must count, plain file wt must not be listed)", es)
	}
	// Absolute prefixes keep the absolute form.
	if got := paths(complete(home + "/src/ap")); !reflect.DeepEqual(got, []string{home + "/src/api/", home + "/src/app/", home + "/src/apple/"}) {
		t.Errorf("absolute: %q", got)
	}
	// Cap and order.
	many := complete("~/many/")
	if len(many) != 100 || many[0].Path != "~/many/d000/" || many[99].Path != "~/many/d099/" {
		t.Errorf("cap: len=%d first=%v", len(many), many[0])
	}
	// Bad prefixes.
	for _, p := range []string{"src/", "foo", "./x", "~user/x"} {
		if _, err := b.CompleteDir(ctx, p); !errors.Is(err, session.ErrBadRequest) {
			t.Errorf("CompleteDir(%q) err = %v, want ErrBadRequest", p, err)
		}
	}

	repos, err := b.FindRepos(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantRepos := []string{"~/a/b/c/", "~/src/app/", "~/top/", "~/wtrepo/"}
	if !reflect.DeepEqual(repos, wantRepos) {
		t.Errorf("FindRepos = %q, want %q", repos, wantRepos)
	}
}

func TestLocalCompletion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX paths")
	}
	home := t.TempDir()
	exerciseCompletion(t, NewLocal(Options{Home: home}), home)
}

func TestLocalFindReposCap(t *testing.T) {
	home := t.TempDir()
	for i := 0; i < 70; i++ {
		mkdirs(t, home, fmt.Sprintf("r%02d/.git", i))
	}
	repos, err := NewLocal(Options{Home: home}).FindRepos(context.Background())
	if err != nil || len(repos) != 50 {
		t.Fatalf("len=%d err=%v", len(repos), err)
	}
}

func TestFindReposCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	home := t.TempDir()
	mkdirs(t, home, "a/.git")
	if _, err := NewLocal(Options{Home: home}).FindRepos(ctx); err == nil {
		t.Error("expected error for cancelled context")
	}
}

func TestCompleteUnavailable(t *testing.T) {
	b := NewLocal(Options{Home: t.TempDir()})
	b.noTmux = "tmux not found"
	if _, err := b.CompleteDir(context.Background(), "~/"); !errors.Is(err, session.ErrUnavailable) {
		t.Errorf("CompleteDir: %v", err)
	}
	if _, err := b.FindRepos(context.Background()); !errors.Is(err, session.ErrUnavailable) {
		t.Errorf("FindRepos: %v", err)
	}
}

func TestSSHCompletion(t *testing.T) {
	r := newSSHRig(t)
	b := r.backend(r.dial(), Options{})
	exerciseCompletion(t, b, r.home)
}

func TestSSHCompletionConnectionLost(t *testing.T) {
	r := newSSHRig(t)
	c := r.dial()
	b := r.backend(c, Options{})
	c.Close()
	waitFor(t, func() bool { return b.isLost() })
	if _, err := b.CompleteDir(context.Background(), "~/"); !errors.Is(err, session.ErrUnavailable) {
		t.Errorf("CompleteDir: %v", err)
	}
	if _, err := b.FindRepos(context.Background()); !errors.Is(err, session.ErrUnavailable) {
		t.Errorf("FindRepos: %v", err)
	}
}
