package sshconn

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/jgulick48/claude-harness/internal/sshconn/sshtest"
)

func skipWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs /bin/sh and unix agent")
	}
}

// env is an isolated test environment: temp HOME, config, known_hosts, agent.
type env struct {
	dir       string
	cfgPath   string
	khPath    string
	signer    ssh.Signer
	agentSock string
	srv       *sshtest.Server
	opts      Options
}

func newEnv(t *testing.T, cfg string) *env {
	t.Helper()
	skipWindows(t)
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("SSH_AUTH_SOCK", "")
	e := &env{dir: dir, cfgPath: filepath.Join(dir, "config"), khPath: filepath.Join(dir, "known_hosts")}
	e.signer = sshtest.NewKey(t)
	e.agentSock = sshtest.StartAgent(t, e.signer)
	e.srv = sshtest.Start(t, sshtest.Options{
		AuthorizedKeys:     []ssh.PublicKey{e.signer.PublicKey()},
		AllowTCPForwarding: true,
	})
	if cfg != "" {
		cfg = strings.ReplaceAll(cfg, "{{PORT}}", fmt.Sprint(e.srv.Port))
		if err := os.WriteFile(e.cfgPath, []byte(cfg), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	e.opts = Options{
		ConfigFile:     e.cfgPath,
		KnownHostsFile: e.khPath,
		AgentSocket:    e.agentSock,
		Timeout:        5 * time.Second,
		KeepAlive:      -1,
	}
	return e
}

func (e *env) addr() string { return fmt.Sprintf("[127.0.0.1]:%d", e.srv.Port) }
func (e *env) target() string {
	return fmt.Sprintf("tester@127.0.0.1:%d", e.srv.Port)
}
func (e *env) trust(t *testing.T) {
	t.Helper()
	if err := TrustHostKey(e.khPath, e.addr(), e.srv.HostKey); err != nil {
		t.Fatal(err)
	}
}

func (e *env) dial(t *testing.T) *Client {
	t.Helper()
	e.trust(t)
	c, err := Dial(context.Background(), e.target(), e.opts)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func writeKey(t *testing.T, path string, pass string) ssh.Signer {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_ = pub
	var blk *pem.Block
	if pass == "" {
		blk, err = ssh.MarshalPrivateKey(priv, "")
	} else {
		blk, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(pass))
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(blk), 0o600); err != nil {
		t.Fatal(err)
	}
	s, _ := ssh.NewSignerFromKey(priv)
	return s
}

func TestResolve(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	cfg := filepath.Join(dir, "config")
	os.WriteFile(cfg, []byte(`
Host web
  HostName web.example.com
  User deploy
  Port 2200
  IdentityFile ~/.ssh/web_key
  IdentityFile %d/.ssh/%h_%r
  ProxyJump bastion, ops@jump2:2022

Host bastion
  HostName bastion.example.com
  User jumper

Host nojump
  ProxyJump none

Host *
  User fallback
  ProxyJump gw
`), 0o600)
	opts := Options{ConfigFile: cfg}

	r, err := Resolve("web", opts)
	if err != nil {
		t.Fatal(err)
	}
	if r.HostName != "web.example.com" || r.User != "deploy" || r.Port != 2200 {
		t.Errorf("bad resolve: %+v", r)
	}
	wantIDs := []string{filepath.Join(dir, ".ssh/web_key"), filepath.Join(dir, ".ssh", "web.example.com_deploy")}
	if fmt.Sprint(r.IdentityFiles) != fmt.Sprint(wantIDs) {
		t.Errorf("identity files %v want %v", r.IdentityFiles, wantIDs)
	}
	if fmt.Sprint(r.ProxyJump) != "[bastion ops@jump2:2022]" {
		t.Errorf("proxyjump %v", r.ProxyJump)
	}

	r, _ = Resolve("alice@web:99", opts)
	if r.User != "alice" || r.Port != 99 || r.HostName != "web.example.com" {
		t.Errorf("override: %+v", r)
	}

	r, _ = Resolve("nojump", opts)
	if len(r.ProxyJump) != 0 || r.User != "fallback" || r.Port != 22 || r.HostName != "nojump" {
		t.Errorf("nojump: %+v", r)
	}

	r, _ = Resolve("plain.example.org", Options{ConfigFile: filepath.Join(dir, "missing")})
	if r.User == "" || r.Port != 22 || r.HostName != "plain.example.org" || len(r.ProxyJump) != 0 {
		t.Errorf("defaults: %+v", r)
	}

	r, _ = Resolve("bob@[::1]:2222", Options{ConfigFile: filepath.Join(dir, "missing")})
	if r.User != "bob" || r.HostName != "::1" || r.Port != 2222 {
		t.Errorf("ipv6: %+v", r)
	}
	if _, err := Resolve("host:notaport", Options{ConfigFile: filepath.Join(dir, "missing")}); err == nil {
		t.Error("expected bad port error")
	}
}

func TestUnknownThenTrustThenDial(t *testing.T) {
	e := newEnv(t, "")
	_, err := Dial(context.Background(), e.target(), e.opts)
	var uk *UnknownHostKeyError
	if !errors.As(err, &uk) {
		t.Fatalf("want UnknownHostKeyError, got %v", err)
	}
	if uk.Fingerprint != ssh.FingerprintSHA256(e.srv.HostKey) || uk.KeyType != "ssh-ed25519" || uk.Address != e.addr() {
		t.Errorf("bad error: %+v", uk)
	}
	if err := TrustHostKey(e.khPath, uk.Address, uk.Key); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(e.khPath)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("known_hosts mode %v", st.Mode().Perm())
	}
	c, err := Dial(context.Background(), e.target(), e.opts)
	if err != nil {
		t.Fatalf("dial after trust: %v", err)
	}
	c.Close()
}

func TestTrustHostKeyCreatesDir(t *testing.T) {
	skipWindows(t)
	p := filepath.Join(t.TempDir(), "sub", "known_hosts")
	if err := TrustHostKey(p, "example.com", sshtest.NewKey(t).PublicKey()); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(filepath.Dir(p))
	if st.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", st.Mode().Perm())
	}
}

