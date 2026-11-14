package api

import (
	"context"
	"errors"
	"fmt"

	"github.com/jgulick48/claude-harness/internal/recent"
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
	pinned, recent := cn.dirLists(p.Host)
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
		Pinned []string `json:"pinned"`
		Recent []string `json:"recent"`
		Repos  []string `json:"repos"`
	}{pinned, recent, repos}, nil
}

// dirLists returns host's pinned and recent directories, never nil.
func (cn *conn) dirLists(host string) (pinned, recent []string) {
	pinned, recent = []string{}, []string{}
	if r := cn.s.recent; r != nil {
		if l := r.Pinned(host); l != nil {
			pinned = l
		}
		if l := r.List(host); l != nil {
			recent = l
		}
	}
	return pinned, recent
}

type pinResult struct {
	Pinned []string `json:"pinned"`
	Recent []string `json:"recent"`
}

// handlePinDir serves remote.pinDir.
func (cn *conn) handlePinDir(p *params) (any, error) {
	r, err := cn.pinnable(p)
	if err != nil {
		return nil, err
	}
	if err := r.Pin(p.Host, p.Dir); err != nil {
		if errors.Is(err, recent.ErrTooManyPinned) {
			return nil, fmt.Errorf("%w: too many pinned directories (at most %d per host); unpin one first", session.ErrBadRequest, recent.MaxPinnedPerHost)
		}
		return nil, err
	}
	pinned, rec := cn.dirLists(p.Host)
	return pinResult{pinned, rec}, nil
}

// handleUnpinDir serves remote.unpinDir.
func (cn *conn) handleUnpinDir(p *params) (any, error) {
	r, err := cn.pinnable(p)
	if err != nil {
		return nil, err
	}
	r.Unpin(p.Host, p.Dir)
	pinned, rec := cn.dirLists(p.Host)
	return pinResult{pinned, rec}, nil
}

func (cn *conn) pinnable(p *params) (session.RecentDirs, error) {
	if p.Dir == "" {
		return nil, fmt.Errorf("%w: dir is required", session.ErrBadRequest)
	}
	if cn.s.recent == nil {
		return nil, fmt.Errorf("%w: pinned directories are not available", session.ErrUnavailable)
	}
	return cn.s.recent, nil
}
