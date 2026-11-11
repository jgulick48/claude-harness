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
	HostConnected   = "connected"
	HostUnavailable = "unavailable" // e.g. tmux missing or older than 3.2
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
	ID          string `json:"id"`   // "local" in M1
	Name        string `json:"name"` // display name, e.g. "This computer"
	State       string `json:"state"`
	Error       string `json:"error,omitempty"`
	Home        string `json:"home,omitempty"` // $HOME on that host, for "~" display
	TmuxVersion string `json:"tmuxVersion,omitempty"`
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
