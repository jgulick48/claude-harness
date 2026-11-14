package api

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jgulick48/claude-harness/internal/session"
)

type fakeTerm struct {
	out      chan []byte
	mu       sync.Mutex
	writes   [][]byte
	resizes  [][2]int
	closed   chan struct{}
	closeOne sync.Once
	err      error
	wrote    chan struct{}
}

func newFakeTerm() *fakeTerm {
	return &fakeTerm{out: make(chan []byte, 16), closed: make(chan struct{}), wrote: make(chan struct{}, 1024)}
}
func (f *fakeTerm) Output() <-chan []byte { return f.out }
func (f *fakeTerm) Write(p []byte) error {
	f.mu.Lock()
	f.writes = append(f.writes, append([]byte(nil), p...))
	f.mu.Unlock()
	f.wrote <- struct{}{}
	return nil
}
func (f *fakeTerm) Resize(c, r int) error {
	f.mu.Lock()
	f.resizes = append(f.resizes, [2]int{c, r})
	f.mu.Unlock()
	return nil
}
func (f *fakeTerm) Close() error {
	f.closeOne.Do(func() { close(f.closed); close(f.out) })
	return nil
}
func (f *fakeTerm) Err() error { return f.err }

// end simulates the terminal ending on its own.
func (f *fakeTerm) end(err error) {
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
	f.closeOne.Do(func() { close(f.out) })
}

type fakeBackend struct {
	id string

	mu       sync.Mutex
	subFn    func([]session.Session)
	unsubbed chan struct{}
	calls    []string
	createOp session.CreateOptions
	err      error // returned by all ops when set
	term     *fakeTerm
	termCh   chan *fakeTerm
}

func newFakeBackend() *fakeBackend { return newFakeBackendID("local") }

func newFakeBackendID(id string) *fakeBackend {
	return &fakeBackend{id: id, unsubbed: make(chan struct{}), termCh: make(chan *fakeTerm, 4)}
}

func (b *fakeBackend) ID() string { return b.id }
func (b *fakeBackend) Host(context.Context) session.Host {
	return session.Host{ID: b.id, Name: "This computer", State: session.HostConnected, Home: "/home/me"}
}
func (b *fakeBackend) rec(s string) {
	b.mu.Lock()
	b.calls = append(b.calls, s)
	b.mu.Unlock()
}
func (b *fakeBackend) List(context.Context) ([]session.Session, error) {
	b.rec("list")
	return []session.Session{{ID: "$1", Name: "a"}}, b.err
}
func (b *fakeBackend) Create(_ context.Context, o session.CreateOptions) (session.Session, error) {
	b.mu.Lock()
	b.createOp = o
	b.mu.Unlock()
	return session.Session{ID: "$2", Name: o.Name}, b.err
}
func (b *fakeBackend) Kill(_ context.Context, id string) error {
	b.rec("kill " + id)
	return b.err
}
func (b *fakeBackend) Rename(_ context.Context, id, name string) error {
	b.rec("rename " + id + "|" + name)
	return b.err
}
func (b *fakeBackend) SetColor(_ context.Context, id, color string) error {
	b.rec("color " + id + "|" + color)
	return b.err
}
func (b *fakeBackend) Subscribe(fn func([]session.Session)) func() {
	b.mu.Lock()
	b.subFn = fn
	b.mu.Unlock()
	fn([]session.Session{{ID: "$1"}})
	return func() { close(b.unsubbed) }
}
func (b *fakeBackend) Attach(_ context.Context, id string, cols, rows int) (session.Terminal, error) {
	b.rec(fmt.Sprintf("attach %s %dx%d", id, cols, rows))
	if b.err != nil {
		return nil, b.err
	}
	t := newFakeTerm()
	t.out <- []byte("seed") // output ready before reply is sent
	b.termCh <- t
	return t, nil
}
func (b *fakeBackend) Close() error { return nil }

func newBackendTS(t *testing.T, b *fakeBackend) *httptest.Server {
	t.Helper()
	return newHMTS(t, newFakeHM(b))
}

func newHMTS(t *testing.T, hm session.HostManager) *httptest.Server {
	t.Helper()
	return newHMRecentTS(t, hm, nil)
}

