package tmuxbackend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/jgulick48/claude-harness/internal/session"
	"github.com/jgulick48/claude-harness/internal/tmux"
)

var _ session.HostChecker = (*Backend)(nil)

// checkTimeout bounds one whole Check.
const checkTimeout = 10 * time.Second

// claudeProbePaths are checked (test -x) when claude isn't on a PATH. A leading
// "~/" means the host's home. A var so tests can override it.
var claudeProbePaths = []string{
	"~/.local/bin/claude",
	"~/.claude/local/claude",
	"/opt/homebrew/bin/claude",
	"/usr/local/bin/claude",
}

const (
	claudeFix        = "Install Claude Code on this host to run claude sessions here."
	claudeFixCommand = "curl -fsSL https://claude.ai/install.sh | bash"
	hooksFix         = "Use Install… in the sidebar"
)

// shellOps runs a shell command line on the host (read-only probes only). A
// non-zero exit is a *tmux.Error; anything else is a transport failure.
type shellOps interface {
	shell(ctx context.Context, cmdline string) ([]byte, error)
}

var _ shellOps = localOps{}
var _ shellOps = sshOps{}

func (localOps) shell(ctx context.Context, cmdline string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", cmdline)
	cmd.Env = tmux.TmuxEnv(os.Environ())
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return stdout.Bytes(), &tmux.Error{Stderr: strings.TrimSpace(stderr.String()), Err: err}
		}
		return stdout.Bytes(), err
	}
	return stdout.Bytes(), nil
}

func (o sshOps) shell(ctx context.Context, cmdline string) ([]byte, error) {
	return o.r.Exec(ctx, cmdline)
}

// lastSlashLine returns the last line of out that starts with "/" (login
// shells may print rc-file noise before the path).
func lastSlashLine(out []byte) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); strings.HasPrefix(l, "/") {
			return l
		}
	}
	return ""
}

// locateBin finds an executable on the host: command -v, then a login shell,
// then the probe paths ("~/" expands to home). "" means not found. Failures of
// individual probes just mean "not here"; only ctx expiry is returned.
func locateBin(ctx context.Context, sh shellOps, name, home string, probes []string) (string, error) {
	q := tmux.ShellQuote(name)
	for _, cmdline := range []string{"command -v " + q, `"$SHELL" -lc ` + tmux.ShellQuote("command -v "+q)} {
		out, err := sh.shell(ctx, cmdline)
		if cerr := ctx.Err(); cerr != nil {
			return "", cerr
		}
		if err != nil {
			continue
		}
		if p := lastSlashLine(out); p != "" {
			return p, nil
		}
	}
	for _, p := range probes {
		if strings.HasPrefix(p, "~/") {
			if home == "" {
				continue
			}
			p = strings.TrimSuffix(home, "/") + p[1:]
		}
		_, err := sh.shell(ctx, "test -x "+tmux.ShellQuote(p))
		if err == nil {
			return p, nil
		}
		if cerr := ctx.Err(); cerr != nil {
			return "", cerr
		}
	}
	return "", nil
}

// parseTmuxVersion extracts the version from `tmux -V` output ("tmux 3.2a",
// "tmux next-3.4"). ok is false when no major.minor is present ("tmux master");
// such builds are assumed new enough.
func parseTmuxVersion(out string) (ver string, major, minor int, ok bool) {
	ver = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out), "tmux"))
	m := versionRe.FindStringSubmatch(ver)
	if m == nil {
		return ver, 0, 0, false
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	return ver, major, minor, true
}

