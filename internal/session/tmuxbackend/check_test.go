package tmuxbackend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jgulick48/claude-harness/internal/session"
)

func TestParseTmuxVersion(t *testing.T) {
	for _, c := range []struct {
		in       string
		ver      string
		maj, min int
		ok       bool
	}{
		{"tmux 3.2a\n", "3.2a", 3, 2, true},
		{"tmux 3.4", "3.4", 3, 4, true},
		{"tmux next-3.5", "next-3.5", 3, 5, true},
		{"tmux 3.3-rc", "3.3-rc", 3, 3, true},
		{"tmux 2.9a", "2.9a", 2, 9, true},
		{"tmux 10.0", "10.0", 10, 0, true},
		{"tmux master", "master", 0, 0, false},
		{"", "", 0, 0, false},
		{"tmux 3", "3", 0, 0, false},
	} {
		ver, maj, min, ok := parseTmuxVersion(c.in)
		if ver != c.ver || maj != c.maj || min != c.min || ok != c.ok {
			t.Errorf("parseTmuxVersion(%q) = %q %d %d %v", c.in, ver, maj, min, ok)
		}
	}
}

func TestInstallHint(t *testing.T) {
	rel := func(id, like string) string {
		s := "NAME=\"x\"\nID=" + id + "\n"
		if like != "" {
			s += "ID_LIKE=\"" + like + "\"\n"
		}
		return s
	}
	for _, c := range []struct{ uname, rel, cmd string }{
		{"Darwin\n", "", "brew install tmux"},
		{"Linux", rel("ubuntu", "debian"), "sudo apt install tmux"},
		{"Linux", rel("debian", ""), "sudo apt install tmux"},
		{"Linux", rel("linuxmint", "ubuntu debian"), "sudo apt install tmux"},
		{"Linux", rel("fedora", ""), "sudo dnf install tmux"},
		{"Linux", rel("rocky", "rhel centos fedora"), "sudo dnf install tmux"},
		{"Linux", rel("arch", ""), "sudo pacman -S tmux"},
		{"Linux", rel("endeavouros", "arch"), "sudo pacman -S tmux"},
		{"Linux", rel("alpine", ""), "sudo apk add tmux"},
		{"Linux", rel("opensuse-leap", "suse opensuse"), "sudo zypper install tmux"},
		{"Linux", rel("weirdos", ""), ""},
		{"Linux", "", ""},
		{"FreeBSD", "", ""},
		{"", "", ""},
	} {
		fix, cmd := installHint(c.uname, c.rel)
		if cmd != c.cmd || fix == "" {
			t.Errorf("installHint(%q, %q) = %q, %q; want cmd %q", c.uname, c.rel, fix, cmd, c.cmd)
		}
	}
}

func TestOSReleaseField(t *testing.T) {
	text := "NAME=\"Ubuntu\"\nID=ubuntu\nID_LIKE='debian'\nVERSION_ID=\"22.04\"\n"
	if got := osReleaseField(text, "ID"); got != "ubuntu" {
		t.Errorf("ID = %q", got)
	}
	if got := osReleaseField(text, "ID_LIKE"); got != "debian" {
		t.Errorf("ID_LIKE = %q", got)
	}
	if got := osReleaseField(text, "NOPE"); got != "" {
		t.Errorf("NOPE = %q", got)
	}
}

func TestLastSlashLine(t *testing.T) {
	if got := lastSlashLine([]byte("welcome\n/usr/bin/claude\n")); got != "/usr/bin/claude" {
		t.Errorf("%q", got)
	}
	if got := lastSlashLine([]byte("/a/b\nnoise\n/c/d  \nbye")); got != "/c/d" {
		t.Errorf("%q", got)
	}
	if got := lastSlashLine([]byte("alias claude='x'\n")); got != "" {
		t.Errorf("%q", got)
	}
}

// ---- integration rigs ----

type checkRig struct {
	b    *Backend
	home string // host home
	cfg  string // CLAUDE_CONFIG_DIR on the host
}

var checkItemOrder = []string{"tmux", "shell", "home", "claude", "config", "hooks"}