func TestHostKeyChanged(t *testing.T) {
	e := newEnv(t, "")
	if err := TrustHostKey(e.khPath, e.addr(), sshtest.NewKey(t).PublicKey()); err != nil {
		t.Fatal(err)
	}
	_, err := Dial(context.Background(), e.target(), e.opts)
	var ch *HostKeyChangedError
	if !errors.As(err, &ch) {
		t.Fatalf("want HostKeyChangedError, got %v", err)
	}
	if ch.Address != e.addr() || ch.Fingerprint != ssh.FingerprintSHA256(e.srv.HostKey) {
		t.Errorf("bad: %+v", ch)
	}
}

func TestAliasUsesResolvedHostName(t *testing.T) {
	e := newEnv(t, "Host myalias\n  HostName 127.0.0.1\n  Port {{PORT}}\n  User tester\n")
	e.trust(t)
	c, err := Dial(context.Background(), "myalias", e.opts)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

func TestIdentityFileAuth(t *testing.T) {
	e := newEnv(t, "")
	key := writeKey(t, filepath.Join(e.dir, "mykey"), "")
	e.srv.Close()
	e.srv = sshtest.Start(t, sshtest.Options{AuthorizedKeys: []ssh.PublicKey{key.PublicKey()}})
	e.opts.AgentSocket = ""
	e.opts.IdentityFiles = []string{filepath.Join(e.dir, "mykey")}
	c := e.dial(t)
	_ = c
}

func TestConfigIdentityFileAuth(t *testing.T) {
	e := newEnv(t, "")
	key := writeKey(t, filepath.Join(e.dir, ".ssh", "id_ed25519"), "") // default path under temp HOME
	e.srv.Close()
	e.srv = sshtest.Start(t, sshtest.Options{AuthorizedKeys: []ssh.PublicKey{key.PublicKey()}})
	e.opts.AgentSocket = ""
	e.dial(t)
}

func TestPassphraseKeySkipped(t *testing.T) {
	e := newEnv(t, "")
	writeKey(t, filepath.Join(e.dir, "locked"), "secret")
	e.opts.AgentSocket = ""
	e.opts.IdentityFiles = []string{filepath.Join(e.dir, "locked")}
	e.trust(t)
	_, err := Dial(context.Background(), e.target(), e.opts)
	if !errors.Is(err, ErrNoAuthMethods) {
		t.Fatalf("want ErrNoAuthMethods, got %v", err)
	}
	if !strings.Contains(err.Error(), "locked") || !strings.Contains(err.Error(), "ssh-add") {
		t.Errorf("message lacks hints: %v", err)
	}
}

func TestNoAuthMethods(t *testing.T) {
	e := newEnv(t, "")
	e.opts.AgentSocket = ""
	e.trust(t)
	_, err := Dial(context.Background(), e.target(), e.opts)
	if !errors.Is(err, ErrNoAuthMethods) {
		t.Fatalf("got %v", err)
	}
}

func TestAgentAuthWrongKeyRejected(t *testing.T) {
	e := newEnv(t, "")
	e.opts.AgentSocket = sshtest.StartAgent(t, sshtest.NewKey(t))
	e.trust(t)
	if _, err := Dial(context.Background(), e.target(), e.opts); err == nil {
		t.Fatal("expected auth failure")
	}
}

func TestExecRoundTrip(t *testing.T) {
	e := newEnv(t, "")
	c := e.dial(t)
	s, err := c.SSH().NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var out, errb bytes.Buffer
	s.Stdout, s.Stderr = &out, &errb
	err = s.Run("echo hello; echo oops >&2; exit 3")
	var ee *ssh.ExitError
	if !errors.As(err, &ee) || ee.ExitStatus() != 3 {
		t.Fatalf("want exit 3, got %v", err)
	}
	if out.String() != "hello\n" || errb.String() != "oops\n" {
		t.Errorf("out=%q err=%q", out.String(), errb.String())
	}
}

func TestLongLivedSessionWithStdin(t *testing.T) {
	e := newEnv(t, "")
	c := e.dial(t)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ { // concurrent sessions
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := c.SSH().NewSession()
			if err != nil {
				t.Error(err)
				return
			}
			defer s.Close()
			in, _ := s.StdinPipe()
			out, _ := s.StdoutPipe()
			if err := s.Start("cat"); err != nil {
				t.Error(err)
				return
			}
			time.Sleep(100 * time.Millisecond)
			io.WriteString(in, "ping\n")
			buf := make([]byte, 5)
			if _, err := io.ReadFull(out, buf); err != nil || string(buf) != "ping\n" {
				t.Errorf("read %q %v", buf, err)
			}
			in.Close()
			if err := s.Wait(); err != nil {
				t.Errorf("wait: %v", err)
			}
		}()
	}
	wg.Wait()
}