// osReleaseField returns the unquoted value of KEY in /etc/os-release text.
func osReleaseField(text, key string) string {
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if v, ok := strings.CutPrefix(l, key+"="); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

// installHint suggests how to install tmux. uname is `uname -s`; osRelease is
// the text of /etc/os-release (Linux only, may be empty). The command is a
// suggestion for the user; it is never run.
func installHint(uname, osRelease string) (fix, command string) {
	generic := "Install tmux 3.2 or newer on this host."
	switch strings.TrimSpace(uname) {
	case "Darwin":
		return "Install tmux with Homebrew (tmux 3.2 or newer).", "brew install tmux"
	case "Linux":
		ids := strings.Fields(strings.ToLower(osReleaseField(osRelease, "ID") + " " + osReleaseField(osRelease, "ID_LIKE")))
		for _, id := range ids {
			switch id {
			case "debian", "ubuntu", "raspbian", "linuxmint", "pop":
				return generic, "sudo apt install tmux"
			case "fedora", "rhel", "centos", "rocky", "almalinux", "amzn", "ol":
				return generic, "sudo dnf install tmux"
			case "arch", "manjaro", "archarm", "endeavouros":
				return generic, "sudo pacman -S tmux"
			case "alpine":
				return generic, "sudo apk add tmux"
			case "suse", "opensuse", "opensuse-leap", "opensuse-tumbleweed", "sles", "sled":
				return generic, "sudo zypper install tmux"
			}
		}
	}
	return generic + " Use your system's package manager.", ""
}

func (b *Backend) tmuxBin() string {
	switch r := b.run.(type) {
	case *tmux.LocalRunner:
		if r.Bin != "" {
			return r.Bin
		}
	case *tmux.SSHRunner:
		if r.Bin != "" {
			return r.Bin
		}
	}
	return "tmux"
}

// Check implements session.HostChecker: a read-only setup check of the host.
// Individual probe failures become warn/error items; the check itself only
// fails when the host is unavailable (ErrUnavailable) or ctx is done.
func (b *Backend) Check(ctx context.Context) (session.HostReport, error) {
	if b.isLost() {
		return session.HostReport{}, fmt.Errorf("%w: connection lost", session.ErrUnavailable)
	}
	sh, ok := b.ops.(shellOps)
	if !ok {
		return session.HostReport{}, fmt.Errorf("%w: host %s cannot run setup checks", session.ErrUnavailable, b.id)
	}
	outer := ctx
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	rep := session.HostReport{Host: b.id, CheckedAt: time.Now().Unix(), Items: []session.CheckItem{}}
	add := func(it session.CheckItem) { rep.Items = append(rep.Items, it) }

	home, homeErr := b.ops.homeDir(ctx)
	if homeErr == nil && home == "" {
		homeErr = errors.New("home directory is empty")
	}

	add(b.checkTmux(ctx, sh, home))
	add(checkShell(ctx, sh))
	if homeErr != nil {
		add(session.CheckItem{ID: "home", Label: "Home directory", Status: session.CheckError,
			Detail: "cannot determine the home directory: " + homeErr.Error(),
			Fix:    "Make sure the host user has a home directory ($HOME)."})
	} else {
		add(session.CheckItem{ID: "home", Label: "Home directory", Status: session.CheckOK, Detail: home})
	}
	add(checkClaude(ctx, sh, home))
	add(b.checkConfig(ctx))
	add(b.checkHooks(ctx))

	if b.isLost() {
		return session.HostReport{}, fmt.Errorf("%w: connection lost", session.ErrUnavailable)
	}
	if err := outer.Err(); err != nil {
		return session.HostReport{}, err
	}
	return rep, nil
}

func (b *Backend) checkTmux(ctx context.Context, sh shellOps, home string) session.CheckItem {
	it := session.CheckItem{ID: "tmux", Label: "tmux"}
	hint := func() (string, string) {
		out, _ := sh.shell(ctx, "uname -s")
		uname := strings.TrimSpace(string(out))
		var rel string
		if uname == "Linux" {
			if ho, ok := b.ops.(hookOps); ok {
				if data, err := ho.readFile(ctx, "/etc/os-release"); err == nil {
					rel = string(data)
				}
			}
		}
		return installHint(uname, rel)
	}
	fail := func(detail string) session.CheckItem {
		it.Status, it.Detail = session.CheckError, detail
		it.Fix, it.FixCommand = hint()
		return it
	}
	bin := b.tmuxBin()
	path := bin
	if !strings.HasPrefix(bin, "/") {
		p, err := locateBin(ctx, sh, bin, home, tmuxProbePaths)
		if err != nil {
			it.Status, it.Detail = session.CheckError, "tmux lookup failed: "+err.Error()
			return it
		}
		if p == "" {
			return fail("tmux not found")
		}
		path = p
	}
	out, err := sh.shell(ctx, tmux.ShellQuote(path)+" -V")
	if err != nil {
		var te *tmux.Error
		if errors.As(err, &te) && te.Stderr != "" {
			err = errors.New(te.Stderr)
		}
		return fail(fmt.Sprintf("tmux at %s failed to run: %v", path, err))
	}
	ver, maj, min, ok := parseTmuxVersion(string(out))
	if ok && (maj < 3 || maj == 3 && min < 2) {
		return fail(fmt.Sprintf("tmux 3.2 or newer required (found %s at %s)", ver, path))
	}
	it.Status, it.Detail = session.CheckOK, ver+" at "+path
	return it
}

func checkShell(ctx context.Context, sh shellOps) session.CheckItem {
	it := session.CheckItem{ID: "shell", Label: "Login shell"}
	out, err := sh.shell(ctx, `printf %s "$SHELL"`)
	s := strings.TrimSpace(string(out))
	switch {
	case err != nil:
		it.Status, it.Detail = session.CheckWarn, "cannot read $SHELL: "+err.Error()
	case s == "":
		it.Status, it.Detail = session.CheckWarn, "$SHELL is not set"
		it.Fix = "Set a login shell for this user so terminals and the claude lookup work."
	default:
		it.Status, it.Detail = session.CheckOK, s
	}
	return it
}

func checkClaude(ctx context.Context, sh shellOps, home string) session.CheckItem {
	it := session.CheckItem{ID: "claude", Label: "Claude Code"}
	p, err := locateBin(ctx, sh, "claude", home, claudeProbePaths)
	switch {
	case err != nil:
		it.Status, it.Detail = session.CheckWarn, "lookup failed: "+err.Error()
	case p == "":
		it.Status, it.Detail = session.CheckWarn, "not found on PATH"
		it.Fix, it.FixCommand = claudeFix, claudeFixCommand
		return it
	default:
		it.Status, it.Detail = session.CheckOK, p
		return it
	}
	it.Fix, it.FixCommand = claudeFix, claudeFixCommand
	return it
}

func (b *Backend) checkConfig(ctx context.Context) session.CheckItem {
	it := session.CheckItem{ID: "config", Label: "Claude config"}
	ops, ok := b.ops.(hookOps)
	if !ok {
		it.Status, it.Detail = session.CheckWarn, "cannot inspect the Claude config on this host"
		return it
	}
	dir, err := ops.configDir(ctx)
	if err != nil {
		it.Status, it.Detail = session.CheckWarn, "cannot determine the Claude config directory: "+err.Error()
		return it
	}
	settings := ops.join(dir, settingsName)
	data, existed, err := readOptional(ctx, ops, settings)
	switch {
	case err != nil:
		it.Status, it.Detail = session.CheckWarn, fmt.Sprintf("%s: cannot read %s: %v", dir, settingsName, err)
	case !existed:
		it.Status, it.Detail = session.CheckOK, dir+" (no "+settingsName+" yet)"
	case len(bytes.TrimSpace(data)) == 0:
		it.Status, it.Detail = session.CheckOK, dir
	default:
		var m map[string]json.RawMessage
		if err := json.Unmarshal(data, &m); err != nil {
			it.Status = session.CheckWarn
			it.Detail = fmt.Sprintf("%s: %s is not valid JSON: %v", dir, settingsName, err)
			it.Fix = "Fix or remove " + settings + " on this host (Claude Code can't read it either)."
		} else {
			it.Status, it.Detail = session.CheckOK, dir
		}
	}
	return it
}

func (b *Backend) checkHooks(ctx context.Context) session.CheckItem {
	it := session.CheckItem{ID: "hooks", Label: "Status hooks"}
	st, err := b.hooksStatus(ctx)
	switch {
	case err != nil:
		it.Status, it.Detail = session.CheckWarn, "cannot check hooks: "+err.Error()
		it.Fix = hooksFix
	case st == session.HooksInstalled:
		it.Status, it.Detail = session.CheckOK, "installed"
	case st == session.HooksOutdated:
		it.Status, it.Detail, it.Fix = session.CheckWarn, "outdated", hooksFix
	default:
		it.Status, it.Detail, it.Fix = session.CheckWarn, "not installed", hooksFix
	}
	return it
}
