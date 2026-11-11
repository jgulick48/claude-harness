package tmuxbackend

import (
	"context"

	"github.com/jgulick48/claude-harness/internal/sshconn"
)

// NewSSH returns a Backend for tmux on a remote host reached through c.
//
// CONTRACT STUB (M2): the implementation replaces this body.
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
	panic("tmuxbackend.NewSSH: not implemented")
}
