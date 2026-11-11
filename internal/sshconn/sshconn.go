// Package sshconn dials SSH hosts the way OpenSSH users expect: ~/.ssh/config
// resolution (HostName, User, Port, IdentityFile, ProxyJump), agent auth
// (SSH_AUTH_SOCK on macOS/Linux, Pageant on Windows), identity files as a
// fallback on macOS/Linux, and strict known_hosts checking.
//
// The exported signatures are a fixed contract that other packages build
// against.
package sshconn

import (
	"errors"
	"log/slog"
	"time"

	"golang.org/x/crypto/ssh"
)

// Options configures Resolve and Dial. Zero values mean the OpenSSH defaults.
type Options struct {
	ConfigFile     string // default ~/.ssh/config (missing file is fine)
	KnownHostsFile string // default ~/.ssh/known_hosts
	// AgentSocket is the agent's unix socket. Default $SSH_AUTH_SOCK.
	// Ignored on Windows, where Pageant is the agent.
	AgentSocket string
	// IdentityFiles are tried after the config's IdentityFile entries and
	// before the defaults (~/.ssh/id_ed25519, id_ecdsa, id_rsa). macOS/Linux only;
	// passphrase-protected keys are skipped (load them into the agent).
	IdentityFiles []string
	Timeout       time.Duration // per-hop dial + handshake; default 15s
	KeepAlive     time.Duration // keepalive interval; default 15s; negative disables
	Logger        *slog.Logger
}

// Resolved is a target after ~/.ssh/config resolution.
type Resolved struct {
	Target        string // the input: alias or user@host[:port]
	User          string
	HostName      string
	Port          int
	IdentityFiles []string
	ProxyJump     []string // hops in order, each an alias or user@host[:port]
}

// UnknownHostKeyError: the host (or a ProxyJump hop) isn't in known_hosts.
type UnknownHostKeyError struct {
	Address     string // known_hosts name: "host" or "[host]:port"
	KeyType     string // e.g. "ssh-ed25519"
	Fingerprint string // ssh.FingerprintSHA256 form, "SHA256:..."
	Key         ssh.PublicKey
}

func (e *UnknownHostKeyError) Error() string {
	return "unknown host key for " + e.Address + " (" + e.KeyType + " " + e.Fingerprint + ")"
}

// HostKeyChangedError: known_hosts has a different key for this address.
// Never auto-resolved; the user must fix known_hosts.
type HostKeyChangedError struct {
	Address     string
	KeyType     string
	Fingerprint string // the key the server presented
}

func (e *HostKeyChangedError) Error() string {
	return "host key for " + e.Address + " has CHANGED (now " + e.KeyType + " " + e.Fingerprint +
		"). This could be a man-in-the-middle attack. If the change is expected, remove the old entry from known_hosts."
}

// ErrNoAuthMethods: no agent keys or usable identity files.
var ErrNoAuthMethods = errors.New("no SSH authentication methods available")

// ErrConnectionLost is wrapped by Client.Err when an established connection
// ends on its own (remote close, keepalive failure). Its text is
// "connection lost".
var ErrConnectionLost = errors.New("connection lost")
