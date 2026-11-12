package tmuxbackend

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jgulick48/claude-harness/internal/claudehooks"
	"github.com/jgulick48/claude-harness/internal/tmux"
)

// Run-as sessions can't reach the login user's tmux server, so their Claude
// reports status through a file in a sticky, world-writable-by-create
// directory owned by the login user (mode 1733: others may create files but
// not list or delete the directory's other entries).

var statusFileNameRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// validStatusFile reports whether p is exactly <dir>/<32 hex chars>.
func validStatusFile(dir, p string) bool {
	if dir == "" || !strings.HasPrefix(p, dir+"/") {
		return false
	}
	return statusFileNameRe.MatchString(p[len(dir)+1:])
}

func newStatusToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// parseStatusFile parses "<state> <epoch>"; ok is false for an empty or
// malformed file.
func parseStatusFile(s string) (state string, at int64, ok bool) {
	f := strings.Fields(s)
	if len(f) != 2 {
		return "", 0, false
	}
	n, err := strconv.ParseInt(f[1], 10, 64)
	if err != nil || n <= 0 {
		return "", 0, false
	}
	return f[0], n, true
}

func statusHookPath(dir string) string {
	return fmt.Sprintf("%s/hook-v%d.sh", dir, claudehooks.Version)
}
func statusSettingsPath(dir string) string {
	return fmt.Sprintf("%s/settings-v%d.json", dir, claudehooks.Version)
}

// ensureScript is the one shell snippet that prepares the status directory
// dirExpr (a shell expression) and installs the hook script and settings.
func ensureScript(dirExpr string, hook, settings []byte, hookName, settingsName string) string {
	q := tmux.ShellQuote
	return `d=` + dirExpr + `
[ -d "$d" ] || mkdir -m 1733 "$d" || { echo "cannot create $d" >&2; exit 1; }
{ [ -d "$d" ] && [ ! -L "$d" ] && [ -O "$d" ]; } || { echo "$d is not a directory owned by us" >&2; exit 1; }
chmod 1733 "$d" || exit 1
# mktemp creates the file exclusively: nothing planted in this shared dir can redirect the write.
t=$(mktemp "$d/.tmp.XXXXXX") || exit 1
trap 'rm -f "$t"' EXIT
printf %s ` + q(string(hook)) + ` >"$t" && chmod 0755 "$t" && mv -f "$t" "$d/"` + q(hookName) + ` || exit 1
printf %s ` + q(string(settings)) + ` >"$t" && chmod 0644 "$t" && mv -f "$t" "$d/"` + q(settingsName) + ` || exit 1
printf %s "$d"
`
}

// resolveStatusDir returns the status directory path (not creating it).
// Caller must not hold statusMu.
func (b *Backend) resolveStatusDir(ctx context.Context) (string, error) {
	if b.statusDir != "" {
		return b.statusDir, nil
	}
	b.statusMu.Lock()
	defer b.statusMu.Unlock()
	return b.resolveLocked(ctx)
}

func (b *Backend) resolveLocked(ctx context.Context) (string, error) {
	if b.statusBase != "" {
		return b.statusBase, nil
	}
	ops, ok := b.ops.(shellOps)
	if !ok {
		return "", fmt.Errorf("host %s cannot run shell snippets", b.id)
	}
	out, err := ops.shell(ctx, `id -u`)
	if err != nil {
		return "", err
	}
	uid := strings.TrimSpace(string(out))
	if _, err := strconv.Atoi(uid); err != nil {
		return "", fmt.Errorf("unexpected uid %q", uid)
	}
	b.statusBase = "/tmp/claude-harness-status-" + uid
	return b.statusBase, nil
}

// statusSetup returns the status directory with the hook script and settings
// installed, running the ensure step lazily. It is cached; a cached result is
// re-verified (cheaply) and redone if the files went missing.
func (b *Backend) statusSetup(ctx context.Context) (dir string, err error) {
	ops, ok := b.ops.(shellOps)
	if !ok {
		return "", fmt.Errorf("host %s cannot run shell snippets", b.id)
	}
	b.statusMu.Lock()
	defer b.statusMu.Unlock()
	if b.statusReady != "" {
		d := b.statusReady
		// Other users can create files in the shared dir: the files must be ours.
		ours := func(p string) string {
			q := tmux.ShellQuote(p)
			return "[ -f " + q + " ] && [ ! -L " + q + " ] && [ -O " + q + " ]"
		}
		chk := ours(statusHookPath(d)) + " && " + ours(statusSettingsPath(d))
		if _, err := ops.shell(ctx, chk); err == nil {
			return d, nil
		}
		b.statusReady = ""
	}
	hookDir := b.statusDir
	if hookDir == "" {
		if hookDir, err = b.resolveLocked(ctx); err != nil {
			return "", err
		}
	}
	dirExpr := tmux.ShellQuote(hookDir)
	settings, err := claudehooks.Merge([]byte("{}"), statusHookPath(hookDir))
	if err != nil {
		return "", err
	}
	script := ensureScript(dirExpr, claudehooks.Script(), settings,
		fmt.Sprintf("hook-v%d.sh", claudehooks.Version), fmt.Sprintf("settings-v%d.json", claudehooks.Version))
	out, err := ops.shell(ctx, script)
	if err != nil {
		return "", err
	}
	if got := strings.TrimSpace(string(out)); got != hookDir {
		return "", fmt.Errorf("unexpected status dir %q", got)
	}
	b.statusReady = hookDir
	return hookDir, nil
}

// readStatusFiles reads every path in one shell exec and returns path ->
// contents. Only valid status paths are read.
func (b *Backend) readStatusFiles(ctx context.Context, dir string, paths []string) (map[string]string, error) {
	ops, ok := b.ops.(shellOps)
	if !ok {
		return nil, fmt.Errorf("host %s cannot run shell snippets", b.id)
	}
	var quoted []string
	for _, p := range paths {
		if validStatusFile(dir, p) {
			quoted = append(quoted, tmux.ShellQuote(p))
		}
	}
	if len(quoted) == 0 {
		return nil, nil
	}
	script := `for f in ` + strings.Join(quoted, " ") + `; do printf '%s\t' "$f"; ` +
		// Regular files only, at most 64 bytes: the target user owns the file and
		// could swap in a FIFO or a link to /dev/zero, which would hang the poll.
		`[ -f "$f" ] && [ ! -L "$f" ] && head -c 64 "$f" 2>/dev/null | head -n 1 | tr -d '\r\t\n'; echo; done`
	out, err := ops.shell(ctx, script)
	if err != nil {
		return nil, err
	}
	res := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		p, content, ok := strings.Cut(line, "\t")
		if ok && validStatusFile(dir, p) {
			res[p] = strings.TrimSpace(content)
		}
	}
	return res, nil
}

// removeStatusFile deletes a session's status file, best effort.
func (b *Backend) removeStatusFile(ctx context.Context, p string) {
	if p == "" {
		return
	}
	dir, err := b.resolveStatusDir(ctx)
	if err != nil || !validStatusFile(dir, p) {
		return
	}
	if ops, ok := b.ops.(shellOps); ok {
		if _, err := ops.shell(ctx, "rm -f "+tmux.ShellQuote(p)); err != nil {
			b.log.Debug("removing status file failed", "err", err)
		}
	}
}
