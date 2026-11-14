// Package usagehist aggregates Claude token usage read from the transcripts
// on each host into hourly buckets and caches them locally, per host, with
// the byte offsets already read so repeat scans only read appended data.
package usagehist

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jgulick48/claude-harness/internal/session"
)

const (
	// Retention is how long buckets are kept.
	Retention = 32 * 24 * time.Hour
	// ScanTimeout bounds one scan.
	ScanTimeout = 60 * time.Second

	fileVersion = 1
)

// Bucket is the usage of one model in one project during one UTC hour.
type Bucket struct {
	Hour        int64  `json:"hour"` // unix seconds, start of the UTC hour
	Model       string `json:"model"`
	Project     string `json:"project"` // the entries' cwd; "" when absent
	Input       int64  `json:"input"`
	Output      int64  `json:"output"`
	CacheCreate int64  `json:"cacheCreate"`
	CacheRead   int64  `json:"cacheRead"`
	Messages    int64  `json:"messages"`
}

// Result is what Scan returns.
type Result struct {
	Buckets   []Bucket `json:"buckets"` // never nil
	ScannedAt int64    `json:"scannedAt"`
}

type fileState struct {
	Offset    int64  `json:"offset"`
	LastMsgID string `json:"lastMsgId,omitempty"`
}

type bucketKey struct {
	hour           int64
	model, project string
}

type fileFormat struct {
	Version   int                  `json:"version"`
	ScannedAt int64                `json:"scannedAt"`
	Files     map[string]fileState `json:"files"`
	Buckets   []Bucket             `json:"buckets"`
}

// hostState is one host's data; mu serializes scans and guards the rest.
type hostState struct {
	mu      sync.Mutex
	loaded  bool
	corrupt bool         // the file on disk is unreadable and has not been moved aside yet
	gen     atomic.Int64 // number of completed scans, to let waiters share a result
	files   map[string]fileState
	buckets map[bucketKey]*Bucket
	scanned int64
}

// Store keeps usage history per host under a directory.
type Store struct {
	dir string
	log *slog.Logger
	now func() time.Time

	mu    sync.Mutex
	hosts map[string]*hostState
}

// DefaultDir is <user config dir>/claude-harness/usage-history.
func DefaultDir() (string, error) {
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "claude-harness", "usage-history"), nil
}

// New returns a store in dir ("" means DefaultDir). Nothing is read or
// created until the first scan.
func New(dir string, logger *slog.Logger) (*Store, error) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	if dir == "" {
		d, err := DefaultDir()
		if err != nil {
			return nil, err
		}
		dir = d
	}
	return &Store{dir: dir, log: logger, now: time.Now, hosts: map[string]*hostState{}}, nil
}

func (s *Store) path(host string) string {
	return filepath.Join(s.dir, url.QueryEscape(host)+".json")
}

func (s *Store) state(host string) *hostState {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.hosts[host]
	if h == nil {
		h = &hostState{}
		s.hosts[host] = h
	}
	return h
}

// load reads the host's file once; h.mu is held.
func (s *Store) load(host string, h *hostState) {
	if h.loaded {
		return
	}
	h.loaded = true
	h.files = map[string]fileState{}
	h.buckets = map[bucketKey]*Bucket{}
	path := s.path(host)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err == nil {
		var f fileFormat
		if err = json.Unmarshal(data, &f); err == nil && f.Version != fileVersion {
			err = errors.New("unsupported version")
		}
		if err == nil {
			for p, fs := range f.Files {
				if fs.Offset >= 0 {
					h.files[p] = fs
				}
			}
			for _, b := range f.Buckets {
				b := b
				h.buckets[bucketKey{b.Hour, b.Model, b.Project}] = &b
			}
			h.scanned = f.ScannedAt
			return
		}
	}
	s.log.Error("usage history file is unreadable; starting empty", "path", path, "err", err)
	h.corrupt = true
}

// Scan reads what was appended to host's transcripts since the last scan,
// folds it into the cached buckets, saves them and returns them. Callers that
// arrive while a scan of the same host runs wait for it and share its result.
func (s *Store) Scan(ctx context.Context, host string, r session.TranscriptReader) (Result, error) {
	h := s.state(host)
	gen := h.gen.Load() // before waiting: a scan that finishes meanwhile is shared
	h.mu.Lock()
	defer h.mu.Unlock()
	s.load(host, h)
	if h.gen.Load() != gen {
		return h.result(), nil // a scan finished while this caller waited
	}
	ctx, cancel := context.WithTimeout(ctx, ScanTimeout)
	defer cancel()
	known := make(map[string]int64, len(h.files))
	for p, fs := range h.files {
		known[p] = fs.Offset
	}
	chunks, err := r.ReadTranscripts(ctx, known)
	if err != nil {
		return Result{}, err
	}
	now := s.now()
	h.apply(chunks, now)
	h.scanned = now.Unix()
	h.gen.Add(1)
	if err := s.save(host, h); err != nil {
		s.log.Error("saving usage history failed", "host", host, "err", err)
	}
	return h.result(), nil
}

// Forget deletes host's cached history.
func (s *Store) Forget(host string) {
	s.mu.Lock()
	h := s.hosts[host]
	delete(s.hosts, host)
	s.mu.Unlock()
	if h != nil {
		h.mu.Lock() // wait for a running scan, which would otherwise recreate the file
		defer h.mu.Unlock()
	}
	if err := os.Remove(s.path(host)); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.log.Error("removing usage history failed", "host", host, "err", err)
	}
}

// apply folds chunks into the state. Files missing from chunks are dropped
// from the offset map; their buckets stay until they age out.
func (h *hostState) apply(chunks []session.TranscriptChunk, now time.Time) {
	cutoff := now.Add(-Retention).Truncate(time.Hour).Unix()
	files := make(map[string]fileState, len(chunks))
	for _, c := range chunks {
		fs := h.files[c.Path]
		if c.Reset {
			fs = fileState{}
		}
		last := fs.LastMsgID
		for _, r := range c.Records {
			if r.MsgID != "" && r.MsgID == last {
				continue
			}
			last = r.MsgID
			hour := r.Time.UTC().Truncate(time.Hour).Unix()
			if hour < cutoff {
				continue
			}
			k := bucketKey{hour, r.Model, r.Cwd}
			b := h.buckets[k]
			if b == nil {
				b = &Bucket{Hour: hour, Model: r.Model, Project: r.Cwd}
				h.buckets[k] = b
			}
			b.Input += r.Input
			b.Output += r.Output
			b.CacheCreate += r.CacheCreate
			b.CacheRead += r.CacheRead
			b.Messages++
		}
		files[c.Path] = fileState{Offset: c.End, LastMsgID: last}
	}
	h.files = files
	for k := range h.buckets {
		if k.hour < cutoff {
			delete(h.buckets, k)
		}
	}
}

func (h *hostState) result() Result {
	out := make([]Bucket, 0, len(h.buckets))
	for _, b := range h.buckets {
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Hour != b.Hour {
			return a.Hour < b.Hour
		}
		if a.Model != b.Model {
			return a.Model < b.Model
		}
		return a.Project < b.Project
	})
	return Result{Buckets: out, ScannedAt: h.scanned}
}

func (s *Store) save(host string, h *hostState) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	res := h.result()
	data, err := json.Marshal(fileFormat{Version: fileVersion, ScannedAt: h.scanned, Files: h.files, Buckets: res.Buckets})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".usage-*.tmp")
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
	path := s.path(host)
	if h.corrupt {
		if err := os.Rename(path, path+".corrupt"); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		h.corrupt = false
	}
	return os.Rename(tmp.Name(), path)
}
