// Package sshtest runs an in-process SSH server for tests. Each "exec" request
// runs the command locally via Shell -c, so tests can drive a local, isolated
// tmux "over SSH" without sshd or real keys. Not for production use.
//
// Tests that need a real shell should skip on Windows.
package sshtest

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Options configures a test server.
type Options struct {
	// AuthorizedKeys may authenticate. Required (no password auth).
	AuthorizedKeys []ssh.PublicKey
	// Env is appended to os.Environ() for exec'd commands (e.g. SHELL=/bin/sh, HOME=...).
	Env []string
	// Shell runs exec'd commands as Shell -c <command>. Default /bin/sh.
	Shell string
	// AllowTCPForwarding enables direct-tcpip channels (for ProxyJump tests).
	AllowTCPForwarding bool
}

// Server is a running test SSH server on 127.0.0.1.
type Server struct {
	Addr    string // "127.0.0.1:<port>"
	Port    int
	HostKey ssh.PublicKey // the server's host key (ed25519, generated per server)

	opts Options
	ln   net.Listener
	cfg  *ssh.ServerConfig

	mu      sync.Mutex
	conns   map[net.Conn]struct{}
	closed  bool
	blocked bool
	wg      sync.WaitGroup
}

// Start starts a server; t.Cleanup stops it.
func Start(t testing.TB, opts Options) *Server {
	t.Helper()
	s, err := StartServer(opts)
	if err != nil {
		t.Fatalf("sshtest: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

// StartServer starts a server on 127.0.0.1 without needing a testing.TB. The
// caller must call Close.
func StartServer(opts Options) (*Server, error) {
	if opts.Shell == "" {
		opts.Shell = "/bin/sh"
	}
	hostSigner, err := GenerateKey()
	if err != nil {
		return nil, err
	}
	authorized := make([][]byte, len(opts.AuthorizedKeys))
	for i, k := range opts.AuthorizedKeys {
		authorized[i] = k.Marshal()
	}
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			kb := key.Marshal()
			for _, a := range authorized {
				if bytes.Equal(a, kb) {
					return &ssh.Permissions{}, nil
				}
			}
			return nil, errors.New("sshtest: key not authorized")
		},
	}
	cfg.AddHostKey(hostSigner)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	s := &Server{
		Addr:    ln.Addr().String(),
		Port:    port,
		HostKey: hostSigner.PublicKey(),
		opts:    opts,
		ln:      ln,
		cfg:     cfg,
		conns:   map[net.Conn]struct{}{},
	}
	s.wg.Add(1)
	go s.acceptLoop()
	return s, nil
}

// SetBlocked makes the server refuse (immediately close) new connections while
// true, without touching existing ones. Combine with DropConnections to keep a
// client offline.
func (s *Server) SetBlocked(b bool) {
	s.mu.Lock()
	s.blocked = b
	s.mu.Unlock()
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		nc, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			nc.Close()
			return
		}
		if s.blocked {
			s.mu.Unlock()
			nc.Close()
			continue
		}
		s.conns[nc] = struct{}{}
		s.mu.Unlock()
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.serveConn(nc)
			nc.Close()
			s.mu.Lock()
			delete(s.conns, nc)
			s.mu.Unlock()
		}()
	}
}

// DropConnections abruptly closes every open client connection (simulates a
// network drop) while the server keeps listening.
func (s *Server) DropConnections() {
	s.mu.Lock()
	conns := make([]net.Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		c.Close()
	}
}

// Close stops listening and drops all connections.
func (s *Server) Close() {
	s.mu.Lock()
	already := s.closed
	s.closed = true
	s.mu.Unlock()
	if !already {
		s.ln.Close()
	}
	s.DropConnections()
	s.wg.Wait()
}

