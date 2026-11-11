// Command sshtestd runs the in-process test SSH server and an ssh-agent as a
// standalone helper for end-to-end tests. Test tooling only.
//
// Usage: sshtestd -home DIR [-tmux-tmpdir DIR]
//
// It prints one JSON line {"addr","port","hostKeyFingerprint","agentSocket"}
// on stdout, then reads commands from stdin:
//
//	drop     abruptly close every open client connection
//	block    refuse new connections
//	unblock  accept new connections again
//
// It exits (cleaning up) on stdin EOF, SIGINT or SIGTERM.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/crypto/ssh"

	"github.com/jgulick48/claude-harness/internal/sshconn/sshtest"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "sshtestd:", err)
		os.Exit(1)
	}
}

func run() error {
	home := flag.String("home", "", "HOME for commands run by the server (required)")
	tmuxTmp := flag.String("tmux-tmpdir", "", "TMUX_TMPDIR for commands run by the server (default: -home)")
	flag.Parse()
	if *home == "" {
		return fmt.Errorf("-home is required")
	}
	if *tmuxTmp == "" {
		*tmuxTmp = *home
	}

	key, err := sshtest.GenerateKey()
	if err != nil {
		return err
	}
	sock, stopAgent, err := sshtest.ServeAgent("", key)
	if err != nil {
		return err
	}
	defer stopAgent()

	srv, err := sshtest.StartServer(sshtest.Options{
		AuthorizedKeys: []ssh.PublicKey{key.PublicKey()},
		Env:            []string{"SHELL=/bin/sh", "HOME=" + *home, "TMUX_TMPDIR=" + *tmuxTmp},
	})
	if err != nil {
		return err
	}
	defer srv.Close()

	out, _ := json.Marshal(map[string]any{
		"addr":               srv.Addr,
		"port":               srv.Port,
		"hostKeyFingerprint": ssh.FingerprintSHA256(srv.HostKey),
		"agentSocket":        sock,
	})
	fmt.Println(string(out))

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			switch strings.TrimSpace(sc.Text()) {
			case "drop":
				srv.DropConnections()
			case "block":
				srv.SetBlocked(true)
			case "unblock":
				srv.SetBlocked(false)
			}
		}
	}()
	select {
	case <-done:
	case <-sig:
	}
	return nil
}
