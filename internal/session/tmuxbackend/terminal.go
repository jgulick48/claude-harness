package tmuxbackend

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jgulick48/claude-harness/internal/session"
	"github.com/jgulick48/claude-harness/internal/tmux"
)

const (
	scrollback    = 2000
	writeChunk    = 256 // bytes of input per send-keys command
	outputBuffer  = 64
	commandTimout = 10 * time.Second
	paneFormat    = "#{session_id} #{pane_id} #{cursor_x} #{cursor_y} #{alternate_on}"
)

var (
	errSessionEnded = errors.New("session ended")
	errConnLost     = errors.New("connection lost")
)

// lostGrace is how long an SSH terminal whose stream died waits for the
// connection-lost signal before reporting "session ended".
const lostGrace = 3 * time.Second

type paneInfo struct {
	session string
	pane    string
	cx, cy  int
	alt     bool
}

type terminal struct {
	b  *Backend
	id string // session target as given by the caller
	cc *tmux.ControlClient

	out      chan []byte
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{} // closed by Close
	finished chan struct{} // closed when run has exited

	closeOnce sync.Once
	mu        sync.Mutex // guards the fields below
	pane      string
	sid       string // canonical session id, e.g. "$3"
	rows      int
	closing   bool
	sawExit   bool
	err       error
	writeMu   sync.Mutex // keeps multi-chunk writes ordered
}

func (b *Backend) Attach(ctx context.Context, id string, cols, rows int) (session.Terminal, error) {
	if cols <= 0 || rows <= 0 {
		return nil, fmt.Errorf("%w: invalid terminal size %dx%d", session.ErrBadRequest, cols, rows)
	}
	if err := b.gate(); err != nil {
		return nil, err
	}
	if b.isClosed() {
		return nil, fmt.Errorf("%w: backend closed", session.ErrUnavailable)
	}
	if _, err := b.run.Output(ctx, "has-session", "-t", id); err != nil {
		return nil, b.mapError(err, id)
	}
	proc, err := b.run.Start(ctx, "-C", "attach-session", "-t", id)
	if err != nil {
		return nil, b.mapError(err, id)
	}

	tctx, cancel := context.WithCancel(context.Background())
	t := &terminal{
		b: b, id: id, rows: rows,
		cc:  tmux.NewControlClient(proc, b.log),
		out: make(chan []byte, outputBuffer),
		ctx: tctx, cancel: cancel,
		done: make(chan struct{}), finished: make(chan struct{}),
	}

	dropUntil, err := t.init(ctx, cols, rows)
	if err != nil {
		cancel()
		t.cc.Close()
		if errors.Is(err, tmux.ErrClosed) { // session vanished between has-session and attach
			return nil, fmt.Errorf("%w: %s", session.ErrNotFound, id)
		}
		return nil, b.mapError(err, id)
	}

	b.termMu.Lock()
	if b.closed {
		b.termMu.Unlock()
		cancel()
		t.cc.Close()
		return nil, fmt.Errorf("%w: backend closed", session.ErrUnavailable)
	}
	b.terms[t] = struct{}{}
	b.termMu.Unlock()

	go t.run(dropUntil)
	return t, nil
}

// init sizes the client and queues the screen seed as the first Output item.
func (t *terminal) init(ctx context.Context, cols, rows int) (dropUntil uint64, err error) {
	if _, err := t.cc.Do(ctx, fmt.Sprintf("refresh-client -C %dx%d", cols, rows)); err != nil {
		return 0, err
	}
	if _, err := t.cc.Do(ctx, "set-option -t "+tmux.Quote(t.id)+" window-size latest"); err != nil {
		return 0, err
	}
	pi, seed, seq, err := t.snapshot(ctx, rows)
	if err != nil {
		return 0, err
	}
	t.mu.Lock()
	t.pane, t.sid = pi.pane, pi.session
	t.mu.Unlock()
	t.out <- seed // empty channel: cannot block
	return seq, nil
}