// isolatedPath makes a directory holding only the named tools (symlinks), for
// use as a PATH that can't see any other binary (in particular, a real claude).
func isolatedPath(t *testing.T, tools ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, tool := range tools {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("%s not found: %v", tool, err)
		}
		if err := os.Symlink(p, filepath.Join(dir, tool)); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func overrideProbes(t *testing.T, tmuxProbes, claudeProbes []string) {
	t.Helper()
	ot, oc := tmuxProbePaths, claudeProbePaths
	tmuxProbePaths, claudeProbePaths = tmuxProbes, claudeProbes
	t.Cleanup(func() { tmuxProbePaths, claudeProbePaths = ot, oc })
}

// newLocalCheckRig builds a local backend with its own HOME/CLAUDE_CONFIG_DIR
// and the given PATH/SHELL. No tmux server is started (Check only runs -V).
func newLocalCheckRig(t *testing.T, path, shell string) *checkRig {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	home, cfg := t.TempDir(), filepath.Join(t.TempDir(), "claudecfg")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	t.Setenv("PATH", path)
	t.Setenv("SHELL", shell)
	b := NewLocal(Options{Socket: "harness-check-unused", Config: "/dev/null", Home: home})
	t.Cleanup(func() { b.Close() })
	return &checkRig{b: b, home: home, cfg: cfg}
}

// newSSHCheckRig builds an SSH backend over the in-process rig. path and shell
// are the host command environment.
func newSSHCheckRig(t *testing.T, path, shell string) *checkRig {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "claudecfg")
	r := newSSHRig(t, "PATH="+path, "SHELL="+shell, "CLAUDE_CONFIG_DIR="+cfg)
	return &checkRig{b: r.backend(r.dial(), Options{}), home: r.home, cfg: cfg}
}

type rigMaker func(t *testing.T, path, shell string) *checkRig

var rigMakers = map[string]rigMaker{"local": newLocalCheckRig, "ssh": newSSHCheckRig}

func forBoth(t *testing.T, f func(t *testing.T, mk rigMaker)) {
	for name, mk := range rigMakers {
		t.Run(name, func(t *testing.T) { f(t, mk) })
	}
}

func (r *checkRig) check(t *testing.T) (session.HostReport, map[string]session.CheckItem) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	rep, err := r.b.Check(ctx)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	m := map[string]session.CheckItem{}
	for _, it := range rep.Items {
		m[it.ID] = it
	}
	return rep, m
}

func assertOrder(t *testing.T, rep session.HostReport, want ...string) {
	t.Helper()
	var got []string
	for _, it := range rep.Items {
		got = append(got, it.ID)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("item order %v, want %v", got, want)
	}
}

func wantStatus(t *testing.T, items map[string]session.CheckItem, id, status string) session.CheckItem {
	t.Helper()
	it, ok := items[id]
	if !ok || it.Status != status {
		t.Errorf("%s: %+v, want status %s", id, it, status)
	}
	if status == session.CheckOK && (it.Fix != "" || it.FixCommand != "") {
		t.Errorf("%s ok but has fix: %+v", id, it)
	}
	return it
}

func fileState(t *testing.T, p string) (data []byte, mtime time.Time) {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return data, fi.ModTime()
}