func newHMRecentTS(t *testing.T, hm session.HostManager, recent session.RecentDirs) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(NewServer(tok, slog.New(slog.NewTextHandler(io.Discard, nil)), hm, recent))
	t.Cleanup(ts.Close)
	return ts
}

// connect dials, consumes hello, the initial hosts.changed and the initial sessions.changed.
func connect(t *testing.T, b *fakeBackend) (*websocket.Conn, context.Context) {
	t.Helper()
	c, ctx := dial(t, newBackendTS(t, b))
	if m := readMsg(t, ctx, c); m["type"] != "hello" {
		t.Fatalf("hello: %v", m)
	}
	if m := readMsg(t, ctx, c); m["event"] != "hosts.changed" {
		t.Fatalf("initial hosts event: %v", m)
	}
	if m := readMsg(t, ctx, c); m["event"] != "sessions.changed" {
		t.Fatalf("initial event: %v", m)
	}
	return c, ctx
}

func rpc(t *testing.T, ctx context.Context, c *websocket.Conn, req string) map[string]any {
	t.Helper()
	send(t, ctx, c, req)
	return readMsg(t, ctx, c)
}

func wantErr(t *testing.T, m map[string]any, code string) {
	t.Helper()
	if m["type"] != "error" || m["code"] != code {
		t.Fatalf("want error %s, got %v", code, m)
	}
}

func TestHostsList(t *testing.T) {
	c, ctx := connect(t, newFakeBackend())
	m := rpc(t, ctx, c, `{"type":"hosts.list","id":1}`)
	if m["type"] != "result" || m["id"] != float64(1) {
		t.Fatal(m)
	}
	h := m["result"].([]any)[0].(map[string]any)
	if h["id"] != "local" || h["state"] != "connected" || h["home"] != "/home/me" {
		t.Fatal(h)
	}
}