func (t *terminal) queryPane(ctx context.Context) (paneInfo, uint64, error) {
	lines, seq, err := t.cc.DoSeq(ctx, "display -p -t "+tmux.Quote(t.id)+" "+tmux.Quote(paneFormat))
	if err != nil {
		return paneInfo{}, 0, err
	}
	if len(lines) == 0 {
		return paneInfo{}, 0, errors.New("tmux: empty display reply")
	}
	f := strings.Fields(lines[0])
	if len(f) != 5 {
		return paneInfo{}, 0, fmt.Errorf("tmux: unexpected pane info %q", lines[0])
	}
	cx, err1 := strconv.Atoi(f[2])
	cy, err2 := strconv.Atoi(f[3])
	if err1 != nil || err2 != nil {
		return paneInfo{}, 0, fmt.Errorf("tmux: unexpected pane info %q", lines[0])
	}
	return paneInfo{session: f[0], pane: f[1], cx: cx, cy: cy, alt: f[4] == "1"}, seq, nil
}

// snapshot captures the active pane. The returned seq is the notification
// sequence number at the final reply: %output with Seq <= seq is already
// reflected in the capture and must be dropped.
func (t *terminal) snapshot(ctx context.Context, rows int) (pi paneInfo, seed []byte, seq uint64, err error) {
	for attempt := 0; ; attempt++ {
		pi, _, err := t.queryPane(ctx)
		if err != nil {
			return paneInfo{}, nil, 0, err
		}
		args := "capture-pane -p -e -N -S -" + strconv.Itoa(scrollback) + " -t " + tmux.Quote(pi.pane)
		if pi.alt {
			args = "capture-pane -p -e -N -t " + tmux.Quote(pi.pane)
		}
		lines, _, err := t.cc.DoSeq(ctx, args)
		if err != nil {
			return paneInfo{}, nil, 0, err
		}
		// Re-read the cursor after the capture so it matches the captured
		// screen; retry if the pane or screen mode changed meanwhile.
		after, seq, err := t.queryPane(ctx)
		if err != nil {
			return paneInfo{}, nil, 0, err
		}
		if (after.pane != pi.pane || after.alt != pi.alt) && attempt < 3 {
			continue
		}
		return pi, buildSeed(lines, after.alt, after.cx, after.cy, rows), seq, nil
	}
}

// buildSeed renders captured screen lines as terminal output that ends with
// exactly `rows` lines on screen (so the cursor position is relative to the
// visible screen) and the cursor at (cx, cy), both 0-based.
func buildSeed(lines []string, alt bool, cx, cy, rows int) []byte {
	for len(lines) < rows {
		lines = append(lines, "")
	}
	var sb strings.Builder
	if alt {
		sb.WriteString("\x1b[?1049h")
	}
	sb.WriteString(strings.Join(lines, "\x1b[0m\r\n"))
	sb.WriteString("\x1b[0m")
	if cy >= rows {
		cy = rows - 1
	}
	if cy < 0 {
		cy = 0
	}
	if cx < 0 {
		cx = 0
	}
	fmt.Fprintf(&sb, "\x1b[%d;%dH", cy+1, cx+1)
	return []byte(sb.String())
}

func (t *terminal) run(dropUntil uint64) {
	defer close(t.finished)
	defer func() {
		t.b.termMu.Lock()
		delete(t.b.terms, t)
		t.b.termMu.Unlock()
	}()
	defer close(t.out)
	defer t.cc.Close()
	defer func() { // runs first: decides Err before Output closes
		t.mu.Lock()
		abrupt := !t.closing && t.err == nil && !t.sawExit
		t.mu.Unlock()
		if abrupt && t.b.lostCh != nil {
			// The stream died without %exit: over SSH that is usually the
			// connection dropping, which may be reported a moment later.
			select {
			case <-t.b.lostCh:
			case <-time.After(lostGrace):
			case <-t.done:
			}
			if t.b.isLost() {
				t.mu.Lock()
				if t.err == nil {
					t.err = errConnLost
				}
				t.mu.Unlock()
			}
		}
		t.mu.Lock()
		if !t.closing && t.err == nil {
			t.err = errSessionEnded
		}
		t.mu.Unlock()
	}()

	notes := t.cc.Notifications()
	for {
		select {
		case <-t.done:
			return
		case n, ok := <-notes:
			if !ok {
				return
			}
			t.mu.Lock()
			pane := t.pane
			t.mu.Unlock()
			switch n.Name {
			case "output":
				// Output that arrived before the seed capture is already in
				// the seed.
				if n.Pane == pane && n.Seq > dropUntil {
					select {
					case t.out <- n.Data:
					case <-t.done:
						return
					}
				}
			case "exit":
				t.mu.Lock()
				t.sawExit = true
				t.mu.Unlock()
				return
			case "window-pane-changed", "session-window-changed", "layout-change":
				if n.Seq <= dropUntil {
					continue // predates the current seed
				}
				// layout-change follows every resize (ours included): tmux and
				// xterm.js re-wrap existing lines differently, and the shell
				// redraws its prompt on SIGWINCH, so without a fresh seed the
				// two screens drift apart and lines get overwritten.
				seq, ok := t.reseed(pane, n, n.Name == "layout-change")
				if !ok {
					return
				}
				if seq > dropUntil {
					dropUntil = seq
				}
			}
		}
	}
}

