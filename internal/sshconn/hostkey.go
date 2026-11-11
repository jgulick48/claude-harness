package sshconn

import (
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/skeema/knownhosts"
	"golang.org/x/crypto/ssh"
)

// loadKnownHosts reads known_hosts fresh (so keys trusted since the last dial
// are seen). A missing file is treated as empty.
func loadKnownHosts(path string) (*knownhosts.HostKeyDB, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		tmp, terr := os.CreateTemp("", "kh")
		if terr != nil {
			return nil, terr
		}
		tmp.Close()
		defer os.Remove(tmp.Name())
		return knownhosts.NewDB(tmp.Name())
	}
	db, err := knownhosts.NewDB(path)
	if err != nil {
		return nil, fmt.Errorf("read known_hosts %s: %w", path, err)
	}
	return db, nil
}

// hostKeyCallback wraps the db callback, converting its errors to our types.
func hostKeyCallback(db *knownhosts.HostKeyDB) ssh.HostKeyCallback {
	cb := db.HostKeyCallback()
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := cb(hostname, remote, key)
		if err == nil {
			return nil
		}
		addr := knownhosts.Normalize(hostname)
		switch {
		case knownhosts.IsHostKeyChanged(err):
			return &HostKeyChangedError{Address: addr, KeyType: key.Type(), Fingerprint: ssh.FingerprintSHA256(key)}
		case knownhosts.IsHostUnknown(err):
			return &UnknownHostKeyError{Address: addr, KeyType: key.Type(), Fingerprint: ssh.FingerprintSHA256(key), Key: key}
		}
		return err
	}
}

// TrustHostKey appends address/key to knownHostsFile (created if missing,
// mode 0600, parent dir 0700), in OpenSSH format.
func TrustHostKey(knownHostsFile, address string, key ssh.PublicKey) error {
	if err := os.MkdirAll(filepath.Dir(knownHostsFile), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(knownHostsFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	line := knownhosts.Line([]string{address}, key) + "\n"
	// Make sure we start on a fresh line if the file lacks a trailing newline.
	if st, err := f.Stat(); err == nil && st.Size() > 0 {
		if b, err := os.ReadFile(knownHostsFile); err == nil && len(b) > 0 && b[len(b)-1] != '\n' {
			line = "\n" + line
		}
	}
	if _, err := f.WriteString(line); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
