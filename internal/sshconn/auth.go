package sshconn

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// authState holds the signers gathered for one hop and what to close afterwards.
type authState struct {
	signers  []ssh.Signer
	closers  []io.Closer
	skipped  []string // passphrase-protected key files
	agentErr error
}

func (a *authState) close() {
	for _, c := range a.closers {
		_ = c.Close()
	}
}

// gatherAuth collects agent signers, then (non-Windows only) identity-file signers.
func gatherAuth(r Resolved, opts Options, log *slog.Logger) *authState {
	st := &authState{}
	ag, closer, err := connectAgent(opts)
	if err != nil {
		st.agentErr = err
		log.Debug("ssh agent unavailable", "err", err)
	} else {
		st.closers = append(st.closers, closer)
		signers, serr := ag.Signers()
		if serr != nil {
			st.agentErr = serr
			log.Debug("ssh agent: listing keys failed", "err", serr)
		} else {
			st.signers = append(st.signers, signers...)
		}
	}
	if onWindows {
		return st // Pageant only: never read key files on Windows
	}

	var files []string
	files = append(files, r.IdentityFiles...)
	for _, f := range opts.IdentityFiles {
		files = append(files, expandTokens(f, r.HostName, r.User))
	}
	if h := homeDir(); h != "" {
		for _, n := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
			files = append(files, filepath.Join(h, ".ssh", n))
		}
	}
	seen := map[string]bool{}
	for _, f := range files {
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		pem, err := os.ReadFile(f)
		if err != nil {
			if !os.IsNotExist(err) {
				log.Debug("identity file unreadable", "file", f, "err", err)
			}
			continue
		}
		s, err := ssh.ParsePrivateKey(pem)
		if err != nil {
			var pm *ssh.PassphraseMissingError
			if errors.As(err, &pm) {
				log.Debug("skipping passphrase-protected identity file", "file", f)
				st.skipped = append(st.skipped, f)
			} else {
				log.Debug("skipping unparsable identity file", "file", f, "err", err)
			}
			continue
		}
		st.signers = append(st.signers, s)
	}
	return st
}

func (a *authState) noMethodsErr(r Resolved) error {
	who := r.User + "@" + r.HostName
	if onWindows {
		return fmt.Errorf("%w for %s: Pageant is not running or has no keys loaded", ErrNoAuthMethods, who)
	}
	msg := "no keys in ssh-agent and no usable identity files; load a key with ssh-add"
	if a.agentErr != nil {
		msg += fmt.Sprintf(" (ssh-agent: %v)", a.agentErr)
	}
	if len(a.skipped) > 0 {
		msg += "; skipped passphrase-protected keys (add them with ssh-add): " + strings.Join(a.skipped, ", ")
	}
	return fmt.Errorf("%w for %s: %s", ErrNoAuthMethods, who, msg)
}
