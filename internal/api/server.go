// Package api implements the core's HTTP and WebSocket endpoints
// (see docs/CORE_PROTOCOL.md).
package api

import (
	"context"
	"crypto/subtle"
	"encoding/binary"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"

	"github.com/jgulick48/claude-harness/internal/session"
	"github.com/jgulick48/claude-harness/internal/usagehist"
	"github.com/jgulick48/claude-harness/internal/version"
)

const readLimit = 4 << 20

type server struct {
	token  string
	logger *slog.Logger
	hm     session.HostManager
	recent session.RecentDirs // may be nil
	usage  UsageHistory       // may be nil
}

// UsageHistory aggregates and caches the token usage read from a host's
// transcripts (internal/usagehist.Store implements it).
type UsageHistory interface {
	Scan(ctx context.Context, host string, r session.TranscriptReader) (usagehist.Result, error)
	Forget(host string)
}

// Option configures NewServer.
type Option func(*server)

// WithUsageHistory enables usage.history; without it the method answers with
// an empty history.
func WithUsageHistory(u UsageHistory) Option { return func(s *server) { s.usage = u } }

// NewServer returns the handler serving /healthz and /ws, guarded by token.
// hm owns the hosts; the API follows its changes while connections are open.
// recent remembers directories used to create sessions; nil disables recording
// (suggestions then carry an empty recent list).
func NewServer(token string, logger *slog.Logger, hm session.HostManager, recent session.RecentDirs, opts ...Option) http.Handler {
	s := &server{token: token, logger: logger, hm: hm, recent: recent}
	for _, o := range opts {
		o(s)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /ws", s.ws)
	return mux
}

func (s *server) tokenOK(got string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) == 1
}

func (s *server) healthz(w http.ResponseWriter, r *http.Request) {
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || !s.tokenOK(got) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok": true, "version": version.Version, "protocol": version.Protocol,
	})
}

type hello struct {
	Type     string `json:"type"`
	Version  string `json:"version"`
	Protocol int    `json:"protocol"`
}

type pong struct {
	Type string `json:"type"`
	ID   *int64 `json:"id"`
}

type errMsg struct {
	Type    string `json:"type"`
	ID      *int64 `json:"id,omitempty"`
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

type resultMsg struct {
	Type   string `json:"type"`
	ID     int64  `json:"id"`
	Result any    `json:"result"`
}

// Error codes.
const (
	codeBadRequest  = "bad_request"
	codeNotFound    = "not_found"
	codeUnavailable = "unavailable"
	codeInternal    = "internal"
)

type apiError struct{ code, msg string }

func (e *apiError) Error() string { return e.msg }

func badReq(msg string) error   { return &apiError{codeBadRequest, msg} }
func notFound(msg string) error { return &apiError{codeNotFound, msg} }

func errCode(err error) string {
	var ae *apiError
	switch {
	case errors.As(err, &ae):
		return ae.code
	case errors.Is(err, session.ErrNotFound):
		return codeNotFound
	case errors.Is(err, session.ErrBadRequest):
		return codeBadRequest
	case errors.Is(err, session.ErrUnavailable):
		return codeUnavailable
	}
	return codeInternal
}

// stream is one attached terminal on a connection.
type stream struct {
	id       uint32
	term     session.Terminal
	detached atomic.Bool // UI-requested detach (or connection teardown): no term.closed
}

// conn is the per-WebSocket-connection state.
type conn struct {
	s      *server
	c      *websocket.Conn
	ctx    context.Context
	logger *slog.Logger

	ev *events

	mu      sync.Mutex
	closed  bool
	nextID  uint32
	streams map[uint32]*stream
}

func (s *server) ws(w http.ResponseWriter, r *http.Request) {
	// Reject before upgrading.
	if !s.tokenOK(r.URL.Query().Get("token")) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// Origin is deliberately not checked: the Electron renderer's origin varies
	// (http://localhost:<port> in dev, file:// when packaged). The token is the
	// access control.
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		s.logger.Warn("websocket accept failed", "err", err)
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(readLimit)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	cn := &conn{s: s, c: c, ctx: ctx, logger: s.logger, nextID: 1, streams: map[uint32]*stream{}}
	defer cn.teardown()

	if err := cn.sendJSON(hello{"hello", version.Version, version.Protocol}); err != nil {
		return
	}
	cn.startEvents()
	defer cn.stopEvents()

	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if typ == websocket.MessageBinary {
			cn.handleBinary(data)
			continue
		}
		cn.handleText(data)
	}
}

