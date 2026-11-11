//go:build windows

package sshconn

import (
	"errors"
	"io"

	pageant "github.com/davidmz/go-pageant"
	"golang.org/x/crypto/ssh/agent"
)

const onWindows = true

// connectAgent talks to Pageant (PuTTY's agent) via its window-message
// interface. Pageant holds and unlocks the keys; we never read key files here.
func connectAgent(Options) (agent.Agent, io.Closer, error) {
	if !pageant.Available() {
		return nil, nil, errors.New("Pageant is not running")
	}
	return pageant.New(), io.NopCloser(nil), nil
}
