package hosts

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

const fileVersion = 1

type fileHost struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Target string `json:"target"`
}

type fileData struct {
	Version int        `json:"version"`
	Hosts   []fileHost `json:"hosts"`
}

// defaultFile is <user config dir>/claude-harness/hosts.json.
func defaultFile() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user config dir: %w", err)
	}
	return filepath.Join(dir, "claude-harness", "hosts.json"), nil
}

// loadFile reads the hosts file. A missing file means no hosts; anything
// malformed is an error (the caller must not overwrite it).
func loadFile(path string) ([]fileHost, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read hosts file: %w", err)
	}
	var fd fileData
	if err := json.Unmarshal(data, &fd); err != nil {
		return nil, fmt.Errorf("hosts file %s is corrupt: %w", path, err)
	}
	if fd.Version != fileVersion {
		return nil, fmt.Errorf("hosts file %s: unsupported version %d", path, fd.Version)
	}
	seenID := map[string]bool{}
	seenTarget := map[string]bool{}
	for _, h := range fd.Hosts {
		if h.ID == "" || h.Target == "" || h.ID == localID || validTarget(h.Target) != nil {
			return nil, fmt.Errorf("hosts file %s is corrupt: invalid entry %q", path, h.ID)
		}
		if seenID[h.ID] || seenTarget[h.Target] {
			return nil, fmt.Errorf("hosts file %s is corrupt: duplicate entry %q", path, h.ID)
		}
		seenID[h.ID], seenTarget[h.Target] = true, true
	}
	return fd.Hosts, nil
}

// saveFile writes atomically: temp file in the same directory, then rename.
func saveFile(path string, hosts []fileHost) error {
	if hosts == nil {
		hosts = []fileHost{}
	}
	data, err := json.MarshalIndent(fileData{Version: fileVersion, Hosts: hosts}, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".hosts-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	cleanup := func() { _ = os.Remove(name) }
	if err := tmp.Chmod(0o600); err != nil && !errors.Is(err, errors.ErrUnsupported) {
		tmp.Close()
		cleanup()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(name, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// validTarget checks target syntax: non-empty, no whitespace or control
// characters, and not option-like (leading "-").
func validTarget(t string) error {
	if t == "" {
		return fmt.Errorf("target is required")
	}
	if strings.HasPrefix(t, "-") {
		return fmt.Errorf("invalid target %q", t)
	}
	for _, r := range t {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("invalid target: whitespace and control characters are not allowed")
		}
	}
	return nil
}

// hostPart extracts the host from alias or [user@]host[:port].
func hostPart(target string) string {
	if i := strings.LastIndex(target, "@"); i >= 0 {
		target = target[i+1:]
	}
	if strings.HasPrefix(target, "[") {
		if j := strings.Index(target, "]"); j > 0 {
			return target[1:j]
		}
		return target
	}
	if strings.Count(target, ":") == 1 {
		target = target[:strings.Index(target, ":")]
	}
	return target
}

// slug maps s to [a-z0-9-], collapsing runs and trimming dashes.
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}