// Event delivery. HostManager and Backend subscriber callbacks may run while
// the publisher holds its own locks, so they must never write to the socket: a
// slow client would stall the publisher. Callbacks only record the latest value
// under evMu and wake the connection's single pump goroutine, which writes.
// Only the newest hosts list and the newest sessions list per host matter, so
// anything not yet sent is replaced (latest wins). The callbacks never block,
// and the pump never holds evMu while writing or calling into a publisher.
//
// The pump also keeps the per-host session subscriptions in step with the host
// set: hosts added mid-connection are subscribed (their initial
// sessions.changed is sent), removed hosts are unsubscribed.
type events struct {
	mu       sync.Mutex
	hosts    []session.Host
	hostsDue bool
	sessions map[string][]session.Session
	order    []string // hosts with pending sessions, first-change order
	wake     chan struct{}
	unsubHM  func()
	subs     map[string]*hostSub
	pumpDone chan struct{}
	stopOnce sync.Once
	stopped  chan struct{}
}

type hostSub struct {
	b     session.Backend
	unsub func()
}

func (cn *conn) startEvents() {
	ev := &events{sessions: map[string][]session.Session{}, wake: make(chan struct{}, 1),
		subs: map[string]*hostSub{}, pumpDone: make(chan struct{}), stopped: make(chan struct{})}
	cn.ev = ev
	go cn.pump()
	ev.unsubHM = cn.s.hm.Subscribe(func(hosts []session.Host) {
		ev.mu.Lock()
		ev.hosts, ev.hostsDue = hosts, true
		ev.mu.Unlock()
		ev.poke()
	})
}

func (ev *events) poke() {
	select {
	case ev.wake <- struct{}{}:
	default:
	}
}

func (cn *conn) stopEvents() {
	ev := cn.ev
	ev.unsubHM()
	close(ev.stopped)
	<-ev.pumpDone
	for _, hs := range ev.subs {
		hs.unsub()
	}
}

func (cn *conn) pump() {
	ev := cn.ev
	defer close(ev.pumpDone)
	for {
		select {
		case <-cn.ctx.Done():
			return
		case <-ev.stopped:
			return
		case <-ev.wake:
		}
		cn.reconcile()
		ev.mu.Lock()
		var hosts []session.Host
		due := ev.hostsDue
		if due {
			hosts = ev.hosts
			ev.hostsDue = false
		}
		order := ev.order
		pending := ev.sessions
		ev.order, ev.sessions = nil, map[string][]session.Session{}
		ev.mu.Unlock()
		if due {
			if hosts == nil {
				hosts = []session.Host{}
			}
			if cn.sendJSON(map[string]any{"type": "event", "event": "hosts.changed", "hosts": hosts}) != nil {
				return
			}
		}
		for _, id := range order {
			// Skip hosts removed since the list was recorded.
			if _, ok := ev.subs[id]; !ok {
				continue
			}
			if cn.sendJSON(sessionsChanged(id, pending[id])) != nil {
				return
			}
		}
	}
}

// reconcile subscribes to backends that appeared and unsubscribes from ones
// that went away (or were replaced). Runs only on the pump goroutine.
func (cn *conn) reconcile() {
	ev := cn.ev
	want := map[string]session.Backend{}
	for _, b := range cn.s.hm.Backends() {
		want[b.ID()] = b
	}
	for id, hs := range ev.subs {
		if b, ok := want[id]; !ok || b != hs.b {
			hs.unsub()
			delete(ev.subs, id)
			ev.mu.Lock()
			delete(ev.sessions, id)
			ev.order = slices.DeleteFunc(ev.order, func(o string) bool { return o == id })
			ev.mu.Unlock()
		}
	}
	for id, b := range want {
		if _, ok := ev.subs[id]; ok {
			continue
		}
		hs := &hostSub{b: b}
		ev.subs[id] = hs
		// Subscribe calls back immediately; the callback only records.
		hs.unsub = b.Subscribe(func(list []session.Session) {
			ev.mu.Lock()
			if _, pending := ev.sessions[id]; !pending {
				ev.order = append(ev.order, id)
			}
			ev.sessions[id] = list
			ev.mu.Unlock()
			ev.poke()
		})
	}
}

