// Package tmuxbackend implements session.Backend on top of tmux.
package tmuxbackend

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jgulick48/claude-harness/internal/session"
	"github.com/jgulick48/claude-harness/internal/tmux"
)

// Options configures a local backend.
type Options struct {
	Bin    string // tmux binary; default "tmux"
	Socket string // tmux -L socket name; optional
	Config string // tmux -f config file; optional
	Home   string // default os.UserHomeDir()
	// Sudo is the sudo command run-as sessions type (default "sudo" on the
	// session shell's PATH). Tests point it at a fake.
	Sudo string
	// StatusDir is the directory run-as sessions' Claude status files live in
	// (default /tmp/claude-harness-status-<login uid>). Tests override it.
	StatusDir    string
	PollInterval time.Duration
	Logger       *slog.Logger
}

const (
	hostCacheTTL = 5 * time.Second
	fieldSep     = "\x1f"
)

// Backend is a session.Backend for one tmux server.
type Backend struct {
	id, name string
	run      tmux.Runner
	home     string
	sudo     string
	kind     string
	ops      hostOps
	poll     time.Duration
	log      *slog.Logger

	// Run-as Claude status (see runas_status.go).
	statusDir   string
	statusMu    sync.Mutex
	statusReady string    // set once the status dir is prepared
	statusBase  string    // resolved status dir (override or by uid)
	sweeping    bool      // a status-file sweep is running
	lastSweep   time.Time // when the last sweep started

	hostMu    sync.Mutex
	hostCache session.Host
	hostAt    time.Time

	// Subscriptions. notifyMu serializes list+compare+callbacks; callbacks run
	// under it and must not call back into Create/Kill/Rename/Subscribe.
	notifyMu sync.Mutex
	subsMu   sync.Mutex
	subs     map[int]func([]session.Session)
	nextSub  int
	last     []session.Session
	haveLast bool
	stopPoll chan struct{} // non-nil while the poll goroutine runs
	kickCh   chan struct{}

	termMu sync.Mutex
	terms  map[*terminal]struct{}
	closed bool

	// SSH backends only. lostCh is closed when the connection drops (never
	// closed for local backends); noTmux is set when tmux could not be found.
	lostCh chan struct{}
	// connDone is the SSH client's own Done channel. It closes a moment before
	// lostCh (which is closed by a goroutine), so isLost checks both: a host
	// manager closing this backend right after the drop must still see the loss.
	connDone <-chan struct{}
	noTmux   string // non-empty: reason tmux is unusable
}

// hostOps are the host-side operations that don't go through tmux.
type hostOps interface {
	// dirExists reports whether dir is an existing directory on the host.
	dirExists(ctx context.Context, dir string) (bool, error)
	// homeDir returns the host user's home directory.
	homeDir(ctx context.Context) (string, error)
	// completeDir lists the subdirectories of dir (an absolute POSIX path)
	// whose names start with name. A missing dir yields nothing. Hidden-name
	// and ordering rules are applied by the caller; at most limit entries need
	// be returned.
	completeDir(ctx context.Context, dir, name string, limit int) ([]dirItem, error)
	// findRepos returns git repository directories under home as paths
	// relative to home ("src/app"), searching to depth 3. It returns what it
	// has when ctx expires.
	findRepos(ctx context.Context, home string) ([]string, error)
}

type localOps struct{ home string }

func (o localOps) dirExists(_ context.Context, dir string) (bool, error) {
	fi, err := os.Stat(dir)
	return err == nil && fi.IsDir(), nil
}

func (o localOps) homeDir(context.Context) (string, error) { return o.home, nil }

// gate returns an ErrUnavailable error when the backend can't serve requests.
func (b *Backend) gate() error {
	// isLost, not just lostCh: lostCh is closed by a goroutine a moment after
	// the SSH client's Done channel, and in between a request would hit the
	// dead connection and return a raw "use of closed network connection".
	if b.isLost() {
		return fmt.Errorf("%w: connection lost", session.ErrUnavailable)
	}
	if b.noTmux != "" {
		return fmt.Errorf("%w: %s", session.ErrUnavailable, b.noTmux)
	}
	return nil
}

// mapError is mapErr, but reports a dropped connection as ErrUnavailable.
func (b *Backend) mapError(err error, id string) error {
	if err == nil {
		return nil
	}
	if b.isLost() {
		return fmt.Errorf("%w: connection lost", session.ErrUnavailable)
	}
	return mapErr(err, id)
}