func TestSessionsCRUD(t *testing.T) {
	b := newFakeBackend()
	c, ctx := connect(t, b)

	m := rpc(t, ctx, c, `{"type":"sessions.list","id":1,"host":"local"}`)
	if m["result"].([]any)[0].(map[string]any)["id"] != "$1" {
		t.Fatal(m)
	}
	m = rpc(t, ctx, c, `{"type":"sessions.create","id":2,"host":"local","dir":"~/x","name":"n"}`)
	if m["type"] != "result" || m["result"].(map[string]any)["id"] != "$2" {
		t.Fatal(m)
	}
	if b.createOp != (session.CreateOptions{Dir: "~/x", Name: "n", Command: ""}) {
		t.Fatalf("create opts %+v", b.createOp)
	}
	rpc(t, ctx, c, `{"type":"sessions.create","id":3,"host":"local","command":"claude"}`)
	if b.createOp.Command != "claude" {
		t.Fatal(b.createOp)
	}
	m = rpc(t, ctx, c, `{"type":"sessions.kill","id":4,"host":"local","session":"$1"}`)
	if m["type"] != "result" || m["result"] != nil {
		t.Fatal(m)
	}
	m = rpc(t, ctx, c, `{"type":"sessions.rename","id":5,"host":"local","session":"$1","name":""}`)
	if m["type"] != "result" || m["id"] != float64(5) {
		t.Fatal(m)
	}
	m = rpc(t, ctx, c, `{"type":"sessions.setColor","id":6,"host":"local","session":"$1","color":"blue"}`)
	if m["type"] != "result" || m["id"] != float64(6) {
		t.Fatal(m)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if fmt.Sprint(b.calls) != "[list kill $1 rename $1| color $1|blue]" {
		t.Fatal(b.calls)
	}
}

func TestErrorMapping(t *testing.T) {
	b := newFakeBackend()
	c, ctx := connect(t, b)
	wantErr(t, rpc(t, ctx, c, `{"type":"sessions.list","id":1,"host":"nope"}`), "not_found")
	for err, code := range map[error]string{
		session.ErrNotFound:    "not_found",
		session.ErrBadRequest:  "bad_request",
		session.ErrUnavailable: "unavailable",
		errors.New("boom"):     "internal",
	} {
		b.mu.Lock()
		b.err = fmt.Errorf("%w: detail", err)
		b.mu.Unlock()
		m := rpc(t, ctx, c, `{"type":"sessions.kill","id":9,"host":"local","session":"$1"}`)
		wantErr(t, m, code)
		if m["id"] != float64(9) || m["message"] != b.err.Error() {
			t.Fatal(m)
		}
	}
}

func TestBadParams(t *testing.T) {
	c, ctx := connect(t, newFakeBackend())
	for _, r := range []string{
		`{"type":"sessions.list","id":1}`,
		`{"type":"sessions.list","id":1,"host":""}`,
		`{"type":"sessions.kill","id":1,"host":"local"}`,
		`{"type":"sessions.rename","id":1,"host":"local"}`,
		`{"type":"sessions.setColor","id":1,"host":"local","color":"red"}`,
		`{"type":"sessions.list","id":1,"host":5}`,
		`{"type":"term.attach","id":1,"host":"local","session":"$1","cols":0,"rows":5}`,
		`{"type":"term.attach","id":1,"host":"local","session":"$1","cols":1001,"rows":5}`,
		`{"type":"term.attach","id":1,"host":"local","session":"$1","cols":80.5,"rows":5}`,
		`{"type":"term.attach","id":1,"host":"local","session":"$1","cols":80}`,
		`{"type":"term.attach","id":1,"host":"local","cols":80,"rows":5}`,
		`{"type":"term.resize","id":1,"cols":80,"rows":5}`,
	} {
		m := rpc(t, ctx, c, r)
		wantErr(t, m, "bad_request")
		if m["id"] != float64(1) {
			t.Fatalf("%s: %v", r, m)
		}
	}
	wantErr(t, rpc(t, ctx, c, `{"type":"term.detach","id":1,"stream":99}`), "not_found")
}

func TestMissingID(t *testing.T) {
	c, ctx := connect(t, newFakeBackend())
	for _, r := range []string{`{"type":"hosts.list"}`, `{"type":"hosts.list","id":"x"}`, `{"type":"hosts.list","id":1.5}`} {
		m := rpc(t, ctx, c, r)
		wantErr(t, m, "bad_request")
		if _, has := m["id"]; has {
			t.Fatalf("%s: id present: %v", r, m)
		}
	}
}

func TestSessionsChangedAndUnsubscribe(t *testing.T) {
	b := newFakeBackend()
	c, ctx := connect(t, b)
	b.mu.Lock()
	fn := b.subFn
	b.mu.Unlock()
	fn([]session.Session{{ID: "$7"}, {ID: "$8"}})
	m := readMsg(t, ctx, c)
	if m["type"] != "event" || m["event"] != "sessions.changed" || m["host"] != "local" || len(m["sessions"].([]any)) != 2 {
		t.Fatal(m)
	}
	c.Close(websocket.StatusNormalClosure, "")
	select {
	case <-b.unsubbed:
	case <-time.After(5 * time.Second):
		t.Fatal("not unsubscribed")
	}
}

func attach(t *testing.T, ctx context.Context, c *websocket.Conn, b *fakeBackend) (uint32, *fakeTerm) {
	t.Helper()
	send(t, ctx, c, `{"type":"term.attach","id":10,"host":"local","session":"$1","cols":80,"rows":24}`)
	// The result must precede the first binary frame.
	typ, data, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("first frame after attach was binary")
	}
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	if m["type"] != "result" || m["id"] != float64(10) {
		t.Fatal(m)
	}
	id := uint32(m["result"].(map[string]any)["stream"].(float64))
	return id, <-b.termCh
}

func readBin(t *testing.T, ctx context.Context, c *websocket.Conn) (uint32, []byte) {
	t.Helper()
	typ, data, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if typ != websocket.MessageBinary || len(data) < 4 {
		t.Fatalf("expected binary frame, got %v %q", typ, data)
	}
	return binary.BigEndian.Uint32(data), data[4:]
}

func frame(id uint32, p string) []byte {
	return append(binary.BigEndian.AppendUint32(nil, id), p...)
}

