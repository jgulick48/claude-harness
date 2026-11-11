// Package session defines the host/session abstraction the API serves.
// Implementations: tmux (internal/session/tmuxbackend, local in M1, SSH in M2).
// See docs/ARCHITECTURE.md §4 and docs/CORE_PROTOCOL.md §5.
package session

import (
	"context"
	"errors"
)

// Host states.
const (
	HostConnected    = "connected"
	HostConnecting   = "connecting"
	HostDisconnected = "disconnected"     // SSH connection dropped or not yet attempted; Error says why
	HostUnavailable  = "unavailable"      // reachable, but tmux missing or older than 3.2
	HostKeyUnknown   = "host_key_unknown" // first connection: HostKey must be trusted by the user
	HostError        = "error"            // connect failed (auth, DNS, changed host key, ...); Error says why
)

// Host kinds.
const (
	KindLocal = "local"
	KindSSH   = "ssh"
)

// Commands accepted by CreateOptions.Command.
const (
	CommandShell  = "shell"
	CommandClaude = "claude"
)

var (
	ErrNotFound   = errors.New("session not found")
	ErrBadRequest = errors.New("bad request")
	// ErrUnavailable means the host can't run sessions (e.g. tmux missing/too old).
	ErrUnavailable = errors.New("host unavailable")
)

// Errors returned by backends wrap one of the sentinels above where applicable
// (fmt.Errorf("%w: directory not found: %s", ErrBadRequest, dir)) so the API can
// map them to protocol error codes with errors.Is. The wrapped message is shown
// to the user.

// Host describes one machine and its tmux server.
type Host struct {
	ID          string `json:"id"`               // "local", or a slug for SSH hosts (e.g. "devbox")
	Name        string `json:"name"`             // display name, e.g. "This computer", "devbox"
	Kind        string `json:"kind"`             // KindLocal or KindSSH
	Target      string `json:"target,omitempty"` // SSH: what the user entered (alias or user@host[:port])
	State       string `json:"state"`
	Error       string `json:"error,omitempty"`
	Home        string `json:"home,omitempty"` // $HOME on that host, for "~" display
	TmuxVersion string `json:"tmuxVersion,omitempty"`
	// HostKey is set when State == HostKeyUnknown.
	HostKey *HostKeyPrompt `json:"hostKey,omitempty"`
}

// HostKeyPrompt describes an unknown SSH host key awaiting the user's decision.
type HostKeyPrompt struct {
	Address     string `json:"address"`     // known_hosts name, e.g. "devbox.lan" or "[devbox.lan]:2222"
	KeyType     string `json:"keyType"`     // e.g. "ssh-ed25519"
	Fingerprint string `json:"fingerprint"` // "SHA256:..."
}

// Session is one tmux session.
type Session struct {
	ID          string `json:"id"`          // tmux session_id, e.g. "$3" (stable across renames)
	Name        string `json:"name"`        // tmux session_name
	HarnessName string `json:"harnessName"` // user-chosen display name (@harness_name); "" if none
	Managed     bool   `json:"managed"`     // created by this app (@harness_managed=1)
	Cwd         string `json:"cwd"`         // active pane's current path
	Windows     int    `json:"windows"`
	Attached    int    `json:"attached"` // tmux clients attached, including ours
	Created     int64  `json:"created"`  // unix seconds
}

// CreateOptions for Backend.Create.
type CreateOptions struct {
	Dir     string // absolute path or "~"/"~/..." (expanded on the host); "" means home
	Name    string // optional display name
	Command string // CommandShell (default when "") or CommandClaude
}

// Backend is one host's session provider. All methods are safe for concurrent use.
type Backend interface {
	ID() string
	// Host probes and returns current host info (cheap; may be cached briefly).
	Host(ctx context.Context) Host
	// List returns all sessions, sorted by Created ascending. No tmux server
	// running is not an error: it returns an empty list.
	List(ctx context.Context) ([]Session, error)
	Create(ctx context.Context, opts CreateOptions) (Session, error)
	Kill(ctx context.Context, id string) error
	// Rename sets the display name (@harness_name); the tmux session name is unchanged.
	Rename(ctx context.Context, id, name string) error
	// Subscribe calls fn with the full list immediately and whenever it changes
	// (including after Create/Kill/Rename). Returns an unsubscribe func.
	Subscribe(fn func([]Session)) (unsubscribe func())
	// Attach opens a live view of the session's active pane at the given size.
	Attach(ctx context.Context, id string, cols, rows int) (Terminal, error)
	// Close detaches all terminals and stops background work. Sessions persist.
	Close() error
}

// Terminal is an attached view of one session.
type Terminal interface {
	// Output yields the screen seed first, then live output. It is closed when
	// the terminal ends (session killed/exited, Close called, or error).
	Output() <-chan []byte
	// Write sends input bytes (keystrokes, paste) to the pane.
	Write(p []byte) error
	Resize(cols, rows int) error
	// Close detaches; the tmux session keeps running.
	Close() error
	// Err reports why Output closed: nil after Close, otherwise the reason
	// (e.g. "session ended").
	Err() error
}

// HostManager owns the set of hosts (local + configured SSH hosts), their
// persistence, and connection lifecycle. The API serves whatever it holds.
type HostManager interface {
	// Backends returns the current backends, local first, then SSH hosts in
	// the order they were added.
	Backends() []Backend
	Backend(id string) (Backend, bool)
	// Add saves a new SSH host and starts connecting in the background.
	// target is an ~/.ssh/config alias or user@host[:port]; name is optional
	// (default: the target's host part). Duplicate targets wrap ErrBadRequest.
	Add(ctx context.Context, target, name string) (Host, error)
	// Remove disconnects and forgets an SSH host ("local" can't be removed:
	// ErrBadRequest). Its tmux sessions keep running on the host.
	Remove(ctx context.Context, id string) error
	// Connect (re)connects a host that is disconnected or in error.
	Connect(ctx context.Context, id string) error
	// TrustHostKey appends the pending key to known_hosts and reconnects.
	// fingerprint must equal the pending HostKey.Fingerprint (else ErrBadRequest).
	TrustHostKey(ctx context.Context, id, fingerprint string) error
	// Subscribe calls fn with all hosts now and whenever any host's info or
	// the set of hosts changes. Callbacks must not block.
	Subscribe(fn func([]Host)) (unsubscribe func())
	Close() error
}
