package webserve

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// bcryptCost is a variable so tests can lower it.
var bcryptCost = 12

const maxPasswordLen = 72 // bcrypt ignores (or rejects) anything longer

// Users is the local account store backed by users.json.
type Users struct {
	path string
	mu   sync.Mutex
	m    map[string]userRec
	// dummy is compared against for unknown users so timing doesn't reveal
	// which names exist.
	dummy []byte
	// mtime is users.json's modification time when last read; a change on
	// disk (e.g. `harness-core user set` run in the container) is reloaded.
	mtime time.Time
}

type userRec struct {
	Hash    string    `json:"hash"`
	Updated time.Time `json:"updated"`
}

// OpenUsers loads path (a missing file is an empty store).
func OpenUsers(path string) (*Users, error) {
	u := &Users{path: path, m: map[string]userRec{}}
	if err := u.load(); err != nil {
		return nil, err
	}
	return u, nil
}

// load reads users.json into u.m; a missing file is an empty store. Callers
// hold u.mu (or own u exclusively).
func (u *Users) load() error {
	st, err := os.Stat(u.path)
	if errors.Is(err, os.ErrNotExist) {
		u.m, u.mtime = map[string]userRec{}, time.Time{}
		return nil
	} else if err != nil {
		return err
	}
	b, err := os.ReadFile(u.path)
	if err != nil {
		return err
	}
	m := map[string]userRec{}
	if err := json.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("%s: %w", u.path, err)
	}
	if m == nil {
		m = map[string]userRec{}
	}
	u.m, u.mtime = m, st.ModTime()
	return nil
}

// refresh reloads users.json when its modification time changed. A file that
// can't be read or parsed leaves the current accounts in place. Caller holds u.mu.
func (u *Users) refresh() {
	st, err := os.Stat(u.path)
	var mt time.Time
	if err == nil {
		mt = st.ModTime()
	} else if !errors.Is(err, os.ErrNotExist) {
		return
	}
	if mt.Equal(u.mtime) {
		return
	}
	old, oldT := u.m, u.mtime
	if err := u.load(); err != nil {
		u.m, u.mtime = old, oldT
	}
}

// Gen is a fingerprint of name's current password hash, or ok=false when the
// account doesn't exist. Sessions store it (Claims.Gen) so they end when the
// account is removed or its password changes.
func (u *Users) Gen(name string) (string, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.refresh()
	rec, ok := u.m[name]
	if !ok {
		return "", false
	}
	return genOf(rec.Hash), true
}

func genOf(hash string) string {
	sum := sha256.Sum256([]byte(hash))
	return hex.EncodeToString(sum[:8])
}

func (u *Users) dummyHash() []byte {
	if u.dummy == nil {
		u.dummy, _ = bcrypt.GenerateFromPassword([]byte("dummy-password"), bcryptCost)
	}
	return u.dummy
}

func (u *Users) save() error {
	b, err := json.MarshalIndent(u.m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(u.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(u.path), ".users-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), u.path); err != nil {
		return err
	}
	if st, err := os.Stat(u.path); err == nil {
		u.mtime = st.ModTime()
	}
	return nil
}

// Set creates or replaces name's password.
func (u *Users) Set(name, password string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("empty user name")
	}
	if password == "" {
		return errors.New("empty password")
	}
	if len(password) > maxPasswordLen {
		return fmt.Errorf("password longer than %d bytes", maxPasswordLen)
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.m[name] = userRec{Hash: string(h), Updated: time.Now().UTC()}
	return u.save()
}

// Remove deletes name; it reports whether the account existed.
func (u *Users) Remove(name string) (bool, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if _, ok := u.m[name]; !ok {
		return false, nil
	}
	delete(u.m, name)
	return true, u.save()
}

// Verify checks a password. It always runs bcrypt, even for unknown users.
func (u *Users) Verify(name, password string) bool {
	u.mu.Lock()
	u.refresh()
	rec, ok := u.m[name]
	dummy := u.dummyHash()
	u.mu.Unlock()
	hash := []byte(rec.Hash)
	if !ok {
		hash = dummy
	}
	err := bcrypt.CompareHashAndPassword(hash, []byte(password))
	return err == nil && ok
}

// Count is the number of accounts.
func (u *Users) Count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.m)
}

// Names lists accounts, sorted.
func (u *Users) Names() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make([]string, 0, len(u.m))
	for n := range u.m {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Bootstrap makes sure name exists with password, rewriting the hash only if
// the stored password differs. It reports whether anything changed.
func (u *Users) Bootstrap(name, password string) (bool, error) {
	u.mu.Lock()
	rec, ok := u.m[name]
	u.mu.Unlock()
	if ok && bcrypt.CompareHashAndPassword([]byte(rec.Hash), []byte(password)) == nil {
		return false, nil
	}
	return true, u.Set(name, password)
}

// RateLimiter counts failures per key in a sliding window.
type RateLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	now    func() time.Time
	fails  map[string][]time.Time
}

// NewRateLimiter allows max failures per key within window.
func NewRateLimiter(max int, window time.Duration) *RateLimiter {
	return &RateLimiter{max: max, window: window, now: time.Now, fails: map[string][]time.Time{}}
}

func (r *RateLimiter) prune(key string) []time.Time {
	cut := r.now().Add(-r.window)
	list := r.fails[key]
	i := 0
	for i < len(list) && !list[i].After(cut) {
		i++
	}
	list = list[i:]
	if len(list) == 0 {
		delete(r.fails, key)
	} else {
		r.fails[key] = list
	}
	return list
}

// Allowed reports whether key may attempt a login.
func (r *RateLimiter) Allowed(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.prune(key)) < r.max
}

// Fail records a failed attempt.
func (r *RateLimiter) Fail(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Opportunistic sweep so the map can't grow without bound.
	if len(r.fails) > 1024 {
		for k := range r.fails {
			r.prune(k)
		}
	}
	r.prune(key)
	r.fails[key] = append(r.fails[key], r.now())
}

// Reset forgets key's failures (after a successful login).
func (r *RateLimiter) Reset(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.fails, key)
}
