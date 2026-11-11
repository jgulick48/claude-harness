package sshconn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	defaultTimeout   = 15 * time.Second
	defaultKeepAlive = 15 * time.Second
)

// Client is a live SSH connection (through any ProxyJump hops).
type Client struct {
	hops []*ssh.Client // hops[len-1] is the final target
	log  *slog.Logger

	mu       sync.Mutex
	finished bool
	err      error
	done     chan struct{}
}

// Dial resolves target, connects through ProxyJump hops, verifies host keys
// against known_hosts, and authenticates. Errors include *UnknownHostKeyError,
// *HostKeyChangedError, and errors wrapping ErrNoAuthMethods.
func Dial(ctx context.Context, target string, opts Options) (*Client, error) {
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if opts.Timeout <= 0 {
		opts.Timeout = defaultTimeout
	}
	if opts.KeepAlive == 0 {
		opts.KeepAlive = defaultKeepAlive
	}
	cfg, err := loadConfig(opts)
	if err != nil {
		return nil, err
	}
	final, err := resolveWith(cfg, target)
	if err != nil {
		return nil, err
	}
	chain := make([]Resolved, 0, len(final.ProxyJump)+1)
	for _, h := range final.ProxyJump {
		hr, err := resolveWith(cfg, h)
		if err != nil {
			return nil, fmt.Errorf("proxy jump %q: %w", h, err)
		}
		chain = append(chain, hr)
	}
	chain = append(chain, final)

	c := &Client{log: log, done: make(chan struct{})}
	var prev *ssh.Client
	for _, r := range chain {
		cl, err := connectHop(ctx, prev, r, opts, log)
		if err != nil {
			c.closeHops()
			return nil, err
		}
		c.hops = append(c.hops, cl)
		prev = cl
	}
	go c.watch(opts)
	return c, nil
}

func hopAddr(r Resolved) string {
	return net.JoinHostPort(r.HostName, strconv.Itoa(r.Port))
}

// connectHop dials r directly (prev == nil) or through prev, then handshakes.
// The whole thing is bounded by opts.Timeout and ctx.
func connectHop(ctx context.Context, prev *ssh.Client, r Resolved, opts Options, log *slog.Logger) (*ssh.Client, error) {
	addr := hopAddr(r)
	db, err := loadKnownHosts(knownHostsPath(opts))
	if err != nil {
		return nil, err
	}
	auth := gatherAuth(r, opts, log)
	defer auth.close()
	if len(auth.signers) == 0 {
		return nil, auth.noMethodsErr(r)
	}
	cc := &ssh.ClientConfig{
		User:            r.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(auth.signers...)},
		HostKeyCallback: hostKeyCallback(db),
	}
	if algs := db.HostKeyAlgorithms(addr); len(algs) > 0 {
		cc.HostKeyAlgorithms = algs
	}

	hctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	type result struct {
		cl  *ssh.Client
		err error
	}
	var (
		cmu  sync.Mutex
		conn net.Conn
	)
	resCh := make(chan result, 1)
	go func() {
		var nc net.Conn
		var err error
		if prev == nil {
			var d net.Dialer
			nc, err = d.DialContext(hctx, "tcp", addr)
		} else {
			nc, err = prev.Dial("tcp", addr)
		}
		if err != nil {
			resCh <- result{err: fmt.Errorf("connect %s: %w", addr, err)}
			return
		}
		cmu.Lock()
		conn = nc
		cmu.Unlock()
		if hctx.Err() != nil {
			nc.Close()
			resCh <- result{err: hctx.Err()}
			return
		}
		sc, chans, reqs, err := ssh.NewClientConn(nc, addr, cc)
		if err != nil {
			nc.Close()
			resCh <- result{err: fmt.Errorf("ssh %s: %w", addr, err)}
			return
		}
		resCh <- result{cl: ssh.NewClient(sc, chans, reqs)}
	}()

	select {
	case res := <-resCh:
		return res.cl, res.err
	case <-hctx.Done():
		cmu.Lock()
		if conn != nil {
			conn.Close()
		}
		cmu.Unlock()
		// If the goroutine finished a connection concurrently, don't leak it.
		go func() {
			if res := <-resCh; res.cl != nil {
				res.cl.Close()
			}
		}()
		cause := hctx.Err()
		if ctx.Err() == nil && errors.Is(cause, context.DeadlineExceeded) {
			return nil, fmt.Errorf("connect %s: timed out after %s: %w", addr, opts.Timeout, cause)
		}
		return nil, fmt.Errorf("connect %s: %w", addr, cause)
	}
}

// watch runs keepalives and notices when the final hop ends.
func (c *Client) watch(opts Options) {
	final := c.hops[len(c.hops)-1]
	go func() {
		err := final.Wait()
		if err == nil || errors.Is(err, io.EOF) {
			err = errors.New("connection closed by remote")
		}
		c.finish(fmt.Errorf("%w: %w", ErrConnectionLost, err))
	}()
	if opts.KeepAlive < 0 {
		return
	}
	go c.keepAlive(final, opts.KeepAlive, opts.Timeout)
}

func (c *Client) keepAlive(cl *ssh.Client, every, timeout time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	fails := 0
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
		}
		res := make(chan error, 1)
		go func() {
			_, _, err := cl.SendRequest("keepalive@openssh.com", true, nil)
			res <- err
		}()
		tm := time.NewTimer(timeout)
		select {
		case <-c.done:
			tm.Stop()
			return
		case err := <-res:
			tm.Stop()
			if err == nil {
				fails = 0
				continue
			}
			fails++
			c.log.Debug("ssh keepalive failed", "err", err, "consecutive", fails)
			if fails >= 2 {
				c.finish(fmt.Errorf("%w: keepalive failed: %w", ErrConnectionLost, err))
				return
			}
		case <-tm.C:
			c.finish(fmt.Errorf("%w: keepalive timed out after %s", ErrConnectionLost, timeout))
			return
		}
	}
}

// finish records the outcome once, closes Done, and tears the hops down.
func (c *Client) finish(err error) {
	c.mu.Lock()
	if c.finished {
		c.mu.Unlock()
		return
	}
	c.finished = true
	c.err = err
	close(c.done)
	c.mu.Unlock()
	c.closeHops()
}

func (c *Client) closeHops() {
	for i := len(c.hops) - 1; i >= 0; i-- {
		_ = c.hops[i].Close()
	}
}

// SSH returns the underlying client for opening sessions.
func (c *Client) SSH() *ssh.Client { return c.hops[len(c.hops)-1] }

// Done is closed when the connection is lost (keepalive failure, EOF) or Close.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err reports why Done closed: nil after Close, otherwise the cause.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Close closes the connection and every hop.
func (c *Client) Close() error {
	c.finish(nil)
	return nil
}
