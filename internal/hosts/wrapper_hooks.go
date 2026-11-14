package hosts

import (
	"context"
	"fmt"

	"github.com/jgulick48/claude-harness/internal/session"
)

var _ session.HookInstaller = (*sshHost)(nil)

// hookInstaller returns the current inner backend as a HookInstaller, or
// ErrUnavailable when the host is not connected or can't manage hooks.
func (h *sshHost) hookInstaller() (session.HookInstaller, error) {
	b, err := h.current()
	if err != nil {
		return nil, err
	}
	hi, ok := b.(session.HookInstaller)
	if !ok {
		return nil, fmt.Errorf("%w: host %s cannot manage Claude hooks", session.ErrUnavailable, h.id)
	}
	return hi, nil
}

// HooksStatus delegates to the current inner backend.
func (h *sshHost) HooksStatus(ctx context.Context) (string, error) {
	hi, err := h.hookInstaller()
	if err != nil {
		return "", err
	}
	return hi.HooksStatus(ctx)
}

// InstallHooks delegates to the current inner backend, then tells subscribers
// (hosts.changed) that the host's hook state may have changed.
func (h *sshHost) InstallHooks(ctx context.Context, usage bool) error {
	hi, err := h.hookInstaller()
	if err != nil {
		return err
	}
	if err := hi.InstallHooks(ctx, usage); err != nil {
		return err
	}
	h.m.notify()
	return nil
}

// RemoveHooks delegates to the current inner backend, then notifies like
// InstallHooks.
func (h *sshHost) RemoveHooks(ctx context.Context) error {
	hi, err := h.hookInstaller()
	if err != nil {
		return err
	}
	if err := hi.RemoveHooks(ctx); err != nil {
		return err
	}
	h.m.notify()
	return nil
}