func sessionsChanged(host string, list []session.Session) map[string]any {
	if list == nil {
		list = []session.Session{}
	}
	return map[string]any{"type": "event", "event": "sessions.changed", "host": host, "sessions": list}
}

// sendJSON writes one text frame. A write failure closes the connection.
func (cn *conn) sendJSON(v any) error {
	data, err := json.Marshal(v)
	if err == nil {
		err = cn.c.Write(cn.ctx, websocket.MessageText, data)
	}
	if err != nil {
		cn.c.CloseNow()
	}
	return err
}

func (cn *conn) sendError(id *int64, code, msg string) {
	cn.sendJSON(errMsg{Type: "error", ID: id, Message: msg, Code: code})
}

// teardown closes every stream's terminal; called once when the connection ends.
func (cn *conn) teardown() {
	cn.mu.Lock()
	cn.closed = true
	streams := cn.streams
	cn.streams = map[uint32]*stream{}
	cn.mu.Unlock()
	for _, st := range streams {
		st.detached.Store(true)
		_ = st.term.Close()
	}
}

func (cn *conn) handleBinary(data []byte) {
	if len(data) < 4 {
		return
	}
	id := binary.BigEndian.Uint32(data)
	cn.mu.Lock()
	st := cn.streams[id]
	cn.mu.Unlock()
	if st == nil {
		return
	}
	if err := st.term.Write(data[4:]); err != nil {
		cn.logger.Debug("terminal write failed", "stream", id, "err", err)
	}
}

// parseID returns the id if present and an integer.
func parseID(raw json.RawMessage) *int64 {
	if len(raw) == 0 {
		return nil
	}
	var id int64
	if err := json.Unmarshal(raw, &id); err != nil {
		return nil
	}
	return &id
}

type params struct {
	Host        string          `json:"host"`
	Session     string          `json:"session"`
	Dir         string          `json:"dir"`
	Name        string          `json:"name"`
	Target      string          `json:"target"`
	Fingerprint string          `json:"fingerprint"`
	Command     string          `json:"command"`
	RunAs       string          `json:"runAs"`
	Cols        json.RawMessage `json:"cols"`
	Rows        json.RawMessage `json:"rows"`
	Stream      json.RawMessage `json:"stream"`
	Prefix      string          `json:"prefix"`
	Usage       json.RawMessage `json:"usage"`
	Color       string          `json:"color"`
}

var methods = map[string]bool{
	"hosts.list": true, "hosts.add": true, "hosts.remove": true, "hosts.connect": true,
	"hosts.trustKey": true, "sessions.list": true, "sessions.create": true,
	"sessions.kill": true, "sessions.rename": true, "sessions.setColor": true,
	"term.attach": true, "term.resize": true, "term.detach": true,
	"hosts.configHosts": true, "remote.completeDir": true, "remote.suggestDirs": true,
	"remote.pinDir": true, "remote.unpinDir": true, "usage.history": true,
	"hosts.installHooks": true, "hosts.removeHooks": true,
	"hosts.check": true,
}

func (cn *conn) handleText(data []byte) {
	var m struct {
		Type string          `json:"type"`
		ID   json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		cn.sendError(nil, "", "invalid JSON")
		return
	}
	id := parseID(m.ID)
	switch {
	case m.Type == "ping":
		cn.sendJSON(pong{"pong", id})
	case methods[m.Type]:
		if id == nil {
			cn.sendError(nil, codeBadRequest, "missing or invalid id")
			return
		}
		go cn.dispatch(m.Type, *id, data)
	default:
		cn.sendError(id, "", "unknown message type: "+m.Type)
	}
}

