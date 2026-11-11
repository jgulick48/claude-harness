package main

import (
	"log/slog"

	"github.com/jgulick48/claude-harness/internal/session"
)

// newBackends builds the host backends served by the API. Placeholder: the
// real tmux backend is wired here separately.
func newBackends(logger *slog.Logger, getenv func(string) string) []session.Backend {
	return nil
}
