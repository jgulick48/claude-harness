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
	"path/filepath"
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
	Bin          string // tmux binary; default "tmux"
	Socket       string // tmux -L socket name; optional
	Config       string // tmux -f config file; optional
	Home         string // default os.UserHomeDir()
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
	poll     time.Duration
	log      *slog.Logger

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
		id: id, name: name, run: run,
		home: opts.Home, poll: opts.PollInterval, log: opts.Logger,
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
	h := session.Host{ID: b.id, Name: b.name, Home: b.home, State: session.HostConnected}
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
	if ctx.Err() == nil { // don't cache a probe that was cut short
		b.hostCache, b.hostAt = h, time.Now()
	}
	return h
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
}, fieldSep)

func (b *Backend) List(ctx context.Context) ([]session.Session, error) {
	out, err := b.run.Output(ctx, "list-sessions", "-F", listFormat)
	if err != nil {
		if isNoServer(err) {
			return []session.Session{}, nil
		}
		return nil, mapErr(err, "")
	}
	list := []session.Session{}
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
		list = append(list, session.Session{
			ID: f[0], Name: f[1], HarnessName: f[2], Managed: f[3] == "1",
			Cwd: f[4], Windows: wins, Attached: att, Created: created,
		})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Created < list[j].Created })
	return list, nil
}

func (b *Backend) expandDir(dir string) string {
	switch {
	case dir == "" || dir == "~":
		return b.home
	case strings.HasPrefix(dir, "~/"):
		return filepath.Join(b.home, dir[2:])
	}
	return dir
}

func (b *Backend) Create(ctx context.Context, opts session.CreateOptions) (session.Session, error) {
	switch opts.Command {
	case "", session.CommandShell, session.CommandClaude:
	default:
		return session.Session{}, fmt.Errorf("%w: unknown command: %s", session.ErrBadRequest, opts.Command)
	}
	dir := b.expandDir(opts.Dir)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return session.Session{}, fmt.Errorf("%w: directory not found: %s", session.ErrBadRequest, dir)
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
		return session.Session{}, mapErr(err, "")
	}

	fail := func(err error) (session.Session, error) {
		b.run.Output(context.WithoutCancel(ctx), "kill-session", "-t", id)
		return session.Session{}, err
	}
	steps := [][]string{
		{"set-option", "-t", id, "@harness_managed", "1"},
		{"set-option", "-t", id, "window-size", "latest"},
	}
	if opts.Name != "" {
		steps = append(steps, []string{"set-option", "-t", id, "@harness_name", opts.Name})
	}
	for _, args := range steps {
		if _, err := b.run.Output(ctx, args...); err != nil {
			return fail(err)
		}
	}
	if opts.Command == session.CommandClaude {
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

func (b *Backend) Kill(ctx context.Context, id string) error {
	if _, err := b.run.Output(ctx, "kill-session", "-t", id); err != nil {
		return mapErr(err, id)
	}
	b.kick()
	return nil
}

func (b *Backend) Rename(ctx context.Context, id, name string) error {
	args := []string{"set-option", "-t", id, "@harness_name", name}
	if name == "" {
		args = []string{"set-option", "-u", "-q", "-t", id, "@harness_name"}
	}
	if _, err := b.run.Output(ctx, args...); err != nil {
		return mapErr(err, id)
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
	base := filepath.Base(filepath.Clean(dir))
	if base == "." || base == string(filepath.Separator) {
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
	if len(b.subs) == 1 && b.stopPoll == nil && !b.isClosed() {
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

	for _, t := range terms {
		t.Close()
	}
	return nil
}
