package api

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/coder/websocket"

	"github.com/jgulick48/claude-harness/internal/recent"
	"github.com/jgulick48/claude-harness/internal/session"
)

// dirBackend is a fakeBackend that can browse directories.
type dirBackend struct {
	*fakeBackend
	mu       sync.Mutex
	prefixes []string
	entries  []session.DirEntry
	repos    []string
	repoErr  error
	dirErr   error
}

func (d *dirBackend) CompleteDir(_ context.Context, prefix string) ([]session.DirEntry, error) {
	d.mu.Lock()
	d.prefixes = append(d.prefixes, prefix)
	d.mu.Unlock()
	return d.entries, d.dirErr
}
func (d *dirBackend) FindRepos(context.Context) ([]string, error) { return d.repos, d.repoErr }

type fakeRecent struct {
	mu     sync.Mutex
	dirs   map[string][]string
	pinned map[string][]string
	pinErr error
	calls  []string
}

func (r *fakeRecent) Add(host, dir string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, "add "+host+" "+dir)
	r.dirs[host] = append([]string{dir}, r.dirs[host]...)
}
func (r *fakeRecent) Pin(host, dir string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pinErr != nil {
		return r.pinErr
	}
	if r.pinned == nil {
		r.pinned = map[string][]string{}
	}
	r.pinned[host] = append(r.pinned[host], dir)
	var rest []string
	for _, d := range r.dirs[host] {
		if d != dir {
			rest = append(rest, d)
		}
	}
	r.dirs[host] = rest
	return nil
}
func (r *fakeRecent) Unpin(host, dir string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var rest []string
	for _, d := range r.pinned[host] {
		if d != dir {
			rest = append(rest, d)
		}
	}
	r.pinned[host] = rest
	r.dirs[host] = append([]string{dir}, r.dirs[host]...)
}
func (r *fakeRecent) Pinned(host string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pinned[host]
}
func (r *fakeRecent) List(host string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dirs[host]
}
func (r *fakeRecent) Forget(host string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, "forget "+host)
	delete(r.dirs, host)
}

func connectWith(t *testing.T, hm *fakeHM, recent session.RecentDirs) (*websocket.Conn, context.Context) {
	t.Helper()
	c, ctx := dial(t, newHMRecentTS(t, hm, recent))
	readMsg(t, ctx, c) // hello
	readMsg(t, ctx, c) // hosts.changed
	for range hm.Backends() {
		readMsg(t, ctx, c) // sessions.changed
	}
	return c, ctx
}

func TestCompleteDir(t *testing.T) {
	db := &dirBackend{fakeBackend: newFakeBackend(), entries: []session.DirEntry{{Path: "~/src/app/", GitRepo: true}}}
	hm := newFakeHM()
	hm.backends = []session.Backend{db}
	c, ctx := connectWith(t, hm, nil)

	m := rpc(t, ctx, c, `{"type":"remote.completeDir","id":1,"host":"local","prefix":"~/s"}`)
	if m["type"] != "result" {
		t.Fatal(m)
	}
	got := m["result"].([]any)[0].(map[string]any)
	if got["path"] != "~/src/app/" || got["gitRepo"] != true {
		t.Fatalf("%v", got)
	}
	if !reflect.DeepEqual(db.prefixes, []string{"~/s"}) {
		t.Fatalf("prefixes %v", db.prefixes)
	}

	db.entries = nil
	m = rpc(t, ctx, c, `{"type":"remote.completeDir","id":2,"host":"local","prefix":"~/"}`)
	if l, ok := m["result"].([]any); !ok || len(l) != 0 {
		t.Fatalf("nil entries should be []: %v", m)
	}

	wantErr(t, rpc(t, ctx, c, `{"type":"remote.completeDir","id":3,"prefix":"~/"}`), "bad_request")
	wantErr(t, rpc(t, ctx, c, `{"type":"remote.completeDir","id":4,"host":"nope","prefix":"~/"}`), "not_found")

	db.dirErr = errors.Join(session.ErrBadRequest)
	wantErr(t, rpc(t, ctx, c, `{"type":"remote.completeDir","id":5,"host":"local","prefix":"x"}`), "bad_request")
}

func TestCompleteDirUnsupported(t *testing.T) {
	c, ctx := connect(t, newFakeBackend())
	m := rpc(t, ctx, c, `{"type":"remote.completeDir","id":1,"host":"local","prefix":"~/"}`)
	if l, ok := m["result"].([]any); !ok || len(l) != 0 {
		t.Fatalf("%v", m)
	}
	m = rpc(t, ctx, c, `{"type":"remote.suggestDirs","id":2,"host":"local"}`)
	res := m["result"].(map[string]any)
	if r, ok := res["recent"].([]any); !ok || len(r) != 0 {
		t.Fatalf("%v", m)
	}
	if r, ok := res["repos"].([]any); !ok || len(r) != 0 {
		t.Fatalf("%v", m)
	}
}

func TestSuggestDirs(t *testing.T) {
	db := &dirBackend{fakeBackend: newFakeBackend(), repos: []string{"~/src/app/"}}
	hm := newFakeHM()
	hm.backends = []session.Backend{db}
	rec := &fakeRecent{dirs: map[string][]string{"local": {"~/work/api", "~"}}}
	c, ctx := connectWith(t, hm, rec)

	m := rpc(t, ctx, c, `{"type":"remote.suggestDirs","id":1,"host":"local"}`)
	if m["type"] != "result" {
		t.Fatal(m)
	}
	res := m["result"].(map[string]any)
	if !reflect.DeepEqual(res["recent"], []any{"~/work/api", "~"}) || !reflect.DeepEqual(res["repos"], []any{"~/src/app/"}) {
		t.Fatalf("%v", res)
	}

	// Unavailable and other FindRepos errors give empty repos, not an error.
	for _, e := range []error{session.ErrUnavailable, errors.New("boom")} {
		db.repoErr, db.repos = e, nil
		m = rpc(t, ctx, c, `{"type":"remote.suggestDirs","id":2,"host":"local"}`)
		res = m["result"].(map[string]any)
		if r, ok := res["repos"].([]any); m["type"] != "result" || !ok || len(r) != 0 {
			t.Fatalf("%v: %v", e, m)
		}
	}
	wantErr(t, rpc(t, ctx, c, `{"type":"remote.suggestDirs","id":3}`), "bad_request")
}

