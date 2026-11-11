//go:build !windows

package sshconn

import (
	"errors"
	"io"
	"net"
	"os"

	"golang.org/x/crypto/ssh/agent"
)

const onWindows = false

// connectAgent connects to the ssh-agent unix socket (opts.AgentSocket or $SSH_AUTH_SOCK).
func connectAgent(opts Options) (agent.Agent, io.Closer, error) {
	sock := opts.AgentSocket
	if sock == "" {
		sock = os.Getenv("SSH_AUTH_SOCK")
	}
	if sock == "" {
		return nil, nil, errors.New("SSH_AUTH_SOCK is not set")
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, nil, err
	}
	return agent.NewClient(conn), conn, nil
}