// NewLocal returns a backend for the tmux server on this machine.
func NewLocal(opts Options) *Backend {
	return newBackend("local", "This computer",
		&tmux.LocalRunner{Bin: opts.Bin, Socket: opts.Socket, Config: opts.Config}, opts)
}

func newBackend(id, name string, run tmux.Runner, opts Options) *Backend {
	if opts.Home == "" {
		opts.Home, _ = os.UserHomeDir()
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 2 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	return &Backend{
		id: id, name: name, run: run, kind: session.KindLocal, ops: localOps{home: opts.Home},
		home: opts.Home, sudo: opts.Sudo, statusDir: opts.StatusDir, poll: opts.PollInterval, log: opts.Logger,
		subs:   map[int]func([]session.Session){},
		kickCh: make(chan struct{}, 1),
		terms:  map[*terminal]struct{}{},
	}
}

func (b *Backend) ID() string { return b.id }

var versionRe = regexp.MustCompile(`(\d+)\.(\d+)`)

func (b *Backend) Host(ctx context.Context) session.Host {
	b.hostMu.Lock()
	defer b.hostMu.Unlock()
	if !b.hostAt.IsZero() && time.Since(b.hostAt) < hostCacheTTL {
		return b.hostCache
	}
	h := session.Host{ID: b.id, Name: b.name, Kind: b.kind, Home: b.home, State: session.HostConnected}
	if b.isLost() {
		h.State, h.Error = session.HostDisconnected, "connection lost"
		return h
	}
	if b.noTmux != "" {
		h.State, h.Error = session.HostUnavailable, b.noTmux
		b.hostCache, b.hostAt = h, time.Now()
		return h
	}
	out, err := b.run.Output(ctx, "-V")
	switch {
	case err != nil:
		h.State = session.HostUnavailable
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			h.Error = "tmux not found"
		} else {
			h.Error = "tmux failed: " + err.Error()
		}
	default:
		h.TmuxVersion = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "tmux"))
		// Unparseable versions (e.g. "master") are assumed new enough.
		if m := versionRe.FindStringSubmatch(h.TmuxVersion); m != nil {
			maj, _ := strconv.Atoi(m[1])
			min, _ := strconv.Atoi(m[2])
			if maj < 3 || maj == 3 && min < 2 {
				h.State = session.HostUnavailable
				h.Error = fmt.Sprintf("tmux 3.2 or newer required (found %s)", h.TmuxVersion)
			}
		}
	}
	if h.State == session.HostConnected {
		if hi, ok := any(b).(session.HookInstaller); ok {
			st, err := hi.HooksStatus(ctx)
			if err != nil || st == "" {
				st = session.HooksUnknown
			}
			h.ClaudeHooks = st
		}
	}
	if ctx.Err() == nil { // don't cache a probe that was cut short
		b.hostCache, b.hostAt = h, time.Now()
	}
	return h
}

// invalidateHostCache drops the cached Host so the next Host call re-probes
// (used after hooks are installed or removed).
func (b *Backend) invalidateHostCache() {
	b.hostMu.Lock()
	b.hostAt = time.Time{}
	b.hostMu.Unlock()
}

func isNoServer(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "no server running") ||
		strings.Contains(s, "error connecting to") ||
		strings.Contains(s, "failed to connect to server")
}

// mapErr turns a tmux failure into a session sentinel where one applies.
func mapErr(err error, id string) error {
	if err == nil {
		return nil
	}
	s := err.Error()
	if isNoServer(err) || strings.Contains(s, "can't find session") || strings.Contains(s, "no such session") ||
		strings.Contains(s, "can't find pane") || strings.Contains(s, "can't find window") {
		return fmt.Errorf("%w: %s", session.ErrNotFound, id)
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: tmux not found", session.ErrUnavailable)
	}
	return err
}

var listFormat = strings.Join([]string{
	"#{session_id}", "#{session_name}", "#{@harness_name}", "#{@harness_managed}",
	"#{pane_current_path}", "#{session_windows}", "#{session_attached}", "#{session_created}",
	"#{@claude_status}", "#{@claude_status_at}", "#{pane_current_command}", "#{@harness_run_as}",
	"#{@harness_status_file}",
}, fieldSep)

// shellCommands are pane commands that mean "no Claude here".
var shellCommands = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true, "ksh": true, "tcsh": true,
}

