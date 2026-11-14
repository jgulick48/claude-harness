package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"github.com/coder/websocket"

	"github.com/jgulick48/claude-harness/internal/session"
	"github.com/jgulick48/claude-harness/internal/usagehist"
)

type transcriptBackend struct {
	*fakeBackend
}

func (transcriptBackend) ReadTranscripts(context.Context, map[string]int64) ([]session.TranscriptChunk, error) {
	return nil, nil
}

type fakeHistory struct {
	mu     sync.Mutex
	res    usagehist.Result
	err    error
	scans  []string
	forget []string
}

func (f *fakeHistory) Scan(_ context.Context, host string, r session.TranscriptReader) (usagehist.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scans = append(f.scans, host)
	return f.res, f.err
}
func (f *fakeHistory) Forget(host string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forget = append(f.forget, host)
}

func connectHistory(t *testing.T, hm *fakeHM, h UsageHistory) (*websocket.Conn, context.Context) {
	t.Helper()
	var opts []Option
	if h != nil {
		opts = append(opts, WithUsageHistory(h))
	}
	ts := httptest.NewServer(NewServer(tok, slog.New(slog.NewTextHandler(io.Discard, nil)), hm, nil, opts...))
	t.Cleanup(ts.Close)
	c, ctx := dial(t, ts)
	readMsg(t, ctx, c) // hello
	readMsg(t, ctx, c) // hosts.changed
	for range hm.Backends() {
		readMsg(t, ctx, c) // sessions.changed
	}
	return c, ctx
}

func TestUsageHistory(t *testing.T) {
	hm := newFakeHM()
	hm.backends = []session.Backend{transcriptBackend{newFakeBackend()}}
	fh := &fakeHistory{res: usagehist.Result{ScannedAt: 1794701234, Buckets: []usagehist.Bucket{
		{Hour: 1794700800, Model: "claude-opus-5-5", Project: "/home/u/proj", Input: 2, Output: 264, CacheCreate: 14394, CacheRead: 24768, Messages: 1},
	}}}
	c, ctx := connectHistory(t, hm, fh)

	m := rpc(t, ctx, c, `{"type":"usage.history","id":1,"host":"local"}`)
	if m["type"] != "result" {
		t.Fatal(m)
	}
	res := m["result"].(map[string]any)
	want := map[string]any{"hour": 1794700800.0, "model": "claude-opus-5-5", "project": "/home/u/proj",
		"input": 2.0, "output": 264.0, "cacheCreate": 14394.0, "cacheRead": 24768.0, "messages": 1.0}
	if res["scannedAt"] != 1794701234.0 || !reflect.DeepEqual(res["buckets"], []any{want}) {
		t.Fatalf("%v", res)
	}
	if !reflect.DeepEqual(fh.scans, []string{"local"}) {
		t.Fatalf("scans %v", fh.scans)
	}

	// Nil buckets from the store, and unavailable hosts, give [].
	for _, e := range []error{nil, session.ErrUnavailable} {
		fh.res, fh.err = usagehist.Result{}, e
		m = rpc(t, ctx, c, `{"type":"usage.history","id":2,"host":"local"}`)
		res = m["result"].(map[string]any)
		if b, ok := res["buckets"].([]any); m["type"] != "result" || !ok || len(b) != 0 || res["scannedAt"] != 0.0 {
			t.Fatalf("%v: %v", e, m)
		}
	}
	// Other errors are errors.
	fh.err = errors.New("boom")
	if m = rpc(t, ctx, c, `{"type":"usage.history","id":3,"host":"local"}`); m["type"] != "error" {
		t.Fatal(m)
	}
	wantErr(t, rpc(t, ctx, c, `{"type":"usage.history","id":4}`), "bad_request")
	wantErr(t, rpc(t, ctx, c, `{"type":"usage.history","id":5,"host":"nope"}`), "not_found")

	// Removing a host forgets its history.
	if m = rpc(t, ctx, c, `{"type":"hosts.remove","id":6,"host":"local"}`); m["type"] != "result" {
		t.Fatal(m)
	}
	if !reflect.DeepEqual(fh.forget, []string{"local"}) {
		t.Fatalf("forget %v", fh.forget)
	}
}

func TestUsageHistoryWithoutCapabilityOrStore(t *testing.T) {
	// No TranscriptReader on the backend.
	hm := newFakeHM(newFakeBackend())
	fh := &fakeHistory{}
	c, ctx := connectHistory(t, hm, fh)
	m := rpc(t, ctx, c, `{"type":"usage.history","id":1,"host":"local"}`)
	res := m["result"].(map[string]any)
	if b, ok := res["buckets"].([]any); !ok || len(b) != 0 || res["scannedAt"] != 0.0 || len(fh.scans) != 0 {
		t.Fatalf("%v", m)
	}
	// No store configured.
	hm = newFakeHM()
	hm.backends = []session.Backend{transcriptBackend{newFakeBackend()}}
	c, ctx = connectHistory(t, hm, nil)
	m = rpc(t, ctx, c, `{"type":"usage.history","id":1,"host":"local"}`)
	if res := m["result"].(map[string]any); len(res["buckets"].([]any)) != 0 {
		t.Fatalf("%v", m)
	}
}
