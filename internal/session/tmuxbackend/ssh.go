package tmuxbackend

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/jgulick48/claude-harness/internal/session"
	"github.com/jgulick48/claude-harness/internal/sshconn"
	"github.com/jgulick48/claude-harness/internal/tmux"
)

// tmuxProbePaths are checked (test -x) when tmux isn't on the PATH. A var so
// tests can override it.
var tmuxProbePaths = []string{
	"/opt/homebrew/bin/tmux",
	"/usr/local/bin/tmux",
	"/usr/bin/tmux",
	"/home/linuxbrew/.linuxbrew/bin/tmux",
}

// NewSSH returns a Backend for tmux on a remote host reached through c.
//
//   - opts.Bin "" means: locate tmux on the host (PATH via the user's shell,
//     then a login shell, then common install paths such as /opt/homebrew/bin).
//     Not found → Host() reports HostUnavailable "tmux not found on <name>".
//   - opts.Home "" means: ask the host for $HOME.
//   - opts.Socket / opts.Config pass -L / -f to the remote tmux (tests).
//   - Directory checks in Create run on the host (test -d), not locally.
//   - When c.Done() closes, open terminals end with Err "connection lost" and
//     List/Create/... wrap session.ErrUnavailable.
//
// The returned Backend does not own c; the caller closes it.
func NewSSH(ctx context.Context, id, name string, c *sshconn.Client, opts Options) (*Backend, error) {
	r := &tmux.SSHRunner{Client: c.SSH(), Bin: opts.Bin, Socket: opts.Socket, Config: opts.Config}
	ops := sshOps{r: r}

	if opts.Home == "" {
		home, err := ops.homeDir(ctx)
		if err != nil {
			return nil, fmt.Errorf("look up home directory on %s: %w", name, err)
		}
		opts.Home = home
	}
	noTmux := ""
	if opts.Bin == "" {
		bin, err := locateTmux(ctx, r)
		if err != nil {
			if cerr := ctx.Err(); cerr != nil {
				return nil, cerr
			}
			var te *tmux.Error
			if !errors.As(err, &te) { // transport failure, not "not found"
				return nil, fmt.Errorf("locate tmux on %s: %w", name, err)
			}
		}
		if bin == "" {
			noTmux = "tmux not found on " + name
		} else {
			r.Bin = bin
		}
	}

	b := newBackend(id, name, r, opts)
	b.kind = session.KindSSH
	b.ops = ops
	b.noTmux = noTmux
	b.lostCh = make(chan struct{})
	b.connDone = c.Done()
	go func() {
		<-c.Done()
		close(b.lostCh)
		b.onConnectionLost()
	}()
	return b, nil
}

// locateTmux returns an absolute tmux path, or "" if none was found.
func locateTmux(ctx context.Context, r *tmux.SSHRunner) (string, error) {
	var lastErr error
	try := func(cmdline string) string {
		out, err := r.Exec(ctx, cmdline)
		if err != nil {
			lastErr = err
			var te *tmux.Error
			if !errors.As(err, &te) {
				return ""
			}
			lastErr = nil
		}
		// Login shells may print rc-file noise; the path is the last such line.
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			if l := strings.TrimSpace(lines[i]); strings.HasPrefix(l, "/") {
				return l
			}
		}
		return ""
	}
	if p := try("command -v tmux"); p != "" {
		return p, nil
	}
	if lastErr != nil {
		return "", lastErr
	}
	if p := try(`"$SHELL" -lc 'command -v tmux'`); p != "" {
		return p, nil
	}
	if lastErr != nil {
		return "", lastErr
	}
	for _, p := range tmuxProbePaths {
		if _, err := r.Exec(ctx, "test -x "+tmux.ShellQuote(p)); err == nil {
			return p, nil
		} else if ctx.Err() != nil {
			return "", ctx.Err()
		}
	}
	return "", nil
}

type sshOps struct{ r *tmux.SSHRunner }

func (o sshOps) dirExists(ctx context.Context, dir string) (bool, error) {
	_, err := o.r.Exec(ctx, "test -d "+tmux.ShellQuote(dir))
	if err == nil {
		return true, nil
	}
	var exit *ssh.ExitError
	if errors.As(err, &exit) {
		return false, nil
	}
	return false, err
}

func (o sshOps) homeDir(ctx context.Context) (string, error) {
	out, err := o.r.Exec(ctx, `printf %s "$HOME"`)
	if err != nil {
		return "", err
	}
	home := strings.TrimSpace(string(out))
	if home == "" {
		return "", errors.New("$HOME is empty")
	}
	return home, nil
}
