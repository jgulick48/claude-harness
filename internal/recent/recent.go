// Package recent persists the directories most recently used to create
// sessions, per host.
package recent

import (
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// MaxPerHost is the number of directories remembered per host.
const MaxPerHost = 10

// MaxPinnedPerHost is the number of pinned directories allowed per host.
const MaxPinnedPerHost = 50

// ErrTooManyPinned is returned by Pin when a host already has MaxPinnedPerHost
// pinned directories.
var ErrTooManyPinned = errors.New("too many pinned directories")

type fileFormat struct {
	Version    int                 `json:"version"`
	RecentDirs map[string][]string `json:"recentDirs"`
	PinnedDirs map[string][]string `json:"pinnedDirs,omitempty"`
}

// Store implements session.RecentDirs, persisted as JSON.
type Store struct {
	path string
	log  *slog.Logger

	mu      sync.Mutex
	dirs    map[string][]string // recent, most recent first
	pinned  map[string][]string // in pinning order
	corrupt bool                // the file on disk is unreadable and has not been moved aside yet
}

// DefaultPath is <user config dir>/claude-harness/state.json.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "claude-harness", "state.json"), nil
}

// New opens the store at path ("" means DefaultPath). A missing file starts
// empty; a corrupt one is logged and also starts empty, and is kept until the
// next successful Add moves it to <path>.corrupt.
func New(path string, logger *slog.Logger) (*Store, error) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	if path == "" {
		p, err := DefaultPath()
		if err != nil {
			return nil, err
		}
		path = p
	}
	s := &Store{path: path, log: logger, dirs: map[string][]string{}, pinned: map[string][]string{}}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		var f fileFormat
		if err := json.Unmarshal(data, &f); err != nil || (f.Version != 1 && f.Version != 2) {
			if err == nil {
				err = errors.New("unsupported version")
			}
			logger.Error("state file is corrupt; starting empty", "path", path, "err", err)
			s.corrupt = true
		} else {
			for h, l := range f.PinnedDirs {
				if len(l) > MaxPinnedPerHost {
					l = l[:MaxPinnedPerHost]
				}
				if len(l) > 0 {
					s.pinned[h] = l
				}
			}
			for h, l := range f.RecentDirs {
				l = without(l, s.pinned[h])
				if len(l) > MaxPerHost {
					l = l[:MaxPerHost]
				}
				if len(l) > 0 {
					s.dirs[h] = l
				}
			}
		}
	}
	return s, nil
}

func normalize(dir string) string {
	if dir == "" {
		return "~"
	}
	if dir != "/" {
		if t := strings.TrimRight(dir, "/"); t != "" {
			dir = t
		}
	}
	return dir
}

// without returns l minus every entry of drop, as a new slice.
func without(l, drop []string) []string {
	out := make([]string, 0, len(l))
	for _, d := range l {
		if !slices.Contains(drop, d) {
			out = append(out, d)
		}
	}
	return out
}

// Add records dir as the most recent directory of host. A pinned directory
// stays pinned and is not added to the recent list.
func (s *Store) Add(host, dir string) {
	dir = normalize(dir)
	s.mu.Lock()
	defer s.mu.Unlock()
	if slices.Contains(s.pinned[host], dir) {
		return
	}
	old := s.dirs[host]
	l := make([]string, 0, len(old)+1)
	l = append(l, dir)
	for _, d := range old {
		if d != dir {
			l = append(l, d)
		}
	}
	if len(l) > MaxPerHost {
		l = l[:MaxPerHost]
	}
	s.dirs[host] = l
	s.saveLocked()
}

// List returns host's directories, most recent first.
func (s *Store) List(host string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.dirs[host]...)
}

// Pinned returns host's pinned directories in the order they were pinned.
func (s *Store) Pinned(host string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.pinned[host]...)
}

// Pin pins dir for host, removing it from the recent list. Pinning an already
// pinned directory is a no-op that keeps its position. It fails with
// ErrTooManyPinned when host already has MaxPinnedPerHost pinned directories.
func (s *Store) Pin(host, dir string) error {
	dir = normalize(dir)
	s.mu.Lock()
	defer s.mu.Unlock()
	if slices.Contains(s.pinned[host], dir) {
		return nil
	}
	if len(s.pinned[host]) >= MaxPinnedPerHost {
		return ErrTooManyPinned
	}
	s.pinned[host] = append(slices.Clone(s.pinned[host]), dir)
	if l := without(s.dirs[host], []string{dir}); len(l) > 0 {
		s.dirs[host] = l
	} else {
		delete(s.dirs, host)
	}
	s.saveLocked()
	return nil
}

// Unpin unpins dir for host and makes it the most recent directory. It does
// nothing if dir is not pinned.
func (s *Store) Unpin(host, dir string) {
	dir = normalize(dir)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !slices.Contains(s.pinned[host], dir) {
		return
	}
	if l := without(s.pinned[host], []string{dir}); len(l) > 0 {
		s.pinned[host] = l
	} else {
		delete(s.pinned, host)
	}
	l := append([]string{dir}, without(s.dirs[host], []string{dir})...)
	if len(l) > MaxPerHost {
		l = l[:MaxPerHost]
	}
	s.dirs[host] = l
	s.saveLocked()
}

// Forget drops host's history and pinned directories.
func (s *Store) Forget(host string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, r := s.dirs[host]
	_, p := s.pinned[host]
	if !r && !p {
		return
	}
	delete(s.dirs, host)
	delete(s.pinned, host)
	s.saveLocked()
}

func (s *Store) saveLocked() {
	if err := s.writeLocked(); err != nil {
		s.log.Error("saving state file failed", "path", s.path, "err", err)
	}
}

func (s *Store) writeLocked() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(fileFormat{Version: 2, RecentDirs: s.dirs, PinnedDirs: s.pinned}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".state-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if s.corrupt {
		if err := os.Rename(s.path, s.path+".corrupt"); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		s.corrupt = false
	}
	return os.Rename(tmp.Name(), s.path)
}
