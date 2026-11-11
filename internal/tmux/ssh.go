package tmux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

// SSHRunner runs tmux on a remote host over an existing SSH connection. Each
// call opens its own ssh.Session. The caller owns Client.
type SSHRunner struct {
	Client *ssh.Client
	Bin    string // tmux path on the host; default "tmux"
	Socket string // -L socket name; optional
	Config string // -f config file; optional
}

func (r *SSHRunner) cmdline(args []string) string {
	bin := r.Bin
	if bin == "" {
		bin = "tmux"
	}
	parts := []string{ShellQuote(bin)}
	if r.Socket != "" {
		parts = append(parts, "-L", ShellQuote(r.Socket))
	}
	if r.Config != "" {
		parts = append(parts, "-f", ShellQuote(r.Config))
	}
	for _, a := range args {
		parts = append(parts, ShellQuote(a))
	}
	return strings.Join(parts, " ")
}

// watch closes sess when ctx is cancelled. The returned stop must be called
// once the session is finished.
func watch(ctx context.Context, sess *ssh.Session) (stop func()) {
	done := make(chan struct{})
	var once sync.Once
	go func() {
		select {
		case <-ctx.Done():
			sess.Close()
		case <-done:
		}
	}()
	return func() { once.Do(func() { close(done) }) }
}

func (r *SSHRunner) run(ctx context.Context, cmdline string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sess, err := r.Client.NewSession()
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	var stdout, stderr bytes.Buffer
	sess.Stdout = &stdout
	sess.Stderr = &stderr
	stop := watch(ctx, sess)
	defer stop()
	if err := sess.Run(cmdline); err != nil {
		if ctx.Err() != nil {
			return stdout.Bytes(), ctx.Err()
		}
		var exit *ssh.ExitError
		if errors.As(err, &exit) {
			return stdout.Bytes(), &Error{Stderr: strings.TrimSpace(stderr.String()), Err: err}
		}
		return stdout.Bytes(), err
	}
	return stdout.Bytes(), nil
}

// Output implements Runner.
func (r *SSHRunner) Output(ctx context.Context, args ...string) ([]byte, error) {
	return r.run(ctx, r.cmdline(args))
}

// Exec runs a raw command line on the host (not through tmux) and returns its
// stdout. A non-zero exit is an *Error wrapping *ssh.ExitError.
func (r *SSHRunner) Exec(ctx context.Context, cmdline string) ([]byte, error) {
	return r.run(ctx, cmdline)
}

// Start implements Runner.
func (r *SSHRunner) Start(ctx context.Context, args ...string) (Proc, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sess, err := r.Client.NewSession()
	if err != nil {
		return nil, err
	}
	p := &sshProc{sess: sess}
	if p.stdin, err = sess.StdinPipe(); err != nil {
		sess.Close()
		return nil, err
	}
	if p.stdout, err = sess.StdoutPipe(); err != nil {
		sess.Close()
		return nil, err
	}
	sess.Stderr = &p.stderr
	stop := watch(ctx, sess) // the context bounds the launch only
	err = sess.Start(r.cmdline(args))
	stop()
	if err != nil {
		sess.Close()
		return nil, fmt.Errorf("start tmux: %w", err)
	}
	return p, nil
}

type sshProc struct {
	sess   *ssh.Session
	stdin  io.WriteCloser
	stdout io.Reader
	stderr syncBuffer
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func (p *sshProc) Stdin() io.WriteCloser { return p.stdin }
func (p *sshProc) Stdout() io.Reader     { return p.stdout }

func (p *sshProc) Wait() error {
	if err := p.sess.Wait(); err != nil {
		return &Error{Stderr: strings.TrimSpace(p.stderr.String()), Err: err}
	}
	return nil
}

// Kill sends SIGKILL if the server supports signals, then closes the session.
func (p *sshProc) Kill() error {
	p.sess.Signal(ssh.SIGKILL)
	err := p.sess.Close()
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}