func (s *Server) serveConn(nc net.Conn) {
	sc, chans, reqs, err := ssh.NewServerConn(nc, s.cfg)
	if err != nil {
		return
	}
	defer sc.Close()
	go func() {
		for r := range reqs {
			if r.WantReply {
				_ = r.Reply(r.Type == "keepalive@openssh.com", nil)
			}
		}
	}()
	var wg sync.WaitGroup
	for nch := range chans {
		switch nch.ChannelType() {
		case "session":
			ch, creqs, err := nch.Accept()
			if err != nil {
				continue
			}
			wg.Add(1)
			go func() { defer wg.Done(); s.session(ch, creqs) }()
		case "direct-tcpip":
			if !s.opts.AllowTCPForwarding {
				_ = nch.Reject(ssh.Prohibited, "tcp forwarding disabled")
				continue
			}
			wg.Add(1)
			go func() { defer wg.Done(); s.directTCPIP(nch) }()
		default:
			_ = nch.Reject(ssh.UnknownChannelType, "unsupported channel type")
		}
	}
	sc.Close()
	wg.Wait()
}

func (s *Server) directTCPIP(nch ssh.NewChannel) {
	var p struct {
		Host     string
		Port     uint32
		OrigAddr string
		OrigPort uint32
	}
	if err := ssh.Unmarshal(nch.ExtraData(), &p); err != nil {
		_ = nch.Reject(ssh.ConnectionFailed, "bad payload")
		return
	}
	target, err := net.DialTimeout("tcp", net.JoinHostPort(p.Host, itoa(p.Port)), 5*time.Second)
	if err != nil {
		_ = nch.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	ch, reqs, err := nch.Accept()
	if err != nil {
		target.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(target, ch)
		if tc, ok := target.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(ch, target)
		_ = ch.CloseWrite()
	}()
	wg.Wait()
	target.Close()
	ch.Close()
}

func itoa(u uint32) string {
	var b [10]byte
	i := len(b)
	for {
		i--
		b[i] = byte('0' + u%10)
		u /= 10
		if u == 0 {
			break
		}
	}
	return string(b[i:])
}

func (s *Server) session(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	var env []string
	var cmd *exec.Cmd
	exited := make(chan struct{})
	for r := range reqs {
		switch r.Type {
		case "env":
			var kv struct{ Name, Value string }
			if ssh.Unmarshal(r.Payload, &kv) == nil {
				env = append(env, kv.Name+"="+kv.Value)
			}
			if r.WantReply {
				_ = r.Reply(true, nil)
			}
		case "exec":
			var p struct{ Command string }
			if cmd != nil || ssh.Unmarshal(r.Payload, &p) != nil {
				if r.WantReply {
					_ = r.Reply(false, nil)
				}
				continue
			}
			cmd = exec.Command(s.opts.Shell, "-c", p.Command)
			// Drop the test process's TMUX/TMUX_PANE: a real sshd wouldn't pass them,
			// and with TMUX set a tmux command without -L/-S targets the user's
			// own tmux server.
			cmd.Env = append(append(withoutTmux(os.Environ()), s.opts.Env...), env...)
			cmd.Stdout = ch
			cmd.Stderr = ch.Stderr()
			cmd.WaitDelay = 2 * time.Second
			setProcessGroup(cmd)
			stdin, err := cmd.StdinPipe()
			if err == nil {
				err = cmd.Start()
			}
			if err != nil {
				if r.WantReply {
					_ = r.Reply(false, nil)
				}
				cmd = nil
				continue
			}
			if r.WantReply {
				_ = r.Reply(true, nil)
			}
			go func() {
				_, _ = io.Copy(stdin, ch)
				_ = stdin.Close()
			}()
			c := cmd
			go func() {
				defer close(exited)
				err := c.Wait()
				code := 0
				if err != nil {
					code = 255
					var ee *exec.ExitError
					if errors.As(err, &ee) && ee.ExitCode() >= 0 {
						code = ee.ExitCode()
					}
				}
				_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(code)}))
				ch.Close()
			}()
		default: // pty-req, shell, subsystem, ...
			if r.WantReply {
				_ = r.Reply(false, nil)
			}
		}
	}
	// The client closed the channel (or the connection dropped): kill the command.
	if cmd != nil {
		select {
		case <-exited:
		default:
			killProcess(cmd)
			<-exited
		}
	}
}

