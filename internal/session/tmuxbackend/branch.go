package tmuxbackend

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jgulick48/claude-harness/internal/session"
	"github.com/jgulick48/claude-harness/internal/tmux"
)

// Git branch of each session's directory (docs/CORE_PROTOCOL.md §5.1). List
// asks the host for the branch of every distinct session directory, so a new
// directory shows its branch at once and known ones are refreshed every
// branchEvery.

// branchEvery is the minimum time between two refreshes of known directories.
const branchEvery = 30 * time.Second

// branchMaxDirs caps the directories looked up by one command.
const branchMaxDirs = 64

// branchNow is the clock for throttling; tests replace it.
var branchNow = time.Now

// branchCache is the Backend's branch state; mu guards all of it.
type branchCache struct {
	mu          sync.Mutex
	refreshedAt time.Time         // last full refresh (or try)
	branches    map[string]string // dir -> branch ("" = none or unknown)
}

// branchScript returns POSIX sh printing "<index>\t<branch>" for each dir.
// Indexes, not paths, so odd characters in a path can't confuse parsing.
func branchScript(dirs []string) string {
	var sb strings.Builder
	sb.WriteString("command -v git >/dev/null 2>&1 || exit 0\nGIT_OPTIONAL_LOCKS=0\nexport GIT_OPTIONAL_LOCKS\n")
	for i, d := range dirs {
		sb.WriteString("printf '%s\\t%s\\n' " + strconv.Itoa(i) + ` "$(git -C ` + tmux.ShellQuote(d) + " symbolic-ref --short -q HEAD 2>/dev/null)\"\n")
	}
	sb.WriteString("exit 0\n")
	return sb.String()
}

// parseBranches parses branchScript's output into index -> branch, ignoring
// malformed lines and indexes outside [0, n).
func parseBranches(out string, n int) map[int]string {
	got := map[int]string{}
	for _, line := range strings.Split(out, "\n") {
		idx, br, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		i, err := strconv.Atoi(idx)
		if err != nil || i < 0 || i >= n {
			continue
		}
		got[i] = strings.TrimSpace(br)
	}
	return got
}

// applyBranches sets GitBranch on the sessions (not run-as ones: running git
// as us in another user's repo would trip git's safe.directory check, which
// we must not override).
func (b *Backend) applyBranches(ctx context.Context, list []session.Session) {
	sh, ok := b.ops.(shellOps)
	if !ok {
		return
	}
	set := map[string]bool{}
	for _, s := range list {
		if s.RunAs == "" && strings.HasPrefix(s.Cwd, "/") {
			set[s.Cwd] = true
		}
	}
	b.branch.mu.Lock()
	defer b.branch.mu.Unlock()
	c := &b.branch
	if c.branches == nil {
		c.branches = map[string]string{}
	}
	dirs := make([]string, 0, len(set))
	for d := range set {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	var uncached []string
	for _, d := range dirs {
		if _, ok := c.branches[d]; !ok {
			uncached = append(uncached, d)
		}
	}
	now := branchNow()
	var look []string
	switch {
	case len(uncached) > 0:
		look = uncached
	case len(dirs) > 0 && (c.refreshedAt.IsZero() || now.Sub(c.refreshedAt) >= branchEvery || now.Before(c.refreshedAt)):
		look = dirs
	}
	if len(look) == len(dirs) {
		c.refreshedAt = now // every dir is looked up now; failures are throttled too
	}
	if len(look) > branchMaxDirs {
		look = look[:branchMaxDirs]
	}
	if len(look) > 0 {
		out, err := sh.shell(ctx, branchScript(look))
		if err != nil {
			b.log.Debug("branch: lookup failed", "err", err)
			for _, d := range look {
				if _, ok := c.branches[d]; !ok {
					c.branches[d] = ""
				}
			}
		} else {
			got := parseBranches(string(out), len(look))
			for i, d := range look {
				c.branches[d] = got[i]
			}
		}
		for d := range c.branches {
			if !set[d] {
				delete(c.branches, d)
			}
		}
	}
	for i := range list {
		if list[i].RunAs == "" {
			list[i].GitBranch = c.branches[list[i].Cwd]
		}
	}
}
