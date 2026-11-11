// Package api implements the core's HTTP and WebSocket endpoints
// (see docs/CORE_PROTOCOL.md).
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/jgulick48/claude-harness/internal/version"
)

type server struct {
	token  string
	logger *slog.Logger
}

// NewServer returns the handler serving /healthz and /ws, guarded by token.
func NewServer(token string, logger *slog.Logger) http.Handler {
	s := &server{token: token, logger: logger}
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

// inbound is any client message; ID is nil when absent.
type inbound struct {
	Type string `json:"type"`
	ID   *int64 `json:"id"`
}

type pong struct {
	Type string `json:"type"`
	ID   *int64 `json:"id"`
}

type errMsg struct {
	Type    string `json:"type"`
	ID      *int64 `json:"id,omitempty"`
	Message string `json:"message"`
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

	ctx := r.Context()
	if err := wsjson.Write(ctx, c, hello{"hello", version.Version, version.Protocol}); err != nil {
		return
	}
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			continue // binary frames are reserved for terminal data (M1+)
		}
		if err := s.handle(ctx, c, data); err != nil {
			return
		}
	}
}

func (s *server) handle(ctx context.Context, c *websocket.Conn, data []byte) error {
	var m inbound
	if err := json.Unmarshal(data, &m); err != nil {
		return wsjson.Write(ctx, c, errMsg{Type: "error", Message: "invalid JSON"})
	}
	if m.Type == "ping" {
		return wsjson.Write(ctx, c, pong{"pong", m.ID})
	}
	return wsjson.Write(ctx, c, errMsg{Type: "error", ID: m.ID, Message: "unknown message type: " + m.Type})
}
