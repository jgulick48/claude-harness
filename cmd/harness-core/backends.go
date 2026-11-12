package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jgulick48/claude-harness/internal/hosts"
	"github.com/jgulick48/claude-harness/internal/recent"
	"github.com/jgulick48/claude-harness/internal/session"
	"github.com/jgulick48/claude-harness/internal/session/tmuxbackend"
	"github.com/jgulick48/claude-harness/internal/sshconn"
)

// newHostManager builds the host manager served by the API: the local
// machine's tmux plus the persisted SSH hosts.
//
// Environment overrides (mainly for tests and development):
//
//	HARNESS_TMUX_BIN     tmux binary (default "tmux" on PATH); local host only
//	HARNESS_TMUX_SOCKET  tmux socket name, passed as -L (default: tmux's default
//	                     server); applies to the local host and to SSH hosts
//	HARNESS_TMUX_CONFIG  tmux config file, passed as -f (e.g. /dev/null to skip
//	                     ~/.tmux.conf); applies to the local host and SSH hosts
//	HARNESS_SUDO         sudo command typed into run-as sessions (default "sudo"
//	                     on the session shell's PATH); local and SSH hosts
//	HARNESS_HOSTS_FILE   saved SSH hosts (default <user config dir>/claude-harness/hosts.json)
//	HARNESS_STATE_FILE   recent directories per host (default <user config dir>/claude-harness/state.json)
//	HARNESS_SSH_CONFIG   ssh config file (default ~/.ssh/config)
//	HARNESS_KNOWN_HOSTS  known_hosts file (default ~/.ssh/known_hosts)
//
// The SSH agent comes from SSH_AUTH_SOCK (sshconn's default).
//
// If the hosts file can't be loaded (e.g. it is corrupt) the error is logged
// and the core runs with the local host only; the file is left untouched.
func newHostManager(logger *slog.Logger, getenv func(string) string) session.HostManager {
	tmuxOpts := tmuxbackend.Options{
		Socket: getenv("HARNESS_TMUX_SOCKET"),
		Config: getenv("HARNESS_TMUX_CONFIG"),
		Sudo:   getenv("HARNESS_SUDO"),
	}
	localOpts := tmuxOpts
	localOpts.Bin = getenv("HARNESS_TMUX_BIN")
	localOpts.Logger = logger.With("host", "local")
	local := tmuxbackend.NewLocal(localOpts)

	sshTmux := tmuxOpts
	sshTmux.Logger = logger
	hm, err := hosts.New(hosts.Options{
		File:  getenv("HARNESS_HOSTS_FILE"),
		Local: local,
		SSH: sshconn.Options{
			ConfigFile:     getenv("HARNESS_SSH_CONFIG"),
			KnownHostsFile: getenv("HARNESS_KNOWN_HOSTS"),
			Logger:         logger,
		},
		TmuxOptions: sshTmux,
		Logger:      logger,
	})
	if err != nil {
		logger.Error("SSH hosts unavailable; running with the local host only", "err", err)
		return localOnly{local}
	}
	return hm
}

// localOnly is the fallback HostManager when the hosts file can't be used.
type localOnly struct{ local session.Backend }

var errNoSSH = fmt.Errorf("%w: SSH hosts are unavailable (hosts file could not be loaded; see core log)", session.ErrUnavailable)

func (l localOnly) Backends() []session.Backend { return []session.Backend{l.local} }
func (l localOnly) Backend(id string) (session.Backend, bool) {
	return l.local, id == l.local.ID()
}
func (l localOnly) Add(context.Context, string, string) (session.Host, error) {
	return session.Host{}, errNoSSH
}
func (l localOnly) Remove(_ context.Context, id string) error {
	if id == l.local.ID() {
		return fmt.Errorf("%w: the local host can't be removed", session.ErrBadRequest)
	}
	return fmt.Errorf("%w: unknown host: %s", session.ErrNotFound, id)
}
func (l localOnly) Connect(context.Context, string) error              { return errNoSSH }
func (l localOnly) TrustHostKey(context.Context, string, string) error { return errNoSSH }
func (l localOnly) Subscribe(fn func([]session.Host)) func() {
	fn([]session.Host{l.local.Host(context.Background())})
	return func() {}
}
func (l localOnly) Close() error { return l.local.Close() }

// newRecentStore opens the recent-directories store (HARNESS_STATE_FILE, or the
// default path). If it can't be opened the error is logged and recording is
// disabled (nil).
func newRecentStore(logger *slog.Logger, getenv func(string) string) session.RecentDirs {
	st, err := recent.New(getenv("HARNESS_STATE_FILE"), logger)
	if err != nil {
		logger.Error("recent directories unavailable", "err", err)
		return nil
	}
	return st
}