// deriveClaudeStatus maps the @claude_status option and the active pane's
// command to a Session.ClaudeStatus. A shell in the active pane means Claude
// has exited, so a stale option is ignored.
func deriveClaudeStatus(option, paneCmd string) string {
	if shellCommands[strings.TrimPrefix(paneCmd, "-")] {
		return ""
	}
	switch option {
	case session.ClaudeWorking, session.ClaudeWaiting, session.ClaudeIdle:
		return option
	}
	if paneCmd == "claude" {
		return session.ClaudeUnknown
	}
	return ""
}

func (b *Backend) List(ctx context.Context) ([]session.Session, error) {
	if err := b.gate(); err != nil {
		return nil, err
	}
	out, err := b.run.Output(ctx, "list-sessions", "-F", listFormat)
	if err != nil {
		if isNoServer(err) {
			b.maybeSweepStatusFiles(nil) // no sessions: nothing references a file
			return []session.Session{}, nil
		}
		return nil, b.mapError(err, "")
	}
	list := []session.Session{}
	var statusFiles, panes []string // parallel to list
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, fieldSep)
		if len(f) < 8 {
			b.log.Warn("tmux: unparseable list-sessions line", "line", line)
			continue
		}
		wins, _ := strconv.Atoi(f[5])
		att, _ := strconv.Atoi(f[6])
		created, _ := strconv.ParseInt(f[7], 10, 64)
		sess := session.Session{
			ID: f[0], Name: f[1], HarnessName: f[2], Managed: f[3] == "1",
			Cwd: f[4], Windows: wins, Attached: att, Created: created,
		}
		if len(f) >= 11 {
			sess.ClaudeStatus = deriveClaudeStatus(f[8], f[10])
			if sess.ClaudeStatus != "" {
				if at, err := strconv.ParseInt(f[9], 10, 64); err == nil && at > 0 {
					sess.ClaudeStatusAt = at
				}
			}
		}
		if len(f) >= 12 {
			sess.RunAs = f[11]
		}
		sf := ""
		if len(f) >= 13 {
			sf = f[12]
		}
		list = append(list, sess)
		statusFiles = append(statusFiles, sf)
		panes = append(panes, "")
		if len(f) >= 11 {
			panes[len(panes)-1] = f[10]
		}
	}
	b.applyFileStatus(ctx, list, statusFiles, panes)
	b.maybeSweepStatusFiles(statusFiles)
	sort.SliceStable(list, func(i, j int) bool { return list[i].Created < list[j].Created })
	return list, nil
}

