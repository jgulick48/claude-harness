// Package sshtest runs an in-process SSH server for tests. Each "exec" request
// runs the command locally via Shell -c, so tests can drive a local, isolated
// tmux "over SSH" without sshd or real keys. Not for production use.
//
// CONTRACT STUB (M2): signatures are fixed; the implementation replaces the
// bodies. Tests that need a real shell should skip on Windows.
package sshtest

import (
	"testing"

	"golang.org/x/crypto/ssh"
)

// Options configures a test server.
type Options struct {
	// AuthorizedKeys may authenticate. Required (no password auth).
	AuthorizedKeys []ssh.PublicKey
	// Env is appended to os.Environ() for exec'd commands (e.g. SHELL=/bin/sh, HOME=...).
	Env []string
	// Shell runs exec'd commands as Shell -c <command>. Default /bin/sh.
	Shell string
	// AllowTCPForwarding enables direct-tcpip channels (for ProxyJump tests).
	AllowTCPForwarding bool
}

// Server is a running test SSH server on 127.0.0.1.
type Server struct {
	Addr    string        // "127.0.0.1:<port>"
	Port    int
	HostKey ssh.PublicKey // the server's host key (ed25519, generated per server)
}

// Start starts a server; t.Cleanup stops it.
func Start(t testing.TB, opts Options) *Server { panic("sshtest.Start: not implemented") }

// DropConnections abruptly closes every open client connection (simulates a
// network drop) while the server keeps listening.
func (s *Server) DropConnections() { panic("not implemented") }

// Close stops listening and drops all connections.
func (s *Server) Close() { panic("not implemented") }

// NewKey returns a fresh ed25519 signer for tests.
func NewKey(t testing.TB) ssh.Signer { panic("sshtest.NewKey: not implemented") }

// StartAgent serves an in-memory ssh-agent holding signers on a unix socket in
// t.TempDir() and returns the socket path (unix only).
func StartAgent(t testing.TB, signers ...ssh.Signer) string {
	panic("sshtest.StartAgent: not implemented")
}
