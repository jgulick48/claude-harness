package api

import (
	"context"

	"github.com/jgulick48/claude-harness/internal/session"
)

// handleCompleteDir serves remote.completeDir (protocol v4).
//
// CONTRACT STUB (M3): replaced by the implementation.
func (cn *conn) handleCompleteDir(ctx context.Context, b session.Backend, p *params) (any, error) {
	panic("remote.completeDir: not implemented")
}

// handleSuggestDirs serves remote.suggestDirs (protocol v4).
//
// CONTRACT STUB (M3): replaced by the implementation.
func (cn *conn) handleSuggestDirs(ctx context.Context, b session.Backend, p *params) (any, error) {
	panic("remote.suggestDirs: not implemented")
}
