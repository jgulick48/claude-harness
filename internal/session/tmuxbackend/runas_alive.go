package tmuxbackend

import (
	"path"
	"strconv"
	"strings"
)

// proc is one row of the host's process table.
type proc struct {
	pid, ppid int
	comm      string
	args      string
}

// parsePS parses `ps -o pid= -o ppid= -o comm= -o args=` output, skipping
// lines that don't have the three leading fields.
func parsePS(s string) []proc {
	var res []proc
	for _, line := range strings.Split(s, "\n") {
		rest := strings.TrimSpace(line)
		var f [3]string
		okLine := true
		for i := range f {
			j := strings.IndexAny(rest, " \t")
			if j < 0 {
				if i < 2 || rest == "" {
					okLine = false
					break
				}
				f[i], rest = rest, ""
				continue
			}
			f[i], rest = rest[:j], strings.TrimLeft(rest[j:], " \t")
		}
		if !okLine {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil {
			continue
		}
		res = append(res, proc{pid: pid, ppid: ppid, comm: f[2], args: rest})
	}
	return res
}

// isClaudeProc reports whether p is a Claude Code process: Linux shows the
// process title ("claude") as comm; the native macOS binary shows its path
// under .../claude/versions/; a launcher may only show up in argv[0].
func isClaudeProc(p proc) bool {
	if path.Base(p.comm) == "claude" || strings.Contains(p.comm, "/claude/versions/") {
		return true
	}
	first, _, _ := strings.Cut(p.args, " ")
	return path.Base(first) == "claude"
}

// claudeAlive reports, for each root pid, whether the root or any of its
// descendants is a Claude process. ok is false when the table can't be trusted
// (empty, or pid 1 missing because procfs hidepid hides other users'
// processes), in which case nothing should be overridden.
func claudeAlive(procs []proc, roots []int) (alive map[int]bool, ok bool) {
	hasInit := false
	children := map[int][]int{}
	byPid := map[int]proc{}
	for _, p := range procs {
		if p.pid == 1 {
			hasInit = true
		}
		byPid[p.pid] = p
		children[p.ppid] = append(children[p.ppid], p.pid)
	}
	if len(procs) == 0 || !hasInit {
		return nil, false
	}
	alive = map[int]bool{}
	for _, r := range roots {
		seen := map[int]bool{r: true}
		queue := []int{r}
		for len(queue) > 0 && !alive[r] {
			pid := queue[0]
			queue = queue[1:]
			if p, ok := byPid[pid]; ok && isClaudeProc(p) {
				alive[r] = true
				break
			}
			for _, c := range children[pid] {
				if !seen[c] {
					seen[c] = true
					queue = append(queue, c)
				}
			}
		}
	}
	return alive, true
}
