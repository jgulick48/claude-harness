package main

import (
	"log/slog"

	"github.com/jgulick48/claude-harness/internal/session"
	"github.com/jgulick48/claude-harness/internal/session/tmuxbackend"
)

// newBackends builds the host backends served by the API. M1 serves only the
// local machine's tmux; M2 adds SSH hosts.
//
// Environment overrides (mainly for tests and development):
//
//	HARNESS_TMUX_BIN     tmux binary (default "tmux" on PATH)
//	HARNESS_TMUX_SOCKET  tmux socket name, passed as -L (default: tmux's default server)
//	HARNESS_TMUX_CONFIG  tmux config file, passed as -f (e.g. /dev/null to skip ~/.tmux.conf)
func newBackends(logger *slog.Logger, getenv func(string) string) []session.Backend {
	local := tmuxbackend.NewLocal(tmuxbackend.Options{
		Bin:    getenv("HARNESS_TMUX_BIN"),
		Socket: getenv("HARNESS_TMUX_SOCKET"),
		Config: getenv("HARNESS_TMUX_CONFIG"),
		Logger: logger.With("host", "local"),
	})
	return []session.Backend{local}
}