func TestCheckAllOK(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	forBoth(t, func(t *testing.T, mk rigMaker) {
		fake := filepath.Dir(fakeClaude(t))
		r := mk(t, fake+":"+os.Getenv("PATH"), "/bin/sh")
		if err := r.b.InstallHooks(context.Background()); err != nil {
			t.Fatal(err)
		}
		rep, items := r.check(t)
		assertOrder(t, rep, checkItemOrder...)
		for _, id := range checkItemOrder {
			wantStatus(t, items, id, session.CheckOK)
		}
		if rep.Host != r.b.ID() || rep.CheckedAt < time.Now().Unix()-30 {
			t.Errorf("%+v", rep)
		}
		if !strings.Contains(items["tmux"].Detail, " at /") {
			t.Errorf("tmux detail %q", items["tmux"].Detail)
		}
		if items["shell"].Detail != "/bin/sh" || items["home"].Detail != r.home {
			t.Errorf("shell/home: %+v %+v", items["shell"], items["home"])
		}
		if items["claude"].Detail != filepath.Join(fake, "claude") {
			t.Errorf("claude detail %q", items["claude"].Detail)
		}
		if !strings.Contains(items["config"].Detail, r.cfg) {
			t.Errorf("config detail %q, want %q", items["config"].Detail, r.cfg)
		}
		if items["hooks"].Detail != "installed" {
			t.Errorf("hooks: %+v", items["hooks"])
		}
		if out, err := json.MarshalIndent(rep, "", "  "); err == nil && t.Name() == "TestCheckAllOK/local" {
			t.Logf("sample report:\n%s", out)
		}
	})
}

func TestCheckClaudeInjectedProbePath(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	forBoth(t, func(t *testing.T, mk rigMaker) {
		fake := fakeClaude(t)
		tmuxDir := isolatedPath(t, "tmux", "cat", "uname")
		overrideProbes(t, tmuxProbePaths, []string{"/nonexistent/claude", fake})
		r := mk(t, tmuxDir, "/nonexistent/sh")
		_, items := r.check(t)
		if it := wantStatus(t, items, "claude", session.CheckOK); it.Detail != fake {
			t.Errorf("%+v", it)
		}
	})
}

func TestCheckClaudeMissing(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	forBoth(t, func(t *testing.T, mk rigMaker) {
		tmuxDir := isolatedPath(t, "tmux", "cat", "uname")
		overrideProbes(t, tmuxProbePaths, []string{"/nonexistent/claude", "~/.local/bin/claude"})
		r := mk(t, tmuxDir, "/nonexistent/sh")
		rep, items := r.check(t)
		assertOrder(t, rep, checkItemOrder...)
		it := wantStatus(t, items, "claude", session.CheckWarn)
		if it.Fix != "Install Claude Code on this host to run claude sessions here." ||
			it.FixCommand != "curl -fsSL https://claude.ai/install.sh | bash" {
			t.Errorf("%+v", it)
		}
		wantStatus(t, items, "tmux", session.CheckOK)
		wantStatus(t, items, "shell", session.CheckOK) // set, even though bogus
	})
}

