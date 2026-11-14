package api

import (
	"context"
	"errors"

	"github.com/jgulick48/claude-harness/internal/session"
	"github.com/jgulick48/claude-harness/internal/usagehist"
)

// handleUsageHistory serves usage.history. A host that can't read
// transcripts, or a core without a history store, answers with no buckets.
func (cn *conn) handleUsageHistory(ctx context.Context, b session.Backend, p *params) (any, error) {
	empty := usagehist.Result{Buckets: []usagehist.Bucket{}}
	tr, ok := b.(session.TranscriptReader)
	if !ok || cn.s.usage == nil {
		return empty, nil
	}
	res, err := cn.s.usage.Scan(ctx, p.Host, tr)
	if errors.Is(err, session.ErrUnavailable) {
		return empty, nil
	}
	if err != nil {
		return nil, err
	}
	if res.Buckets == nil {
		res.Buckets = []usagehist.Bucket{}
	}
	return res, nil
}
