package api

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jgulick48/claude-harness/internal/session"
)

// handleHooks serves hosts.installHooks and hosts.removeHooks (protocol v5).
func (cn *conn) handleHooks(ctx context.Context, method string, p *params) (any, error) {
	b, err := cn.backend(p.Host)
	if err != nil {
		return nil, err
	}
	hi, ok := b.(session.HookInstaller)
	if !ok {
		return nil, fmt.Errorf("%w: hooks not supported on this host", session.ErrBadRequest)
	}
	if method == "hosts.installHooks" {
		usage := true // default: an old UI keeps working and gets usage
		if len(p.Usage) > 0 && string(p.Usage) != "null" {
			if json.Unmarshal(p.Usage, &usage) != nil {
				return nil, fmt.Errorf("%w: usage must be a boolean", session.ErrBadRequest)
			}
		}
		err = hi.InstallHooks(ctx, usage)
	} else {
		err = hi.RemoveHooks(ctx)
	}
	if err != nil {
		return nil, err
	}
	// Host.ClaudeHooks changed. SSH hosts announce it through their wrapper,
	// but the local host has none, so ask the manager to re-send the hosts.
	if n, ok := cn.s.hm.(session.HostsNotifier); ok {
		n.NotifyHostsChanged()
	}
	return nil, nil
}