// applyFileStatus overrides the Claude status of run-as sessions that have a
// status file, reading all files in one host exec. Failures leave the
// tmux-derived status alone.
func (b *Backend) applyFileStatus(ctx context.Context, list []session.Session, files, panes []string) {
	var paths []string
	for i, p := range files {
		if p != "" && list[i].RunAs != "" {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return
	}
	dir, err := b.resolveStatusDir(ctx)
	if err != nil {
		b.log.Warn("resolving status dir failed", "err", err)
		return
	}
	got, err := b.readStatusFiles(ctx, dir, paths)
	if err != nil {
		b.log.Warn("reading run-as status files failed", "err", err)
		return
	}
	for i, p := range files {
		content, ok := got[p]
		if !ok {
			continue
		}
		state, at, _ := parseStatusFile(content)
		// A reported state is authoritative: the pane command of a run-as
		// session is the wrapper shell, not claude, so it can't veto it.
		// Without one (empty file after SessionEnd) fall back as usual.
		switch state {
		case session.ClaudeWorking, session.ClaudeWaiting, session.ClaudeIdle:
			list[i].ClaudeStatus, list[i].ClaudeStatusAt = state, at
		default:
			list[i].ClaudeStatus, list[i].ClaudeStatusAt = deriveClaudeStatus("", panes[i]), 0
		}
	}
}

// expandDir expands "~" against the host's home. Host paths are always POSIX
// (tmux hosts are macOS/Linux), so this uses path, not filepath, even when the
// desktop running the core is Windows.
func (b *Backend) expandDir(dir string) string {
	switch {
	case dir == "" || dir == "~":
		return b.home
	case strings.HasPrefix(dir, "~/"):
		return path.Join(b.home, dir[2:])
	}
	return dir
}

func (b *Backend) Create(ctx context.Context, opts session.CreateOptions) (session.Session, error) {
	if err := b.gate(); err != nil {
		return session.Session{}, err
	}
	switch opts.Command {
	case "", session.CommandShell, session.CommandClaude:
	default:
		return session.Session{}, fmt.Errorf("%w: unknown command: %s", session.ErrBadRequest, opts.Command)
	}
	if opts.RunAs != "" && !userNameRe.MatchString(opts.RunAs) {
		return session.Session{}, fmt.Errorf("%w: invalid user name: %s", session.ErrBadRequest, opts.RunAs)
	}
	dir := b.expandDir(opts.Dir)
	if opts.RunAs != "" {
		// The directory belongs to the target user, who may own paths the
		// login user can't read: start in the login user's home and cd later.
		dir = b.home
	} else {
		ok, err := b.ops.dirExists(ctx, dir)
		if err != nil {
			return session.Session{}, b.mapError(err, "")
		}
		if !ok {
			return session.Session{}, fmt.Errorf("%w: directory not found: %s", session.ErrBadRequest, dir)
		}
	}

	base := sanitizeName(opts.Name)
	if base == "" {
		base = defaultName(dir)
	}

	var id string
	for attempt := 0; ; attempt++ {
		existing, err := b.List(ctx)
		if err != nil {
			return session.Session{}, err
		}
		names := make(map[string]bool, len(existing))
		for _, s := range existing {
			names[s.Name] = true
		}
		out, err := b.run.Output(ctx, "new-session", "-d", "-s", uniqueName(base, names), "-c", dir, "-P", "-F", "#{session_id}")
		if err == nil {
			id = strings.TrimSpace(string(out))
			break
		}
		// Lost a race with another creator of the same name: pick again.
		if attempt < 3 && strings.Contains(err.Error(), "duplicate session") {
			continue
		}
		return session.Session{}, b.mapError(err, "")
	}

	fail := func(err error) (session.Session, error) {
		b.run.Output(context.WithoutCancel(ctx), "kill-session", "-t", id)
		return session.Session{}, err
	}
	steps := [][]string{
		{"set-option", "-t", id, "@harness_managed", "1"},
		{"set-option", "-t", id, "window-size", "latest"},
	}
	var statusFile, settingsFile string
	if opts.RunAs != "" {
		steps = append(steps, []string{"set-option", "-t", id, "@harness_run_as", opts.RunAs})
		// Best effort: without the status dir the session just has no status.
		if dir, err := b.statusSetup(ctx); err != nil {
			b.log.Warn("run-as Claude status unavailable", "err", err)
		} else {
			statusFile, settingsFile = dir+"/"+newStatusToken(), statusSettingsPath(dir)
			steps = append(steps, []string{"set-option", "-t", id, "@harness_status_file", statusFile})
		}
	}
	if opts.Name != "" {
		steps = append(steps, []string{"set-option", "-t", id, "@harness_name", opts.Name})
	}
	for _, args := range steps {
		if _, err := b.run.Output(ctx, args...); err != nil {
			return fail(err)
		}
	}
	// Wait for the shell to draw its prompt. Shells can discard keystrokes typed
	// while they start up (bash does on macOS), and until the shell is running
	// tmux may report the server's cwd instead of the pane's.
	b.waitShellReady(ctx, id)
	if opts.RunAs != "" {
		line := runAsCommand(b.sudo, opts.RunAs, opts.Dir, opts.Command, statusFile, settingsFile)
		if _, err := b.run.Output(ctx, "send-keys", "-t", id, "-l", line); err != nil {
			return fail(err)
		}
		if _, err := b.run.Output(ctx, "send-keys", "-t", id, "Enter"); err != nil {
			return fail(err)
		}
	} else if opts.Command == session.CommandClaude {
		// Typed into the shell rather than exec'd, so exiting Claude leaves a shell.
		if _, err := b.run.Output(ctx, "send-keys", "-t", id, "-l", "claude"); err != nil {
			return fail(err)
		}
		if _, err := b.run.Output(ctx, "send-keys", "-t", id, "Enter"); err != nil {
			return fail(err)
		}
	}

	b.kick()
	list, err := b.List(ctx)
	if err != nil {
		return session.Session{}, err
	}
	for _, s := range list {
		if s.ID == id {
			return s, nil
		}
	}
	return session.Session{}, fmt.Errorf("%w: %s", session.ErrNotFound, id)
}

var userNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9._-]{0,31}$`)

// runAsCommand builds the line typed into a run-as session's shell: sudo -iu
// <user> running a small script that cds to dir (interpreted as that user),
// optionally starts claude, then leaves the user in their login shell. Not
// exec'd, so a failing sudo leaves the login user's shell to retry in. sudo
// "" means plain "sudo" from the shell's PATH. With a statusFile the command
// runs under env HARNESS_STATUS_FILE=<file> so the hook script can report
// status, and claude is started with --settings <settingsPath>; both "" gives
// the plain line.
func runAsCommand(sudo, user, dir, command, statusFile, settingsPath string) string {
	inner := ""
	notFound := func(d string) string {
		return tmux.ShellQuote("harness: directory not found: " + d)
	}
	switch {
	case dir == "" || dir == "~":
	case strings.HasPrefix(dir, "~/"):
		inner += `cd "$HOME"/` + tmux.ShellQuote(dir[2:]) + " || echo " + notFound(dir) + " >&2; "
	default:
		inner += "cd " + tmux.ShellQuote(dir) + " || echo " + notFound(dir) + " >&2; "
	}
	if command == session.CommandClaude {
		inner += "claude"
		if statusFile != "" && settingsPath != "" {
			inner += " --settings " + tmux.ShellQuote(settingsPath)
		}
		inner += "; "
	}
	inner += `exec "$SHELL" -l`
	bin := "sudo"
	if sudo != "" {
		bin = tmux.ShellQuote(sudo)
	}
	env := ""
	if statusFile != "" {
		env = " env HARNESS_STATUS_FILE=" + tmux.ShellQuote(statusFile)
	}
	return bin + " -iu " + tmux.ShellQuote(user) + " --" + env + " sh -c " + tmux.ShellQuote(inner)
}

// shellReadyTimeout bounds waitShellReady; a shell with an empty prompt just
// costs this delay once.
var shellReadyTimeout = 5 * time.Second

// waitShellReady polls until the pane's cursor leaves the origin, i.e. the shell
// has printed its prompt, or the timeout passes. Best effort: errors are ignored.
func (b *Backend) waitShellReady(ctx context.Context, id string) {
	ctx, cancel := context.WithTimeout(ctx, shellReadyTimeout)
	defer cancel()
	t := time.NewTicker(25 * time.Millisecond)
	defer t.Stop()
	for {
		out, err := b.run.Output(ctx, "display", "-p", "-t", id, "#{cursor_x} #{cursor_y}")
		if err == nil && strings.TrimSpace(string(out)) != "0 0" {
			return
		}
		select {
		case <-ctx.Done():
			b.log.Debug("shell not ready before timeout", "session", id)
			return
		case <-t.C:
		}
	}
}

func (b *Backend) Kill(ctx context.Context, id string) error {
	if err := b.gate(); err != nil {
		return err
	}
	statusFile := ""
	if out, err := b.run.Output(ctx, "display-message", "-p", "-t", id, "#{@harness_status_file}"); err == nil {
		statusFile = strings.TrimSpace(string(out))
	}
	if _, err := b.run.Output(ctx, "kill-session", "-t", id); err != nil {
		return b.mapError(err, id)
	}
	b.removeStatusFile(ctx, statusFile)
	b.kick()
	return nil
}

func (b *Backend) Rename(ctx context.Context, id, name string) error {
	if err := b.gate(); err != nil {
		return err
	}
	args := []string{"set-option", "-t", id, "@harness_name", name}
	if name == "" {
		args = []string{"set-option", "-u", "-q", "-t", id, "@harness_name"}
	}
	if _, err := b.run.Output(ctx, args...); err != nil {
		return b.mapError(err, id)
	}
	b.kick()
	return nil
}

var (
	nameRe     = regexp.MustCompile(`[^A-Za-z0-9_-]`)
	dashRunsRe = regexp.MustCompile(`-{2,}`)
)

const maxNameLen = 40

// sanitizeName maps a display name to a legal tmux session name. It may
// return "".
func sanitizeName(s string) string {
	s = strings.Trim(dashRunsRe.ReplaceAllString(nameRe.ReplaceAllString(s, "-"), "-"), "-")
	if len(s) > maxNameLen {
		s = strings.Trim(s[:maxNameLen], "-")
	}
	return s
}

func defaultName(dir string) string {
	base := path.Base(path.Clean(dir))
	if base == "." || base == "/" {
		base = ""
	}
	base = sanitizeName(base)
	if base == "" {
		base = "home"
	}
	return sanitizeName("h-" + base)
}

// uniqueName returns base, or base-2, base-3, ... (kept within maxNameLen)
// so that it is not in taken.
func uniqueName(base string, taken map[string]bool) string {
	if !taken[base] {
		return base
	}
	for n := 2; ; n++ {
		suffix := "-" + strconv.Itoa(n)
		stem := base
		if len(stem)+len(suffix) > maxNameLen {
			stem = strings.TrimRight(stem[:maxNameLen-len(suffix)], "-")
		}
		if name := stem + suffix; !taken[name] {
			return name
		}
	}
}

// --- subscriptions ---

func (b *Backend) Subscribe(fn func([]session.Session)) (unsubscribe func()) {
	b.notifyMu.Lock()
	defer b.notifyMu.Unlock()

	b.subsMu.Lock()
	id := b.nextSub
	b.nextSub++
	b.subs[id] = fn
	if len(b.subs) == 1 && b.stopPoll == nil && !b.isClosed() && b.gate() == nil {
		b.stopPoll = make(chan struct{})
		go b.pollLoop(b.stopPoll)
	}
	b.subsMu.Unlock()

	list, err := b.List(context.Background())
	if err != nil {
		b.log.Debug("tmux: initial list failed", "err", err)
		list = []session.Session{}
	}
	if !b.haveLast || !reflect.DeepEqual(list, b.last) {
		b.last, b.haveLast = list, true
		b.fanout(list)
	} else {
		fn(list)
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			b.subsMu.Lock()
			defer b.subsMu.Unlock()
			delete(b.subs, id)
			if len(b.subs) == 0 && b.stopPoll != nil {
				close(b.stopPoll)
				b.stopPoll = nil
			}
		})
	}
}

func (b *Backend) fanout(list []session.Session) {
	b.subsMu.Lock()
	fns := make([]func([]session.Session), 0, len(b.subs))
	for _, fn := range b.subs {
		fns = append(fns, fn)
	}
	b.subsMu.Unlock()
	for _, fn := range fns {
		fn(list)
	}
}

// refresh lists sessions and notifies subscribers if anything changed.
func (b *Backend) refresh() {
	b.notifyMu.Lock()
	defer b.notifyMu.Unlock()
	list, err := b.List(context.Background())
	if err != nil {
		b.log.Debug("tmux: poll failed", "err", err)
		return
	}
	if b.haveLast && reflect.DeepEqual(list, b.last) {
		return
	}
	b.last, b.haveLast = list, true
	b.fanout(list)
}

// kick requests an immediate refresh from the poll goroutine (if running).
func (b *Backend) kick() {
	select {
	case b.kickCh <- struct{}{}:
	default:
	}
}

func (b *Backend) pollLoop(stop <-chan struct{}) {
	t := time.NewTicker(b.poll)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		case <-b.kickCh:
		}
		select {
		case <-stop:
			return
		default:
		}
		b.refresh()
	}
}

// --- lifecycle ---

func (b *Backend) isClosed() bool {
	b.termMu.Lock()
	defer b.termMu.Unlock()
	return b.closed
}

func (b *Backend) Close() error {
	b.termMu.Lock()
	b.closed = true
	terms := make([]*terminal, 0, len(b.terms))
	for t := range b.terms {
		terms = append(terms, t)
	}
	b.termMu.Unlock()

	b.subsMu.Lock()
	if b.stopPoll != nil {
		close(b.stopPoll)
		b.stopPoll = nil
	}
	b.subs = map[int]func([]session.Session){}
	b.subsMu.Unlock()

	lost := b.isLost()
	for _, t := range terms {
		if lost {
			// The connection dropped before Close: report that, not a plain close.
			t.mu.Lock()
			if t.err == nil && !t.closing {
				t.err = errConnLost
			}
			t.mu.Unlock()
		}
		t.Close()
	}
	return nil
}

func (b *Backend) isLost() bool {
	if b.lostCh == nil {
		return false
	}
	select {
	case <-b.lostCh:
		return true
	default:
	}
	select {
	case <-b.connDone:
		return true
	default:
		return false
	}
}

// onConnectionLost ends every open terminal with "connection lost" and stops
// polling. Later requests fail through gate.
func (b *Backend) onConnectionLost() {
	b.termMu.Lock()
	terms := make([]*terminal, 0, len(b.terms))
	for t := range b.terms {
		terms = append(terms, t)
	}
	b.termMu.Unlock()

	b.subsMu.Lock()
	if b.stopPoll != nil {
		close(b.stopPoll)
		b.stopPoll = nil
	}
	b.subsMu.Unlock()

	for _, t := range terms {
		t.mu.Lock()
		if t.err == nil && !t.closing {
			t.err = errConnLost
		}
		t.mu.Unlock()
		t.Close()
	}
}