func TestClientCloseKillsProcess(t *testing.T) {
	e := newEnv(t, "")
	c := e.dial(t)
	marker := filepath.Join(e.dir, "alive")
	s, _ := c.SSH().NewSession()
	if err := s.Start("while true; do echo x > " + marker + "; sleep 0.05; done"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	s.Close()
	time.Sleep(300 * time.Millisecond)
	os.Remove(marker)
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Error("process still running after channel close")
	}
}

func TestProxyJump(t *testing.T) {
	e := newEnv(t, "")
	// target server (second) + jump = e.srv
	target := sshtest.Start(t, sshtest.Options{AuthorizedKeys: []ssh.PublicKey{e.signer.PublicKey()}})
	cfg := fmt.Sprintf("Host dest\n  HostName 127.0.0.1\n  Port %d\n  User tester\n  ProxyJump jumper\nHost jumper\n  HostName 127.0.0.1\n  Port %d\n  User tester\n",
		target.Port, e.srv.Port)
	os.WriteFile(e.cfgPath, []byte(cfg), 0o600)

	// Unknown key on the jump hop is reported with the hop's address.
	_, err := Dial(context.Background(), "dest", e.opts)
	var uk *UnknownHostKeyError
	if !errors.As(err, &uk) || uk.Address != e.addr() {
		t.Fatalf("want unknown key for jump hop %s, got %v", e.addr(), err)
	}
	if err := TrustHostKey(e.khPath, uk.Address, uk.Key); err != nil {
		t.Fatal(err)
	}
	// Now the final hop is unknown.
	_, err = Dial(context.Background(), "dest", e.opts)
	if !errors.As(err, &uk) || uk.Address != fmt.Sprintf("[127.0.0.1]:%d", target.Port) {
		t.Fatalf("want unknown key for target, got %v", err)
	}
	TrustHostKey(e.khPath, uk.Address, uk.Key)

	c, err := Dial(context.Background(), "dest", e.opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.hops) != 2 {
		t.Fatalf("hops=%d", len(c.hops))
	}
	s, _ := c.SSH().NewSession()
	out, err := s.Output("echo via-jump")
	s.Close()
	if err != nil || string(out) != "via-jump\n" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	c.Close()
	select {
	case <-c.Done():
	default:
		t.Error("Done not closed")
	}
	if c.Err() != nil {
		t.Errorf("Err after Close: %v", c.Err())
	}
}

func TestDropConnections(t *testing.T) {
	e := newEnv(t, "")
	c := e.dial(t)
	e.srv.DropConnections()
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done not closed after drop")
	}
	if err := c.Err(); err == nil || !strings.Contains(err.Error(), "connection lost") {
		t.Errorf("Err=%v", err)
	}
	// Server still accepts new connections.
	c2, err := Dial(context.Background(), e.target(), e.opts)
	if err != nil {
		t.Fatalf("redial: %v", err)
	}
	c2.Close()
}

func TestKeepAliveWorksAndCloseIdempotent(t *testing.T) {
	e := newEnv(t, "")
	e.opts.KeepAlive = 50 * time.Millisecond
	c := e.dial(t)
	time.Sleep(300 * time.Millisecond)
	select {
	case <-c.Done():
		t.Fatalf("keepalive killed healthy connection: %v", c.Err())
	default:
	}
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); c.Close(); _ = c.Err() }()
	}
	wg.Wait()
	<-c.Done()
	if c.Err() != nil {
		t.Errorf("Err after Close: %v", c.Err())
	}
}

func TestContextCancelDuringDial(t *testing.T) {
	e := newEnv(t, "")
	e.trust(t)
	// A listener that accepts but never speaks SSH: handshake hangs.
	ln, err := newBlackhole(t)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	start := time.Now()
	_, err = Dial(ctx, "tester@"+ln, e.opts)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Error("cancel took too long")
	}
}

func TestHandshakeTimeout(t *testing.T) {
	e := newEnv(t, "")
	ln, _ := newBlackhole(t)
	e.opts.Timeout = 300 * time.Millisecond
	_, err := Dial(context.Background(), "tester@"+ln, e.opts)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("want timeout, got %v", err)
	}
}
