package hosts

import (
	"context"
	"fmt"
	"time"

	"github.com/jgulick48/claude-harness/internal/session"
)

var _ session.HostChecker = (*sshHost)(nil)

// Check implements session.HostChecker. Connected: the inner backend's report
// preceded by a "connection" item. Otherwise a report with only a failing
// "connection" item (not an error: the report itself is the answer).
func (h *sshHost) Check(ctx context.Context) (session.HostReport, error) {
	h.mu.Lock()
	inner, state, errMsg, target := h.inner, h.state, h.errMsg, h.target
	h.mu.Unlock()

	if inner == nil || state != session.HostConnected {
		detail := "host is " + state
		if errMsg != "" {
			detail += ": " + errMsg
		}
		return session.HostReport{Host: h.id, CheckedAt: time.Now().Unix(), Items: []session.CheckItem{{
			ID: "connection", Label: "Connection", Status: session.CheckError, Detail: detail,
			Fix: "Reconnect the host (or check the SSH target and keys)",
		}}}, nil
	}
	hc, ok := inner.(session.HostChecker)
	if !ok {
		return session.HostReport{}, fmt.Errorf("%w: setup check not supported on host %s", session.ErrBadRequest, h.id)
	}
	rep, err := hc.Check(ctx)
	if err != nil {
		return session.HostReport{}, err
	}
	conn := session.CheckItem{ID: "connection", Label: "Connection", Status: session.CheckOK, Detail: "connected to " + target}
	rep.Host = h.id
	rep.Items = append([]session.CheckItem{conn}, rep.Items...)
	return rep, nil
}
