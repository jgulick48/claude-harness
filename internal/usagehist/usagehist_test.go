package usagehist

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jgulick48/claude-harness/internal/session"
)

type fakeReader struct {
	mu     sync.Mutex
	chunks []session.TranscriptChunk
	err    error
	known  []map[string]int64
	delay  chan struct{}
	calls  int
}

func (f *fakeReader) ReadTranscripts(_ context.Context, known map[string]int64) ([]session.TranscriptChunk, error) {
	f.mu.Lock()
	f.calls++
	f.known = append(f.known, known)
	d := f.delay
	f.mu.Unlock()
	if d != nil {
		<-d
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.chunks, f.err
}

var t0 = time.Date(2026, 11, 14, 19, 37, 1, 0, time.UTC)

func rec(id string, at time.Time, in int64) session.TranscriptRecord {
	return session.TranscriptRecord{Time: at, Model: "m", MsgID: id, Input: in, Output: 1, CacheCreate: 2, CacheRead: 3, Cwd: "/p"}
}

func newStore(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := New(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return t0.Add(time.Hour) }
	return s
}

func TestDedupeAcrossChunksAndOffsets(t *testing.T) {
	dir := t.TempDir()
	s := newStore(t, dir)
	r := &fakeReader{chunks: []session.TranscriptChunk{{Path: "/a", Size: 100, End: 60, Records: []session.TranscriptRecord{
		rec("msg_1", t0, 10), rec("msg_1", t0, 10), rec("msg_2", t0, 5),
	}}}}
	res, err := s.Scan(context.Background(), "h", r)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Buckets) != 1 || res.Buckets[0].Input != 15 || res.Buckets[0].Messages != 2 || res.Buckets[0].Hour != t0.Truncate(time.Hour).Unix() ||
		res.Buckets[0].Project != "/p" || res.ScannedAt != t0.Add(time.Hour).Unix() {
		t.Fatalf("first: %+v", res)
	}
	// Second read: offsets are passed, msg_2 repeats across the boundary.
	r.chunks = []session.TranscriptChunk{{Path: "/a", Size: 200, Start: 60, End: 200, Records: []session.TranscriptRecord{
		rec("msg_2", t0, 5), rec("msg_3", t0.Add(30*time.Minute), 1),
	}}}
	res, _ = s.Scan(context.Background(), "h", r)
	if got := r.known[1]["/a"]; got != 60 {
		t.Errorf("known offset %d, want 60", got)
	}
	if len(res.Buckets) != 2 || res.Buckets[0].Input != 15 || res.Buckets[1].Input != 1 {
		t.Fatalf("second: %+v", res.Buckets)
	}
	// Persisted: a new store continues from the same offsets and last id.
	s2 := newStore(t, dir)
	r.chunks = []session.TranscriptChunk{{Path: "/a", Size: 300, Start: 200, End: 300, Records: []session.TranscriptRecord{rec("msg_3", t0, 1), rec("msg_4", t0, 100)}}}
	res, err = s2.Scan(context.Background(), "h", r)
	if err != nil || r.known[2]["/a"] != 200 {
		t.Fatalf("known after reload: %v %v", r.known[2], err)
	}
	if res.Buckets[0].Input != 115 || res.Buckets[0].Messages != 3 || len(res.Buckets) != 2 || res.Buckets[1].Input != 1 {
		t.Fatalf("after reload: %+v", res.Buckets)
	}
	fi, err := os.Stat(filepath.Join(dir, "h.json"))
	if err != nil || runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("file: %v %v", fi, err)
	}
}

func TestResetAndMissingFiles(t *testing.T) {
	s := newStore(t, t.TempDir())
	r := &fakeReader{chunks: []session.TranscriptChunk{
		{Path: "/a", Size: 10, End: 10, Records: []session.TranscriptRecord{rec("msg_1", t0, 10)}},
		{Path: "/b", Size: 10, End: 10, Records: []session.TranscriptRecord{rec("msg_9", t0, 1)}},
	}}
	s.Scan(context.Background(), "h", r)
	// /a was replaced (reset) and starts with the same id again: counted
	// again, earlier counts are kept. /b vanished.
	r.chunks = []session.TranscriptChunk{{Path: "/a", Size: 5, Start: 0, End: 5, Reset: true, Records: []session.TranscriptRecord{rec("msg_1", t0, 10)}}}
	res, _ := s.Scan(context.Background(), "h", r)
	if res.Buckets[0].Input != 21 || res.Buckets[0].Messages != 3 {
		t.Fatalf("after reset: %+v", res.Buckets)
	}
	r.chunks = nil
	s.Scan(context.Background(), "h", r)
	s.Scan(context.Background(), "h", r)
	if _, ok := r.known[3]["/b"]; ok || len(r.known[3]) != 0 {
		t.Errorf("vanished files still known: %v", r.known[3])
	}
}