func (cn *conn) dispatch(method string, id int64, data []byte) {
	var p params
	if err := json.Unmarshal(data, &p); err != nil {
		cn.sendError(&id, codeBadRequest, "invalid params: "+err.Error())
		return
	}
	res, err := cn.call(method, id, &p)
	if err != nil {
		cn.sendError(&id, errCode(err), err.Error())
		return
	}
	if err := cn.sendJSON(resultMsg{"result", id, res}); err != nil {
		return
	}
	if st, ok := res.(*attachResult); ok {
		// The result is on the wire; output frames may now flow.
		go cn.drain(st.st)
	}
}

type attachResult struct {
	Stream uint32 `json:"stream"`
	st     *stream
}

func (a *attachResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Stream uint32 `json:"stream"`
	}{a.Stream})
}

func dim(raw json.RawMessage, name string) (int, error) {
	var n int64
	if len(raw) == 0 || json.Unmarshal(raw, &n) != nil || n < 1 || n > 1000 {
		return 0, badReq(name + " must be an integer between 1 and 1000")
	}
	return int(n), nil
}

func need(name, v string) error {
	if v == "" {
		return badReq(name + " is required")
	}
	return nil
}

func (cn *conn) backend(host string) (session.Backend, error) {
	if err := need("host", host); err != nil {
		return nil, err
	}
	b, ok := cn.s.hm.Backend(host)
	if !ok {
		return nil, notFound("unknown host: " + host)
	}
	return b, nil
}

func (cn *conn) streamParam(raw json.RawMessage) (*stream, error) {
	var n int64
	if len(raw) == 0 || json.Unmarshal(raw, &n) != nil || n < 1 || n > math.MaxUint32 {
		return nil, badReq("stream must be a positive integer")
	}
	cn.mu.Lock()
	st := cn.streams[uint32(n)]
	cn.mu.Unlock()
	if st == nil {
		return nil, notFound("unknown stream")
	}
	return st, nil
}

func (cn *conn) call(method string, id int64, p *params) (any, error) {
	ctx := cn.ctx
	if method == "hosts.configHosts" {
		return cn.handleConfigHosts(ctx) // confighosts.go
	}
	if method == "hosts.installHooks" || method == "hosts.removeHooks" {
		return cn.handleHooks(ctx, method, p) // hooks.go
	}
	if method == "hosts.check" {
		return cn.handleCheck(ctx, p) // check.go
	}
	if strings.HasPrefix(method, "hosts.") {
		return cn.hostsCall(method, p)
	}
	if method == "term.resize" || method == "term.detach" {
		st, err := cn.streamParam(p.Stream)
		if err != nil {
			return nil, err
		}
		if method == "term.detach" {
			cn.mu.Lock()
			delete(cn.streams, st.id)
			cn.mu.Unlock()
			st.detached.Store(true)
			_ = st.term.Close()
			return nil, nil
		}
		cols, err := dim(p.Cols, "cols")
		if err != nil {
			return nil, err
		}
		rows, err := dim(p.Rows, "rows")
		if err != nil {
			return nil, err
		}
		return nil, st.term.Resize(cols, rows)
	}

	b, err := cn.backend(p.Host)
	if err != nil {
		return nil, err
	}
	switch method {
	case "remote.completeDir":
		return cn.handleCompleteDir(ctx, b, p) // remote.go
	case "remote.suggestDirs":
		return cn.handleSuggestDirs(ctx, b, p) // remote.go
	case "remote.pinDir":
		return cn.handlePinDir(p) // remote.go
	case "remote.unpinDir":
		return cn.handleUnpinDir(p) // remote.go
	case "usage.history":
		return cn.handleUsageHistory(ctx, b, p) // usagehistory.go
	case "sessions.list":
		list, err := b.List(ctx)
		if err != nil {
			return nil, err
		}
		if list == nil {
			list = []session.Session{}
		}
		return list, nil
	case "sessions.create":
		sess, err := b.Create(ctx, session.CreateOptions{Dir: p.Dir, Name: p.Name, Command: p.Command, RunAs: p.RunAs})
		if err != nil {
			return nil, err
		}
		if cn.s.recent != nil {
			cn.s.recent.Add(p.Host, p.Dir)
		}
		return sess, nil
	case "sessions.kill":
		if err := need("session", p.Session); err != nil {
			return nil, err
		}
		return nil, b.Kill(ctx, p.Session)
	case "sessions.rename":
		if err := need("session", p.Session); err != nil {
			return nil, err
		}
		return nil, b.Rename(ctx, p.Session, p.Name)
	case "sessions.setColor":
		if err := need("session", p.Session); err != nil {
			return nil, err
		}
		return nil, b.SetColor(ctx, p.Session, p.Color)
	case "term.attach":
		if err := need("session", p.Session); err != nil {
			return nil, err
		}
		cols, err := dim(p.Cols, "cols")
		if err != nil {
			return nil, err
		}
		rows, err := dim(p.Rows, "rows")
		if err != nil {
			return nil, err
		}
		term, err := b.Attach(ctx, p.Session, cols, rows)
		if err != nil {
			return nil, err
		}
		cn.mu.Lock()
		if cn.closed {
			cn.mu.Unlock()
			_ = term.Close()
			return nil, errors.New("connection closed")
		}
		st := &stream{id: cn.nextID, term: term}
		cn.nextID++
		cn.streams[st.id] = st
		cn.mu.Unlock()
		return &attachResult{Stream: st.id, st: st}, nil
	}
	return nil, errors.New("unhandled method")
}

