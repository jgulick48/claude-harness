package webserve

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// EnsureSSHKey makes sure <sshDir>/id_ed25519 exists, generating an ed25519
// key pair when it doesn't. An existing private key is never overwritten. It
// returns the public key as an authorized_keys line (no trailing newline).
func EnsureSSHKey(sshDir, hostname string) (pub string, created bool, err error) {
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return "", false, err
	}
	priv := filepath.Join(sshDir, "id_ed25519")
	pubPath := priv + ".pub"
	comment := "claude-harness@" + hostname

	if b, rerr := os.ReadFile(priv); rerr == nil {
		if pb, perr := os.ReadFile(pubPath); perr == nil && strings.HasPrefix(string(pb), "ssh-") {
			return strings.TrimSpace(string(pb)), false, nil
		}
		// Public half missing: derive it from the private key.
		signer, serr := ssh.ParsePrivateKey(b)
		if serr != nil {
			return "", false, fmt.Errorf("%s: %w", priv, serr)
		}
		line := authorizedLine(signer.PublicKey(), comment)
		return line, false, os.WriteFile(pubPath, []byte(line+"\n"), 0o644)
	} else if !errors.Is(rerr, os.ErrNotExist) {
		return "", false, rerr
	}

	pk, sk, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", false, err
	}
	block, err := ssh.MarshalPrivateKey(sk, comment)
	if err != nil {
		return "", false, err
	}
	f, err := os.OpenFile(priv, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", false, err
	}
	_, werr := f.Write(pem.EncodeToMemory(block))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(priv)
		return "", false, werr
	}
	spk, err := ssh.NewPublicKey(pk)
	if err != nil {
		return "", false, err
	}
	line := authorizedLine(spk, comment)
	if err := os.WriteFile(pubPath, []byte(line+"\n"), 0o644); err != nil {
		return "", false, err
	}
	return line, true, nil
}

func authorizedLine(k ssh.PublicKey, comment string) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k))) + " " + comment
}