func TestCheckClaudeHomeProbe(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	forBoth(t, func(t *testing.T, mk rigMaker) {
		tmuxDir := isolatedPath(t, "tmux", "cat", "uname")
		overrideProbes(t, tmuxProbePaths, []string{"~/.local/bin/claude"})
		fake := fakeClaude(t) // build before PATH is replaced
		r := mk(t, tmuxDir, "/nonexistent/sh")
		if err := os.MkdirAll(filepath.Join(r.home, ".local/bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(fake, filepath.Join(r.home, ".local/bin/claude")); err != nil {
			t.Fatal(err)
		}
		_, items := r.check(t)
		if it := wantStatus(t, items, "claude", session.CheckOK); it.Detail != r.home+"/.local/bin/claude" {
			t.Errorf("%+v", it)
		}
	})
}

func TestCheckInvalidSettings(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	forBoth(t, func(t *testing.T, mk rigMaker) {
		r := mk(t, os.Getenv("PATH"), "/bin/sh")
		if err := os.MkdirAll(r.cfg, 0o755); err != nil {
			t.Fatal(err)
		}
		settings := filepath.Join(r.cfg, "settings.json")
		if err := os.WriteFile(settings, []byte("{not json,"), 0o600); err != nil {
			t.Fatal(err)
		}
		before, mt := fileState(t, settings)
		time.Sleep(20 * time.Millisecond)
		rep, items := r.check(t)
		assertOrder(t, rep, checkItemOrder...)
		it := wantStatus(t, items, "config", session.CheckWarn)
		if !strings.Contains(it.Detail, "not valid JSON") || it.Fix == "" {
			t.Errorf("%+v", it)
		}
		wantStatus(t, items, "hooks", session.CheckWarn) // unreadable settings: can't be installed
		after, mt2 := fileState(t, settings)
		if !bytes.Equal(before, after) || !mt.Equal(mt2) {
			t.Errorf("settings.json changed")
		}
		entries, _ := os.ReadDir(r.cfg)
		if len(entries) != 1 {
			t.Errorf("check wrote into the config dir: %v", entries)
		}
	})
}

func TestCheckConfigMissingIsOK(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	forBoth(t, func(t *testing.T, mk rigMaker) {
		r := mk(t, os.Getenv("PATH"), "/bin/sh")
		_, items := r.check(t)
		wantStatus(t, items, "config", session.CheckOK)
		if _, err := os.Stat(r.cfg); !os.IsNotExist(err) {
			t.Errorf("check created the config dir: %v", err)
		}
	})
}

func TestCheckHooks(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	forBoth(t, func(t *testing.T, mk rigMaker) {
		r := mk(t, os.Getenv("PATH"), "/bin/sh")
		_, items := r.check(t)
		it := wantStatus(t, items, "hooks", session.CheckWarn)
		if it.Fix != "Use Install… in the sidebar" {
			t.Errorf("%+v", it)
		}
		if err := r.b.InstallHooks(context.Background()); err != nil {
			t.Fatal(err)
		}
		_, items = r.check(t)
		wantStatus(t, items, "hooks", session.CheckOK)
		wantStatus(t, items, "config", session.CheckOK)
	})
}

func TestCheckTmuxMissing(t *testing.T) {
	forBoth(t, func(t *testing.T, mk rigMaker) {
		overrideProbes(t, []string{"/nonexistent/tmux"}, []string{"/nonexistent/claude"})
		// uname and cat so the OS hint can be worked out on the host.
		r := mk(t, isolatedPath(t, "cat", "uname"), "/nonexistent/sh")
		rep, items := r.check(t)
		assertOrder(t, rep, checkItemOrder...)
		it := wantStatus(t, items, "tmux", session.CheckError)
		if it.Detail != "tmux not found" || it.Fix == "" {
			t.Errorf("%+v", it)
		}
		switch runtime.GOOS {
		case "darwin":
			if it.FixCommand != "brew install tmux" {
				t.Errorf("fixCommand %q", it.FixCommand)
			}
		case "linux":
			if !strings.HasPrefix(it.FixCommand, "sudo ") || !strings.HasSuffix(it.FixCommand, "tmux") {
				t.Errorf("fixCommand %q", it.FixCommand)
			}
		}
		wantStatus(t, items, "claude", session.CheckWarn)
	})
}

func TestCheckTmuxTooOld(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	r := newLocalCheckRig(t, isolatedPath(t, "cat", "uname"), "/bin/sh")
	dir := t.TempDir()
	old := filepath.Join(dir, "tmux")
	if err := os.WriteFile(old, []byte("#!/bin/sh\necho 'tmux 2.9a'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r.b.run = nil
	b := NewLocal(Options{Bin: old, Home: r.home})
	r.b = b
	_, items := r.check(t)
	it := wantStatus(t, items, "tmux", session.CheckError)
	if !strings.Contains(it.Detail, "2.9a") || it.Fix == "" {
		t.Errorf("%+v", it)
	}
}

func TestCheckShellUnset(t *testing.T) {
	r := newLocalCheckRig(t, os.Getenv("PATH"), "")
	_, items := r.check(t)
	it := wantStatus(t, items, "shell", session.CheckWarn)
	if it.Fix == "" {
		t.Errorf("%+v", it)
	}
}

func TestCheckCanceled(t *testing.T) {
	r := newLocalCheckRig(t, os.Getenv("PATH"), "/bin/sh")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.b.Check(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

func TestSSHCheckLost(t *testing.T) {
	r := newSSHRig(t)
	c := r.dial()
	b := r.backend(c, Options{})
	c.Close()
	waitFor(t, b.isLost)
	if _, err := b.Check(context.Background()); !errors.Is(err, session.ErrUnavailable) {
		t.Errorf("err = %v", err)
	}
}