func (cn *conn) hostsCall(method string, p *params) (any, error) {
	ctx := cn.ctx
	hm := cn.s.hm
	switch method {
	case "hosts.list":
		bs := hm.Backends()
		hosts := make([]session.Host, 0, len(bs))
		for _, b := range bs {
			hosts = append(hosts, b.Host(ctx))
		}
		return hosts, nil
	case "hosts.add":
		if err := need("target", p.Target); err != nil {
			return nil, err
		}
		return orErr2(hm.Add(ctx, p.Target, p.Name))
	case "hosts.remove":
		if err := need("host", p.Host); err != nil {
			return nil, err
		}
		if err := hm.Remove(ctx, p.Host); err != nil {
			return nil, err
		}
		if cn.s.recent != nil {
			cn.s.recent.Forget(p.Host)
		}
		if cn.s.usage != nil {
			cn.s.usage.Forget(p.Host)
		}
		return nil, nil
	case "hosts.connect":
		if err := need("host", p.Host); err != nil {
			return nil, err
		}
		return nil, hm.Connect(ctx, p.Host)
	case "hosts.trustKey":
		if err := need("host", p.Host); err != nil {
			return nil, err
		}
		if err := need("fingerprint", p.Fingerprint); err != nil {
			return nil, err
		}
		return nil, hm.TrustHostKey(ctx, p.Host, p.Fingerprint)
	}
	return nil, errors.New("unhandled method")
}

func orErr2(v session.Host, err error) (any, error) {
	if err != nil {
		return nil, err
	}
	return v, nil
}

// drain forwards terminal output as binary frames until Output closes.
func (cn *conn) drain(st *stream) {
	hdr := [4]byte{}
	binary.BigEndian.PutUint32(hdr[:], st.id)
	for chunk := range st.term.Output() {
		frame := make([]byte, 0, 4+len(chunk))
		frame = append(frame, hdr[:]...)
		frame = append(frame, chunk...)
		if err := cn.c.Write(cn.ctx, websocket.MessageBinary, frame); err != nil {
			cn.c.CloseNow()
			// Keep draining so the terminal's producer isn't blocked; teardown closes it.
		}
	}
	cn.mu.Lock()
	delete(cn.streams, st.id)
	cn.mu.Unlock()
	if st.detached.Load() {
		return
	}
	reason := "closed"
	if err := st.term.Err(); err != nil {
		reason = err.Error()
	}
	cn.sendJSON(map[string]any{"type": "event", "event": "term.closed", "stream": st.id, "reason": reason})
}
