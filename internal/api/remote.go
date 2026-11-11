package api

import (
	"context"
	"errors"

	"github.com/jgulick48/claude-harness/internal/session"
)

// handleCompleteDir serves remote.completeDir (protocol v4).
func (cn *conn) handleCompleteDir(ctx context.Context, b session.Backend, p *params) (any, error) {
	dc, ok := b.(session.DirCompleter)
	if !ok {
		return []session.DirEntry{}, nil
	}
	es, err := dc.CompleteDir(ctx, p.Prefix)
	if err != nil {
		return nil, err
	}
	if es == nil {
		es = []session.DirEntry{}
	}
	return es, nil
}

// handleSuggestDirs serves remote.suggestDirs (protocol v4).
func (cn *conn) handleSuggestDirs(ctx context.Context, b session.Backend, p *params) (any, error) {
	recent := []string{}
	if r := cn.s.recent; r != nil {
		if l := r.List(p.Host); l != nil {
			recent = l
		}
	}
	repos := []string{}
	if dc, ok := b.(session.DirCompleter); ok {
		l, err := dc.FindRepos(ctx)
		switch {
		case err == nil:
			if l != nil {
				repos = l
			}
		case !errors.Is(err, session.ErrUnavailable):
			cn.s.logger.Warn("finding repositories failed", "host", p.Host, "err", err)
		}
	}
	return struct {
		Recent []string `json:"recent"`
		Repos  []string `json:"repos"`
	}{recent, repos}, nil
}
