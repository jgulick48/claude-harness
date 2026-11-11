package api

import (
	"context"

	"github.com/jgulick48/claude-harness/internal/session"
)

// handleConfigHosts serves hosts.configHosts (protocol v4): the concrete hosts
// of ~/.ssh/config, when the host manager can list them; otherwise [].
func (cn *conn) handleConfigHosts(ctx context.Context) (any, error) {
	lister, ok := cn.s.hm.(session.ConfigHostLister)
	if !ok {
		return []session.ConfigHost{}, nil
	}
	list, err := lister.ConfigHosts(ctx)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []session.ConfigHost{}
	}
	return list, nil
}
