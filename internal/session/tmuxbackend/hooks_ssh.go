package tmuxbackend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/jgulick48/claude-harness/internal/tmux"
)

var _ hookOps = sshOps{}

// exitNotFound is the exit status our read script uses for a missing file.
const exitNotFound = 44

const configDirPrintf = `printf %s "${CLAUDE_CONFIG_DIR:-$HOME/.claude}"`

func (o sshOps) join(elem ...string) string { return path.Join(elem...) }

func (o sshOps) configDir(ctx context.Context) (string, error) {
	out, err := o.r.Exec(ctx, configDirPrintf)
	if err != nil {
		return "", err
	}
	if d := strings.TrimSpace(string(out)); d != "" {
		return d, nil
	}
	// Same approach as locateTmux: a login shell, last non-empty line.
	out, err = o.r.Exec(ctx, `"$SHELL" -lc `+tmux.ShellQuote(configDirPrintf))
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if d := strings.TrimSpace(lines[len(lines)-1]); d != "" {
		return d, nil
	}
	return "", errors.New("cannot determine the Claude config directory")
}

func exitCode(err error) (int, bool) {
	var exit *ssh.ExitError
	if errors.As(err, &exit) {
		return exit.ExitStatus(), true
	}
	return 0, false
}

func (o sshOps) readFile(ctx context.Context, p string) ([]byte, error) {
	q := tmux.ShellQuote(p)
	out, err := o.r.Exec(ctx, fmt.Sprintf("test -e %s || exit %d; cat %s", q, exitNotFound, q))
	if err != nil {
		if code, ok := exitCode(err); ok && code == exitNotFound {
			return nil, fmt.Errorf("%s: %w", p, fs.ErrNotExist)
		}
		return nil, err
	}
	return out, nil
}

func (o sshOps) exists(ctx context.Context, p string) (bool, error) {
	_, err := o.r.Exec(ctx, "test -e "+tmux.ShellQuote(p))
	if err == nil {
		return true, nil
	}
	if _, ok := exitCode(err); ok {
		return false, nil
	}
	return false, err
}

func (o sshOps) removeFile(ctx context.Context, p string) error {
	_, err := o.r.Exec(ctx, "rm -f "+tmux.ShellQuote(p))
	return err
}

func (o sshOps) writeFileAtomic(ctx context.Context, p string, data []byte, perm fs.FileMode, keepMode bool) error {
	f := tmux.ShellQuote(p)
	tmp := tmux.ShellQuote(p+".tmp.") + "$$"
	create := fmt.Sprintf("(umask 077; cat > %s) && chmod %04o %s", tmp, perm.Perm(), tmp)
	write := create
	if keepMode {
		// cp -p carries the existing mode; cat then rewrites the contents.
		write = fmt.Sprintf("if [ -e %s ]; then cp -p %s %s && cat > %s; else %s; fi", f, f, tmp, tmp, create)
	}
	cmd := fmt.Sprintf(`(umask 077; mkdir -p "$(dirname %s)") && { %s; } && mv -f %s %s || { rm -f %s; exit 1; }`,
		f, write, tmp, f, tmp)
	_, err := o.execStdin(ctx, cmd, data)
	return err
}

// execStdin runs cmdline on the host with stdin fed from data.
func (o sshOps) execStdin(ctx context.Context, cmdline string, data []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sess, err := o.r.Client.NewSession()
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	var stdout, stderr bytes.Buffer
	sess.Stdin = bytes.NewReader(data)
	sess.Stdout, sess.Stderr = &stdout, &stderr
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			sess.Close()
		case <-done:
		}
	}()
	if err := sess.Run(cmdline); err != nil {
		if ctx.Err() != nil {
			return stdout.Bytes(), ctx.Err()
		}
		if _, ok := exitCode(err); ok {
			return stdout.Bytes(), &tmux.Error{Stderr: strings.TrimSpace(stderr.String()), Err: err}
		}
		return stdout.Bytes(), err
	}
	return stdout.Bytes(), nil
}
