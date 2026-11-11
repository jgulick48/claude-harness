package api

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/coder/websocket"

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
	mu    sync.Mutex
	dirs  map[string][]string
	calls []string
}

func (r *fakeRecent) Add(host, dir string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, "add "+host+" "+dir)
	r.dirs[host] = append([]string{dir}, r.dirs[host]...)
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
