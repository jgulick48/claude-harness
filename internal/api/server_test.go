package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

const tok = "secret"

func newTS(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(NewServer(tok, slog.New(slog.NewTextHandler(io.Discard, nil)), newFakeHM(), nil))
	t.Cleanup(ts.Close)
	return ts
}

func TestHealthz(t *testing.T) {
	ts := newTS(t)
	tests := []struct {
		name, auth string
		want       int
	}{
		{"ok", "Bearer " + tok, 200},
		{"missing", "", 401},
		{"wrong", "Bearer nope", 401},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", ts.URL+"/healthz", nil)
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Fatalf("status %d, want %d", resp.StatusCode, tc.want)
			}
			if tc.want == 200 {
				var b map[string]any
				if err := json.NewDecoder(resp.Body).Decode(&b); err != nil {
					t.Fatal(err)
				}
				if b["ok"] != true || b["protocol"] != float64(5) {
					t.Fatalf("body %v", b)
				}
			}
		})
	}
}

func wsURL(ts *httptest.Server, q string) string {
	return "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws" + q
}

func TestWSAuthRejected(t *testing.T) {
	ts := newTS(t)
	for _, q := range []string{"", "?token=wrong"} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, resp, err := websocket.Dial(ctx, wsURL(ts, q), nil)
		cancel()
		if err == nil {
			t.Fatalf("%q: expected dial error", q)
		}
		if resp == nil || resp.StatusCode != 401 {
			t.Fatalf("%q: resp %v, want 401", q, resp)
		}
	}
}

func dial(t *testing.T, ts *httptest.Server) (*websocket.Conn, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	c, _, err := websocket.Dial(ctx, wsURL(ts, "?token="+tok), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	return c, ctx
}

func readMsg(t *testing.T, ctx context.Context, c *websocket.Conn) map[string]any {
	t.Helper()
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func send(t *testing.T, ctx context.Context, c *websocket.Conn, s string) {
	t.Helper()
	if err := c.Write(ctx, websocket.MessageText, []byte(s)); err != nil {
		t.Fatal(err)
	}
}

func TestWSMessages(t *testing.T) {
	ts := newTS(t)
	c, ctx := dial(t, ts)

	h := readMsg(t, ctx, c)
	if h["type"] != "hello" || h["protocol"] != float64(5) || h["version"] == "" {
		t.Fatalf("hello: %v", h)
	}
	if m := readMsg(t, ctx, c); m["event"] != "hosts.changed" || len(m["hosts"].([]any)) != 0 {
		t.Fatalf("hosts.changed: %v", m)
	}

	send(t, ctx, c, `{"type":"ping","id":42}`)
	if m := readMsg(t, ctx, c); m["type"] != "pong" || m["id"] != float64(42) {
		t.Fatalf("pong: %v", m)
	}

	send(t, ctx, c, `{"type":"foo","id":7}`)
	m := readMsg(t, ctx, c)
	if m["type"] != "error" || m["id"] != float64(7) || m["message"] != "unknown message type: foo" {
		t.Fatalf("unknown w/ id: %v", m)
	}

	send(t, ctx, c, `{"type":"foo"}`)
	m = readMsg(t, ctx, c)
	if _, has := m["id"]; has || m["type"] != "error" || m["message"] != "unknown message type: foo" {
		t.Fatalf("unknown w/o id: %v", m)
	}

	send(t, ctx, c, `{not json`)
	m = readMsg(t, ctx, c)
	if m["type"] != "error" || m["message"] != "invalid JSON" {
		t.Fatalf("invalid: %v", m)
	}

	// Connection still usable.
	send(t, ctx, c, `{"type":"ping","id":1}`)
	if m := readMsg(t, ctx, c); m["type"] != "pong" || m["id"] != float64(1) {
		t.Fatalf("pong after error: %v", m)
	}
}
