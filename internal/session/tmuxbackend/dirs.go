package tmuxbackend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jgulick48/claude-harness/internal/session"
	"github.com/jgulick48/claude-harness/internal/tmux"
)

const (
	maxDirResults  = 100
	maxRepoResults = 50
	repoTimeout    = 3 * time.Second
	repoMaxDepth   = 3 // repository directory depth below home
)

var _ session.DirCompleter = (*Backend)(nil)

// dirItem is one subdirectory found by hostOps.completeDir.
type dirItem struct {
	name string
	git  bool
}

// CompleteDir implements session.DirCompleter.
func (b *Backend) CompleteDir(ctx context.Context, prefix string) ([]session.DirEntry, error) {
	if err := b.gate(); err != nil {
		return nil, err
	}
	if prefix == "" || prefix == "~" {
		prefix = "~/"
	}
	var dirForm string // the directory part as typed, ending in "/"
	switch {
	case strings.HasPrefix(prefix, "~/"), strings.HasPrefix(prefix, "/"):
		dirForm = prefix[:strings.LastIndex(prefix, "/")+1]
	default:
		return nil, fmt.Errorf("%w: prefix must start with ~/ or /", session.ErrBadRequest)
	}
	name := prefix[len(dirForm):]
	abs := dirForm
	if strings.HasPrefix(dirForm, "~/") {
		abs = path.Join(b.home, dirForm[2:])
	}
	items, err := b.ops.completeDir(ctx, abs, name, maxDirResults)
	if err != nil {
		return nil, b.mapError(err, "")
	}
	out := make([]session.DirEntry, 0, len(items))
	for _, it := range items {
		if it.name == "" || it.name == "." || it.name == ".." || strings.Contains(it.name, "/") {
			continue
		}
		if !strings.HasPrefix(it.name, name) {
			continue
		}
		if strings.HasPrefix(it.name, ".") && !strings.HasPrefix(name, ".") {
			continue
		}
		out = append(out, session.DirEntry{Path: dirForm + it.name + "/", GitRepo: it.git})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	if len(out) > maxDirResults {
		out = out[:maxDirResults]
	}
	return out, nil
}

// FindRepos implements session.DirCompleter.
func (b *Backend) FindRepos(ctx context.Context) ([]string, error) {
	if err := b.gate(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, repoTimeout)
	defer cancel()
	rels, err := b.ops.findRepos(ctx, b.home)
	if err != nil && ctx.Err() == nil {
		return nil, b.mapError(err, "")
	}
	// Timeout (our own deadline) is not an error: return what was found. A
	// cancelled parent context is.
	if err != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, err
	}
	seen := map[string]bool{}
	out := []string{}
	for _, r := range rels {
		r = strings.Trim(r, "/")
		if r == "" || seen["~/"+r] {
			continue
		}
		seen["~/"+r] = true
		out = append(out, "~/"+r+"/")
	}
	sort.Strings(out)
	if len(out) > maxRepoResults {
		out = out[:maxRepoResults]
	}
	return out, nil
}

// ---- local ----

func (o localOps) completeDir(ctx context.Context, dir, name string, limit int) ([]dirItem, error) {
	entries, err := os.ReadDir(filepath.FromSlash(dir))
	if err != nil {
		return nil, nil // missing or unreadable: no completions
	}
	var out []dirItem
	for _, e := range entries {
		n := e.Name()
		if !strings.HasPrefix(n, name) {
			continue
		}
		full := filepath.Join(filepath.FromSlash(dir), n)
		if !e.IsDir() {
			if e.Type()&fs.ModeSymlink == 0 {
				continue
			}
			if fi, err := os.Stat(full); err != nil || !fi.IsDir() {
				continue
			}
		}
		_, gerr := os.Lstat(filepath.Join(full, ".git"))
		out = append(out, dirItem{name: n, git: gerr == nil})
	}
	return out, nil
}

func repoSkipDir(name string) bool {
	return name == "node_modules" || strings.HasPrefix(name, ".")
}

func (o localOps) findRepos(ctx context.Context, home string) ([]string, error) {
	root := filepath.FromSlash(home)
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if p == root {
			return nil
		}
		if repoSkipDir(d.Name()) {
			return fs.SkipDir
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return fs.SkipDir
		}
		depth := strings.Count(rel, string(filepath.Separator)) + 1
		if _, gerr := os.Lstat(filepath.Join(p, ".git")); gerr == nil {
			out = append(out, filepath.ToSlash(rel))
			if len(out) >= maxRepoResults {
				return fs.SkipAll
			}
		}
		if depth >= repoMaxDepth {
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	return out, nil
}

// ---- ssh ----

// completeScript lists subdirectories of "$1" whose names start with "$2",
// as NUL-terminated name and flag ("1" when it holds .git, else "0"). The
// quoted prefix followed by an unquoted glob keeps special characters in names
// literal. LC_ALL=C makes the glob order bytewise; "." and ".." (matched by
// a ".*" prefix) are skipped; output is capped at $3 entries.
const completeScript = `LC_ALL=C; export LC_ALL
cd -- "$1" 2>/dev/null || exit 0
n=0
for d in "$2"*/; do
  [ -d "$d" ] || continue
  d=${d%/}
  case $d in .|..) continue;; esac
  g=0; [ -e "$d/.git" ] && g=1
  printf '%s\0%s\0' "$d" "$g"
  n=$((n+1)); [ "$n" -ge "$3" ] && break
done
exit 0`

// completeCmd builds the one-shot remote command: the script above with its
// three arguments passed through "sh -c", so every value is shell-quoted once.
func completeCmd(dir, name string, limit int) string {
	return "sh -c " + tmux.ShellQuote(completeScript) + " sh " +
		tmux.ShellQuote(dir) + " " + tmux.ShellQuote(name) + " " + fmt.Sprint(limit)
}

func (o sshOps) completeDir(ctx context.Context, dir, name string, limit int) ([]dirItem, error) {
	out, err := o.r.Exec(ctx, completeCmd(dir, name, limit))
	if err != nil {
		return nil, err
	}
	parts := bytes.Split(out, []byte{0})
	var items []dirItem
	for i := 0; i+1 < len(parts); i += 2 {
		items = append(items, dirItem{name: string(parts[i]), git: string(parts[i+1]) == "1"})
	}
	return items, nil
}

// findScript finds .git entries at depth 2..4 under $HOME (repositories at
// depth 1..3), pruning node_modules and dot-directories, and ends at the first
// 16 KiB of output so a huge tree can't flood the connection. Paths are
// relative to $HOME and NUL-terminated.
const findScript = `cd "$HOME" 2>/dev/null || exit 0
find . -mindepth 1 -maxdepth 4 \( -name node_modules -o \( -name '.?*' ! -name .git \) \) -prune -o -name .git -print0 -prune 2>/dev/null | head -c 16384
exit 0`

func (o sshOps) findRepos(ctx context.Context, home string) ([]string, error) {
	out, err := o.r.Exec(ctx, "sh -c "+tmux.ShellQuote(findScript))
	var repos []string
	for _, p := range bytes.Split(out, []byte{0}) {
		s := string(p)
		// A path cut off by head -c has no .git suffix and is dropped here.
		s = strings.TrimPrefix(s, "./")
		dir, ok := strings.CutSuffix(s, "/.git")
		if !ok || dir == "" {
			continue
		}
		if strings.Count(dir, "/")+1 > repoMaxDepth {
			continue
		}
		repos = append(repos, dir)
	}
	return repos, err
}