func TestPruning(t *testing.T) {
	s := newStore(t, t.TempDir())
	old := t0.Add(-Retention - 2*time.Hour)
	r := &fakeReader{chunks: []session.TranscriptChunk{{Path: "/a", Size: 1, End: 1, Records: []session.TranscriptRecord{
		rec("msg_old", old, 1), rec("msg_new", t0, 2),
	}}}}
	res, _ := s.Scan(context.Background(), "h", r)
	if len(res.Buckets) != 1 || res.Buckets[0].Input != 2 {
		t.Fatalf("old record kept: %+v", res.Buckets)
	}
	// Time passes: the remaining bucket ages out of the window.
	s.now = func() time.Time { return t0.Add(Retention + 2*time.Hour) }
	r.chunks = []session.TranscriptChunk{{Path: "/a", Size: 1, End: 1}}
	res, _ = s.Scan(context.Background(), "h", r)
	if res.Buckets == nil || len(res.Buckets) != 0 {
		t.Fatalf("aged bucket kept: %#v", res.Buckets)
	}
}

func TestCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "h.json")
	os.WriteFile(path, []byte("{nope"), 0o600)
	s := newStore(t, dir)
	r := &fakeReader{chunks: []session.TranscriptChunk{{Path: "/a", Size: 1, End: 1, Records: []session.TranscriptRecord{rec("msg_1", t0, 1)}}}}
	res, err := s.Scan(context.Background(), "h", r)
	if err != nil || len(res.Buckets) != 1 || len(r.known[0]) != 0 {
		t.Fatalf("scan after corrupt: %+v %v %v", res, err, r.known)
	}
	if b, err := os.ReadFile(path + ".corrupt"); err != nil || string(b) != "{nope" {
		t.Errorf("corrupt file not kept aside: %q %v", b, err)
	}
	if _, err := New("", nil); err != nil {
		t.Error(err)
	}
}

func TestErrorForgetAndHostNames(t *testing.T) {
	dir := t.TempDir()
	s := newStore(t, dir)
	boom := errors.New("boom")
	if _, err := s.Scan(context.Background(), "h", &fakeReader{err: boom}); !errors.Is(err, boom) {
		t.Fatalf("err: %v", err)
	}
	r := &fakeReader{chunks: []session.TranscriptChunk{{Path: "/a", Size: 1, End: 1, Records: []session.TranscriptRecord{rec("msg_1", t0, 1)}}}}
	s.Scan(context.Background(), "../weird/host", r)
	if m, _ := filepath.Glob(filepath.Join(dir, "*.json")); len(m) != 1 || filepath.Dir(m[0]) != dir {
		t.Fatalf("files: %v", m)
	}
	s.Forget("../weird/host")
	if m, _ := filepath.Glob(filepath.Join(dir, "*.json")); len(m) != 0 {
		t.Fatalf("not forgotten: %v", m)
	}
	res, _ := s.Scan(context.Background(), "../weird/host", &fakeReader{})
	if len(res.Buckets) != 0 {
		t.Fatalf("history survived Forget: %+v", res)
	}
}

func TestConcurrentScansShare(t *testing.T) {
	s := newStore(t, t.TempDir())
	r := &fakeReader{delay: make(chan struct{}), chunks: []session.TranscriptChunk{{Path: "/a", Size: 1, End: 1, Records: []session.TranscriptRecord{rec("msg_1", t0, 1)}}}}
	var wg sync.WaitGroup
	out := make([]Result, 3)
	for i := range out {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i], _ = s.Scan(context.Background(), "h", r)
		}()
		if i == 0 {
			for { // let the first scan reach the reader
				r.mu.Lock()
				n := r.calls
				r.mu.Unlock()
				if n > 0 {
					break
				}
				time.Sleep(time.Millisecond)
			}
		}
	}
	time.Sleep(50 * time.Millisecond)
	close(r.delay)
	wg.Wait()
	if r.calls != 1 {
		t.Errorf("reader called %d times", r.calls)
	}
	for _, o := range out {
		if len(o.Buckets) != 1 || o.Buckets[0].Messages != 1 {
			t.Errorf("result: %+v", o)
		}
	}
}