func TestAttachOutputAndInput(t *testing.T) {
	b := newFakeBackend()
	c, ctx := connect(t, b)
	id, term := attach(t, ctx, c, b)
	if id != 1 {
		t.Fatalf("first stream id %d", id)
	}
	if gid, p := readBin(t, ctx, c); gid != 1 || string(p) != "seed" {
		t.Fatal(gid, p)
	}
	term.out <- []byte("live")
	if gid, p := readBin(t, ctx, c); gid != 1 || string(p) != "live" {
		t.Fatal(gid, p)
	}

	// Input, ordered.
	const n = 200
	for i := 0; i < n; i++ {
		if err := c.Write(ctx, websocket.MessageBinary, frame(id, fmt.Sprintf("k%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < n; i++ {
		select {
		case <-term.wrote:
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for writes")
		}
	}
	term.mu.Lock()
	defer term.mu.Unlock()
	for i, w := range term.writes {
		if string(w) != fmt.Sprintf("k%d", i) {
			t.Fatalf("write %d = %q", i, w)
		}
	}
}

func TestResizeDetach(t *testing.T) {
	b := newFakeBackend()
	c, ctx := connect(t, b)
	id, term := attach(t, ctx, c, b)
	readBin(t, ctx, c)

	m := rpc(t, ctx, c, fmt.Sprintf(`{"type":"term.resize","id":11,"stream":%d,"cols":100,"rows":30}`, id))
	if m["type"] != "result" {
		t.Fatal(m)
	}
	term.mu.Lock()
	if fmt.Sprint(term.resizes) != "[[100 30]]" {
		t.Fatal(term.resizes)
	}
	term.mu.Unlock()

	m = rpc(t, ctx, c, fmt.Sprintf(`{"type":"term.detach","id":12,"stream":%d}`, id))
	if m["type"] != "result" || m["id"] != float64(12) {
		t.Fatal(m)
	}
	select {
	case <-term.closed:
	default:
		t.Fatal("terminal not closed")
	}
	// No term.closed: the next message is the pong.
	m = rpc(t, ctx, c, `{"type":"ping","id":13}`)
	if m["type"] != "pong" {
		t.Fatalf("unexpected message after detach: %v", m)
	}
	wantErr(t, rpc(t, ctx, c, fmt.Sprintf(`{"type":"term.detach","id":14,"stream":%d}`, id)), "not_found")
}

func TestTerminalEnds(t *testing.T) {
	b := newFakeBackend()
	c, ctx := connect(t, b)
	id, term := attach(t, ctx, c, b)
	readBin(t, ctx, c)
	term.end(errors.New("session ended"))
	m := readMsg(t, ctx, c)
	if m["event"] != "term.closed" || m["stream"] != float64(id) || m["reason"] != "session ended" {
		t.Fatal(m)
	}

	id2, term2 := attach(t, ctx, c, b)
	readBin(t, ctx, c)
	term2.end(nil)
	m = readMsg(t, ctx, c)
	if m["event"] != "term.closed" || m["stream"] != float64(id2) || m["reason"] != "closed" {
		t.Fatal(m)
	}
}

func TestDisconnectClosesTerminals(t *testing.T) {
	b := newFakeBackend()
	c, ctx := connect(t, b)
	_, term := attach(t, ctx, c, b)
	c.Close(websocket.StatusNormalClosure, "")
	select {
	case <-term.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("terminal not closed on disconnect")
	}
}

func TestBadBinaryFramesIgnored(t *testing.T) {
	b := newFakeBackend()
	c, ctx := connect(t, b)
	id, term := attach(t, ctx, c, b)
	readBin(t, ctx, c)
	_ = c.Write(ctx, websocket.MessageBinary, []byte{1, 2})
	_ = c.Write(ctx, websocket.MessageBinary, nil)
	_ = c.Write(ctx, websocket.MessageBinary, frame(id+50, "x"))
	_ = c.Write(ctx, websocket.MessageBinary, frame(id, "ok"))
	select {
	case <-term.wrote:
	case <-time.After(5 * time.Second):
		t.Fatal("valid frame not delivered")
	}
	term.mu.Lock()
	if len(term.writes) != 1 || string(term.writes[0]) != "ok" {
		t.Fatal(term.writes)
	}
	term.mu.Unlock()
	if m := rpc(t, ctx, c, `{"type":"ping","id":1}`); m["type"] != "pong" {
		t.Fatal(m)
	}
}
