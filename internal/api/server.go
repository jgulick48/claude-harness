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
	"strings"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"

	"github.com/jgulick48/claude-harness/internal/session"
	"github.com/jgulick48/claude-harness/internal/version"
)

const readLimit = 4 << 20

type server struct {
	token    string
	logger   *slog.Logger
	backends []session.Backend
	byID     map[string]session.Backend
}

// NewServer returns the handler serving /healthz and /ws, guarded by token.
func NewServer(token string, logger *slog.Logger, backends []session.Backend) http.Handler {
	s := &server{token: token, logger: logger, backends: backends, byID: map[string]session.Backend{}}
	for _, b := range backends {
		s.byID[b.ID()] = b
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
	for _, b := range s.backends {
		id := b.ID()
		unsub := b.Subscribe(func(list []session.Session) {
			cn.sendJSON(sessionsChanged(id, list))
		})
		defer unsub()
	}

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
	Host    string          `json:"host"`
	Session string          `json:"session"`
	Dir     string          `json:"dir"`
	Name    string          `json:"name"`
	Command string          `json:"command"`
	Cols    json.RawMessage `json:"cols"`
	Rows    json.RawMessage `json:"rows"`
	Stream  json.RawMessage `json:"stream"`
}

var methods = map[string]bool{
	"hosts.list": true, "sessions.list": true, "sessions.create": true,
	"sessions.kill": true, "sessions.rename": true,
	"term.attach": true, "term.resize": true, "term.detach": true,
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
	b, ok := cn.s.byID[host]
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
	if method == "hosts.list" {
		hosts := make([]session.Host, 0, len(cn.s.backends))
		for _, b := range cn.s.backends {
			hosts = append(hosts, b.Host(ctx))
		}
		return hosts, nil
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
		return orErr(b.Create(ctx, session.CreateOptions{Dir: p.Dir, Name: p.Name, Command: p.Command}))
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

func orErr(v session.Session, err error) (any, error) {
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