func TestSuggestDirsNilRecent(t *testing.T) {
	db := &dirBackend{fakeBackend: newFakeBackend()}
	hm := newFakeHM()
	hm.backends = []session.Backend{db}
	c, ctx := connectWith(t, hm, nil)
	m := rpc(t, ctx, c, `{"type":"remote.suggestDirs","id":1,"host":"local"}`)
	res := m["result"].(map[string]any)
	if r, ok := res["recent"].([]any); !ok || len(r) != 0 {
		t.Fatalf("%v", m)
	}
}

func TestPinUnpinDir(t *testing.T) {
	db := &dirBackend{fakeBackend: newFakeBackend()}
	hm := newFakeHM()
	hm.backends = []session.Backend{db}
	rec := &fakeRecent{dirs: map[string][]string{"local": {"~/work/api", "~"}}}
	c, ctx := connectWith(t, hm, rec)

	m := rpc(t, ctx, c, `{"type":"remote.pinDir","id":1,"host":"local","dir":"~/work/api"}`)
	if m["type"] != "result" {
		t.Fatal(m)
	}
	res := m["result"].(map[string]any)
	if !reflect.DeepEqual(res["pinned"], []any{"~/work/api"}) || !reflect.DeepEqual(res["recent"], []any{"~"}) {
		t.Fatalf("%v", res)
	}
	m = rpc(t, ctx, c, `{"type":"remote.suggestDirs","id":2,"host":"local"}`)
	res = m["result"].(map[string]any)
	if !reflect.DeepEqual(res["pinned"], []any{"~/work/api"}) || !reflect.DeepEqual(res["recent"], []any{"~"}) {
		t.Fatalf("%v", res)
	}

	m = rpc(t, ctx, c, `{"type":"remote.unpinDir","id":3,"host":"local","dir":"~/work/api"}`)
	res = m["result"].(map[string]any)
	if p, ok := res["pinned"].([]any); !ok || len(p) != 0 {
		t.Fatalf("pinned should be []: %v", res)
	}
	if !reflect.DeepEqual(res["recent"], []any{"~/work/api", "~"}) {
		t.Fatalf("%v", res)
	}

	wantErr(t, rpc(t, ctx, c, `{"type":"remote.pinDir","id":4,"host":"local"}`), "bad_request")
	wantErr(t, rpc(t, ctx, c, `{"type":"remote.unpinDir","id":5,"host":"local"}`), "bad_request")
	wantErr(t, rpc(t, ctx, c, `{"type":"remote.pinDir","id":6,"dir":"~/x"}`), "bad_request")
	wantErr(t, rpc(t, ctx, c, `{"type":"remote.pinDir","id":7,"host":"nope","dir":"~/x"}`), "not_found")

	rec.pinErr = recent.ErrTooManyPinned
	wantErr(t, rpc(t, ctx, c, `{"type":"remote.pinDir","id":8,"host":"local","dir":"~/x"}`), "bad_request")
}

func TestPinDirNilRecent(t *testing.T) {
	db := &dirBackend{fakeBackend: newFakeBackend()}
	hm := newFakeHM()
	hm.backends = []session.Backend{db}
	c, ctx := connectWith(t, hm, nil)
	wantErr(t, rpc(t, ctx, c, `{"type":"remote.pinDir","id":1,"host":"local","dir":"~/x"}`), "unavailable")
}

func TestRecordsRecentOnCreateAndForgetsOnRemove(t *testing.T) {
	b := newFakeBackend()
	rec := &fakeRecent{dirs: map[string][]string{}}
	hm := newFakeHM(b)
	c, ctx := connectWith(t, hm, rec)

	rpc(t, ctx, c, `{"type":"sessions.create","id":1,"host":"local","dir":"~/x/"}`)
	rpc(t, ctx, c, `{"type":"sessions.create","id":2,"host":"local"}`)
	if want := []string{"add local ~/x/", "add local "}; !reflect.DeepEqual(rec.calls, want) {
		t.Fatalf("calls %v", rec.calls)
	}

	// A failed create is not recorded.
	b.mu.Lock()
	b.err = session.ErrBadRequest
	b.mu.Unlock()
	wantErr(t, rpc(t, ctx, c, `{"type":"sessions.create","id":3,"host":"local","dir":"~/bad"}`), "bad_request")
	if len(rec.calls) != 2 {
		t.Fatalf("calls %v", rec.calls)
	}

	// A failed remove does not forget; a successful one does.
	hm.mu.Lock()
	hm.err = session.ErrBadRequest
	hm.mu.Unlock()
	wantErr(t, rpc(t, ctx, c, `{"type":"hosts.remove","id":4,"host":"local"}`), "bad_request")
	hm.mu.Lock()
	hm.err = nil
	hm.mu.Unlock()
	if m := rpc(t, ctx, c, `{"type":"hosts.remove","id":5,"host":"local"}`); m["type"] != "result" {
		t.Fatal(m)
	}
	if want := []string{"add local ~/x/", "add local ", "forget local"}; !reflect.DeepEqual(rec.calls, want) {
		t.Fatalf("calls %v", rec.calls)
	}
}