// GenerateKey returns a fresh ed25519 signer.
func GenerateKey() (ssh.Signer, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, fmt.Errorf("signer: %w", err)
	}
	return s, nil
}

// NewKey returns a fresh ed25519 signer for tests.
func NewKey(t testing.TB) ssh.Signer {
	t.Helper()
	s, err := GenerateKey()
	if err != nil {
		t.Fatalf("sshtest: %v", err)
	}
	return s
}

// StartAgent serves an in-memory ssh-agent holding signers on a unix socket in
// a short temp dir and returns the socket path (unix only).
func StartAgent(t testing.TB, signers ...ssh.Signer) string {
	t.Helper()
	sock, stop, err := ServeAgent("", signers...)
	if err != nil {
		t.Fatalf("sshtest: %v", err)
	}
	t.Cleanup(stop)
	return sock
}

// ServeAgent serves an in-memory ssh-agent holding signers on a unix socket in
// a fresh short temp dir under dir ("" means the default temp dir). stop closes
// the agent and removes the socket's directory.
func ServeAgent(dir string, signers ...ssh.Signer) (socketPath string, stop func(), err error) {
	d, err := os.MkdirTemp(dir, "sa")
	if err != nil {
		return "", nil, fmt.Errorf("tempdir: %w", err)
	}
	sock := d + "/a.sock"
	ln, err := net.Listen("unix", sock)
	if err != nil {
		os.RemoveAll(d)
		return "", nil, fmt.Errorf("agent listen: %w", err)
	}
	ag := &signerAgent{signers: signers}
	var (
		mu    sync.Mutex
		conns []net.Conn
		wg    sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
			wg.Add(1)
			go func() { defer wg.Done(); _ = agent.ServeAgent(ag, c); c.Close() }()
		}
	}()
	stop = func() {
		ln.Close()
		mu.Lock()
		for _, c := range conns {
			c.Close()
		}
		mu.Unlock()
		wg.Wait()
		os.RemoveAll(d)
	}
	return sock, stop, nil
}

// signerAgent is a read-only agent.Agent over ssh.Signers. (agent.NewKeyring
// needs raw private keys, which a Signer does not expose.)
type signerAgent struct{ signers []ssh.Signer }

var errReadOnly = errors.New("sshtest: agent is read-only")

func (a *signerAgent) List() ([]*agent.Key, error) {
	keys := make([]*agent.Key, 0, len(a.signers))
	for _, s := range a.signers {
		pk := s.PublicKey()
		keys = append(keys, &agent.Key{Format: pk.Type(), Blob: pk.Marshal(), Comment: "sshtest"})
	}
	return keys, nil
}

func (a *signerAgent) Sign(key ssh.PublicKey, data []byte) (*ssh.Signature, error) {
	kb := key.Marshal()
	for _, s := range a.signers {
		if bytes.Equal(s.PublicKey().Marshal(), kb) {
			return s.Sign(rand.Reader, data)
		}
	}
	return nil, errors.New("sshtest: unknown key")
}

func (a *signerAgent) Signers() ([]ssh.Signer, error) { return a.signers, nil }
func (a *signerAgent) Add(agent.AddedKey) error       { return errReadOnly }
func (a *signerAgent) Remove(ssh.PublicKey) error     { return errReadOnly }
func (a *signerAgent) RemoveAll() error               { return errReadOnly }
func (a *signerAgent) Lock([]byte) error              { return errReadOnly }
func (a *signerAgent) Unlock([]byte) error            { return errReadOnly }

func withoutTmux(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_PANE=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}
