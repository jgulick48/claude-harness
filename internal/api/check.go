package api

import (
	"context"
	"fmt"

	"github.com/jgulick48/claude-harness/internal/session"
)

// handleCheck serves hosts.check (protocol v6): a read-only setup check.
func (cn *conn) handleCheck(ctx context.Context, p *params) (any, error) {
	b, err := cn.backend(p.Host)
	if err != nil {
		return nil, err
	}
	hc, ok := b.(session.HostChecker)
	if !ok {
		return nil, fmt.Errorf("%w: setup check not supported on this host", session.ErrBadRequest)
	}
	rep, err := hc.Check(ctx)
	if err != nil {
		return nil, err
	}
	if rep.Items == nil {
		rep.Items = []session.CheckItem{}
	}
	return rep, nil
}
