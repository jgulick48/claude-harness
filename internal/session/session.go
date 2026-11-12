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
	// RetryAt is when the next automatic reconnect attempt happens (unix
	// milliseconds). Set only while State is HostDisconnected and a retry is
	// scheduled.
	RetryAt int64 `json:"retryAt,omitempty"`
	// ClaudeHooks reports whether the Claude status hooks are installed on
	// this host (HooksInstalled etc.); "" when not checked yet.
	ClaudeHooks string `json:"claudeHooks,omitempty"`
}

// HostKeyPrompt describes an unknown SSH host key awaiting the user's decision.
type HostKeyPrompt struct {
	Address     string `json:"address"`     // known_hosts name, e.g. "devbox.lan" or "[devbox.lan]:2222"
	KeyType     string `json:"keyType"`     // e.g. "ssh-ed25519"
	Fingerprint string `json:"fingerprint"` // "SHA256:..."
}

// Session is one tmux session.
type Session struct {
	ID          string `json:"id"`              // tmux session_id, e.g. "$3" (stable across renames)
	Name        string `json:"name"`            // tmux session_name
	HarnessName string `json:"harnessName"`     // user-chosen display name (@harness_name); "" if none
	Managed     bool   `json:"managed"`         // created by this app (@harness_managed=1)
	RunAs       string `json:"runAs,omitempty"` // @harness_run_as: user the session switched to
	Cwd         string `json:"cwd"`             // active pane's current path
	Windows     int    `json:"windows"`
	Attached    int    `json:"attached"` // tmux clients attached, including ours
	Created     int64  `json:"created"`  // unix seconds
	// ClaudeStatus is Claude Code's state in this session (see ClaudeWorking
	// etc.); "" when Claude isn't running. ClaudeStatusAt is when the status
	// last changed (unix seconds), 0 if unknown.
	ClaudeStatus   string `json:"claudeStatus"`
	ClaudeStatusAt int64  `json:"claudeStatusAt,omitempty"`
}

// Claude statuses (Session.ClaudeStatus). They come from Claude Code hooks
// that set the tmux session options @claude_status / @claude_status_at
// (docs/ARCHITECTURE.md §6).
const (
	ClaudeWorking = "working" // processing a prompt or running tools
	ClaudeWaiting = "waiting" // needs the user: a permission prompt or a question
	ClaudeIdle    = "idle"    // finished its turn; nothing pending
	// ClaudeUnknown: Claude is running in the session's active pane, but no
	// hook has reported (e.g. hooks not installed on that host).
	ClaudeUnknown = "unknown"
)

// Hook install states (Host.ClaudeHooks).
const (
	HooksInstalled = "installed" // our hooks are present and current
	HooksOutdated  = "outdated"  // our hooks are present but an older version
	HooksMissing   = "missing"   // not installed
	HooksUnknown   = "unknown"   // couldn't tell (host unreachable, unreadable settings)
)

// CreateOptions for Backend.Create.
type CreateOptions struct {
	Dir     string // absolute path or "~"/"~/..." (expanded on the host); "" means home
	Name    string // optional display name
	Command string // CommandShell (default when "") or CommandClaude
	RunAs   string // optional: switch to this user with sudo -iu inside the session
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

// DirEntry is one directory returned by DirCompleter.CompleteDir.
type DirEntry struct {
	Path    string `json:"path"`    // same form as the prefix ("~/src/app/" or "/srv/app/"); ends with "/"
	GitRepo bool   `json:"gitRepo"` // the directory contains .git
}

// DirCompleter is an optional Backend capability: browsing the host's
// directories for the new-terminal dialog. Callers type-assert for it.
type DirCompleter interface {
	// CompleteDir lists directories whose path starts with prefix. prefix is
	// "", "~", "~/...", or an absolute path; "" means "~/". A prefix ending in
	// "/" lists that directory's children; otherwise the last segment is a
	// name prefix. Results keep the prefix's form ("~/..." stays "~/..."),
	// end with "/", are sorted, and are capped at 100. Hidden directories are
	// included only when the typed name prefix starts with ".". A directory
	// that doesn't exist yields an empty list, not an error.
	CompleteDir(ctx context.Context, prefix string) ([]DirEntry, error)
	// FindRepos returns git repositories under the host's home as "~/..."
	// paths (with a trailing "/"), searching at most 3 levels deep, skipping
	// node_modules and hidden directories, capped at 50 results and ~3s.
	FindRepos(ctx context.Context) ([]string, error)
}

// ConfigHost is a concrete Host entry from ~/.ssh/config (no wildcards).
type ConfigHost struct {
	Alias    string `json:"alias"`
	HostName string `json:"hostName"`
	User     string `json:"user"`
	Port     int    `json:"port"`
	Added    bool   `json:"added"` // a saved host already uses this alias as its target
}

// ConfigHostLister is an optional HostManager capability: listing hosts that
// can be imported from ~/.ssh/config. Callers type-assert for it.
type ConfigHostLister interface {
	ConfigHosts(ctx context.Context) ([]ConfigHost, error)
}

// RecentDirs remembers directories used to create sessions, per host
// (most recent first, at most 10, persisted across restarts). Safe for
// concurrent use.
type RecentDirs interface {
	Add(host, dir string)
	List(host string) []string
	// Forget drops a host's history (e.g. when the host is removed).
	Forget(host string)
}

// HookInstaller is an optional Backend capability: installing the Claude Code
// hooks that report status into tmux. Callers type-assert for it.
//
// Install merges our hook entries into the host's Claude Code user settings
// file, leaving every other setting and hook untouched, after backing the file
// up. Remove deletes only our entries. Both are idempotent.
type HookInstaller interface {
	HooksStatus(ctx context.Context) (string, error) // HooksInstalled / HooksOutdated / HooksMissing
	InstallHooks(ctx context.Context) error
	RemoveHooks(ctx context.Context) error
}

// HostsNotifier is an optional HostManager capability: re-send the host list
// to subscribers now. Used after a change the manager can't observe itself,
// such as installing hooks on the local host (which has no SSH wrapper).
type HostsNotifier interface {
	NotifyHostsChanged()
}

// Check item states (CheckItem.Status).
const (
	CheckOK    = "ok"
	CheckWarn  = "warn"  // works, but something is missing or worth fixing
	CheckError = "error" // blocks using the host (e.g. tmux missing)
)

// CheckItem is one line of a host setup check.
type CheckItem struct {
	ID     string `json:"id"`     // stable key, e.g. "tmux", "claude", "hooks"
	Label  string `json:"label"`  // e.g. "tmux"
	Status string `json:"status"` // CheckOK, CheckWarn or CheckError
	Detail string `json:"detail"` // what was found, e.g. "3.4 at /usr/bin/tmux"
	// Fix explains how to resolve a warn/error ("" when ok). FixCommand is an
	// optional shell command the user can copy (never run by the app).
	Fix        string `json:"fix,omitempty"`
	FixCommand string `json:"fixCommand,omitempty"`
}

// HostReport is the result of a host setup check.
type HostReport struct {
	Host      string      `json:"host"`
	CheckedAt int64       `json:"checkedAt"` // unix seconds
	Items     []CheckItem `json:"items"`
}

// HostChecker is an optional Backend capability: a read-only setup check of
// the host (it never changes anything on the host). Callers type-assert for it.
type HostChecker interface {
	Check(ctx context.Context) (HostReport, error)
}