// reseed re-reads the active pane after a window/pane switch or a resize and
// sends a fresh full-screen seed: always when force (resize), otherwise only if
// the active pane changed. ok is false if the terminal ended.
func (t *terminal) reseed(cur string, n tmux.Notification, force bool) (seq uint64, ok bool) {
	t.mu.Lock()
	rows, sid := t.rows, t.sid
	t.mu.Unlock()
	if n.Name == "session-window-changed" {
		if id, _, _ := strings.Cut(n.Args, " "); id != sid {
			return 0, true // another session's window
		}
	}
	ctx, cancel := context.WithTimeout(t.ctx, commandTimout)
	defer cancel()
	pi, seed, seq, err := t.snapshot(ctx, rows)
	if err != nil {
		if t.ctx.Err() != nil || errors.Is(err, tmux.ErrClosed) {
			return 0, false
		}
		t.b.log.Warn("tmux: re-seed failed", "session", t.id, "err", err)
		return 0, true
	}
	if pi.pane == cur && !force {
		return 0, true // nothing emitted, so nothing to drop
	}
	t.mu.Lock()
	t.pane = pi.pane
	t.mu.Unlock()
	data := append([]byte("\x1b[?1049l\x1b[H\x1b[2J\x1b[3J"), seed...)
	select {
	case t.out <- data:
		return seq, true
	case <-t.done:
		return 0, false
	}
}

func (t *terminal) Output() <-chan []byte { return t.out }

func (t *terminal) Err() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.err
}

func (t *terminal) isClosed() bool {
	select {
	case <-t.done:
		return true
	default:
		return false
	}
}

func (t *terminal) Write(p []byte) error {
	if t.isClosed() {
		return errors.New("terminal closed")
	}
	t.mu.Lock()
	pane := t.pane
	t.mu.Unlock()
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	for _, cmd := range sendKeysCommands(pane, p) {
		if err := t.cc.Send(cmd); err != nil {
			return err
		}
	}
	return nil
}

// sendKeysCommands encodes p as send-keys -H commands of at most writeChunk
// input bytes each.
func sendKeysCommands(pane string, p []byte) []string {
	const hexdigits = "0123456789abcdef"
	var cmds []string
	for len(p) > 0 {
		n := min(len(p), writeChunk)
		var sb strings.Builder
		sb.WriteString("send-keys -t " + tmux.Quote(pane) + " -H")
		for _, c := range p[:n] {
			sb.WriteByte(' ')
			sb.WriteByte(hexdigits[c>>4])
			sb.WriteByte(hexdigits[c&15])
		}
		cmds = append(cmds, sb.String())
		p = p[n:]
	}
	return cmds
}

func (t *terminal) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return fmt.Errorf("%w: invalid terminal size %dx%d", session.ErrBadRequest, cols, rows)
	}
	if t.isClosed() {
		return errors.New("terminal closed")
	}
	// Record the size first: the layout-change this triggers re-seeds the
	// screen at t.rows, and may be handled before refresh-client returns.
	t.mu.Lock()
	t.rows = rows
	t.mu.Unlock()
	ctx, cancel := context.WithTimeout(t.ctx, commandTimout)
	defer cancel()
	if _, err := t.cc.Do(ctx, fmt.Sprintf("refresh-client -C %dx%d", cols, rows)); err != nil {
		return err
	}
	return nil
}

func (t *terminal) Close() error {
	t.closeOnce.Do(func() {
		t.mu.Lock()
		t.closing = true
		t.mu.Unlock()
		t.cancel()
		close(t.done)
	})
	<-t.finished
	return nil
}
