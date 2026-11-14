package api

import (
	"context"
	"fmt"

	"github.com/jgulick48/claude-harness/internal/session"
)

// handleHostLoad serves hosts.load. A backend that can't read load is
// unsupported.
func (cn *conn) handleHostLoad(ctx context.Context, p *params) (any, error) {
	b, err := cn.backend(p.Host)
	if err != nil {
		return nil, err
	}
	lr, ok := b.(session.LoadReader)
	if !ok {
		return nil, fmt.Errorf("%w: host cannot report load", session.ErrUnsupported)
	}
	return lr.ReadLoad(ctx)
}
