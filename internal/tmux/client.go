package tmux

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// ErrClosed is returned for commands issued to (or pending on) a client whose
// tmux process has exited or that was closed.
var ErrClosed = errors.New("tmux control client closed")

// CommandError is a command that tmux answered with %error.
type CommandError struct{ Lines []string }

func (e *CommandError) Error() string { return strings.Join(e.Lines, "; ") }

type result struct {
	lines []string
	seq   uint64
	err   error
}

type call struct{ ch chan result } // ch is nil for fire-and-forget

// ControlClient drives a started `tmux -C` process.
type ControlClient struct {
	proc Proc
	log  *slog.Logger

	mu      sync.Mutex // guards pending, closed and writes to stdin
	pending []*call
	closed  bool

	// Notifications are queued without bound so the reader never blocks on a
	// slow consumer (which could otherwise deadlock a consumer that issues
	// commands while handling a notification).
	qmu   sync.Mutex
	queue []Notification
	qdone bool
	qwake chan struct{}
	notes chan Notification

	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}
	err      error // process exit error; valid once done is closed
}

// NewControlClient starts reading proc's output.
func NewControlClient(proc Proc, log *slog.Logger) *ControlClient {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	c := &ControlClient{
		proc:  proc,
		log:   log,
		qwake: make(chan struct{}, 1),
		notes: make(chan Notification),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	go c.readLoop()
	go c.pump()
	return c
}

// Notifications delivers notifications in arrival order (including %output and
// %exit). It is closed after the process exits and the queue drains, or after
// Close.
func (c *ControlClient) Notifications() <-chan Notification { return c.notes }

// Done is closed when the tmux process has exited.
func (c *ControlClient) Done() <-chan struct{} { return c.done }

// Err is the process exit error; valid after Done is closed.
func (c *ControlClient) Err() error { return c.err }

func (c *ControlClient) readLoop() {
	rd := NewReader(c.proc.Stdout())
	var seq uint64
	var rerr error
	for {
		m, err := rd.Next()
		if err != nil {
			rerr = err
			break
		}
		if m.Reply != nil {
			c.handleReply(m.Reply, seq)
			continue
		}
		seq++
		n := *m.Note
		n.Seq = seq
		c.push(n)
	}
	werr := c.proc.Wait()
	c.err = werr
	if c.err == nil && rerr != io.EOF {
		c.err = rerr
	}

	c.mu.Lock()
	c.closed = true
	pend := c.pending
	c.pending = nil
	c.mu.Unlock()
	for _, p := range pend {
		if p.ch != nil {
			p.ch <- result{err: ErrClosed}
		}
	}

	c.qmu.Lock()
	c.qdone = true
	c.qmu.Unlock()
	c.wake()
	close(c.done)
}

func (c *ControlClient) handleReply(r *Reply, seq uint64) {
	// tmux emits an unsolicited block (flags 0) right after attach; only
	// blocks with flags bit 0 answer commands we sent.
	if r.Flags&1 == 0 {
		return
	}
	c.mu.Lock()
	if len(c.pending) == 0 {
		c.mu.Unlock()
		c.log.Warn("tmux: reply with no pending command", "num", r.Num)
		return
	}
	p := c.pending[0]
	c.pending = c.pending[1:]
	c.mu.Unlock()

	var err error
	if r.Err {
		err = &CommandError{Lines: r.Lines}
	}
	if p.ch == nil {
		if err != nil {
			c.log.Warn("tmux command failed", "err", err)
		}
		return
	}
	p.ch <- result{lines: r.Lines, seq: seq, err: err}
}

func (c *ControlClient) push(n Notification) {
	c.qmu.Lock()
	c.queue = append(c.queue, n)
	c.qmu.Unlock()
	c.wake()
}

func (c *ControlClient) wake() {
	select {
	case c.qwake <- struct{}{}:
	default:
	}
}

func (c *ControlClient) pump() {
	defer close(c.notes)
	for {
		c.qmu.Lock()
		if len(c.queue) > 0 {
			n := c.queue[0]
			c.queue[0] = Notification{}
			c.queue = c.queue[1:]
			c.qmu.Unlock()
			select {
			case c.notes <- n:
			case <-c.stop:
				return
			}
			continue
		}
		finished := c.qdone
		c.qmu.Unlock()
		if finished {
			return
		}
		select {
		case <-c.qwake:
		case <-c.stop:
			return
		}
	}
}

func (c *ControlClient) submit(cmd string, p *call) error {
	if strings.ContainsAny(cmd, "\r\n") {
		return fmt.Errorf("tmux: command contains a newline: %q", cmd)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrClosed
	}
	c.pending = append(c.pending, p)
	if _, err := io.WriteString(c.proc.Stdin(), cmd+"\n"); err != nil {
		c.pending = c.pending[:len(c.pending)-1]
		return fmt.Errorf("%w: %v", ErrClosed, err)
	}
	return nil
}

// Do sends one command line and waits for its reply, returning the reply
// lines. A %error reply yields a *CommandError.
func (c *ControlClient) Do(ctx context.Context, cmd string) ([]string, error) {
	lines, _, err := c.DoSeq(ctx, cmd)
	return lines, err
}

// DoSeq is Do that also returns the Seq of the last notification that arrived
// before the reply. Notifications with Seq <= that value predate the command's
// effects.
func (c *ControlClient) DoSeq(ctx context.Context, cmd string) ([]string, uint64, error) {
	p := &call{ch: make(chan result, 1)}
	if err := c.submit(cmd, p); err != nil {
		return nil, 0, err
	}
	select {
	case r := <-p.ch:
		return r.lines, r.seq, r.err
	case <-ctx.Done():
		// The entry stays queued so reply matching remains FIFO-correct.
		return nil, 0, ctx.Err()
	}
}

// Send issues a command without waiting for the reply; failures are logged.
func (c *ControlClient) Send(cmd string) error {
	return c.submit(cmd, &call{})
}

// Close detaches the client (closing stdin), waits up to ~2s for the process
// to exit, then kills it.
func (c *ControlClient) Close() error {
	c.mu.Lock()
	c.closed = true
	c.proc.Stdin().Close()
	c.mu.Unlock()

	t := time.NewTimer(2 * time.Second)
	defer t.Stop()
	select {
	case <-c.done:
	case <-t.C:
		c.proc.Kill()
		select {
		case <-c.done:
		case <-time.After(2 * time.Second):
		}
	}
	c.stopOnce.Do(func() { close(c.stop) })
	return nil
}
