package api

import (
	"fmt"
	"strings"
	"testing"
)

func TestSendPrompt(t *testing.T) {
	b := newFakeBackend()
	b.statuses = map[string]string{"$idle": "idle", "$wait": "waiting", "$work": "working", "$none": "", "$unk": "unknown"}
	c, ctx := connect(t, b)
	req := func(id int, sess, text, extra string) map[string]any {
		return rpc(t, ctx, c, fmt.Sprintf(`{"type":"sessions.sendPrompt","id":%d,"host":"local","session":%q,"text":%q%s}`, id, sess, text, extra))
	}

	if m := req(1, "$idle", "hi\r\nthere\n", ""); m["type"] != "result" || m["result"] != nil {
		t.Fatal(m)
	}
	wantErr(t, req(2, "$work", "hi", ""), "busy")
	wantErr(t, req(3, "$work", "hi", `,"allowWaiting":true`), "busy")
	wantErr(t, req(4, "$wait", "hi", ""), "busy")
	if m := req(5, "$wait", "hi", `,"allowWaiting":true`); m["type"] != "result" {
		t.Fatal(m)
	}
	wantErr(t, req(6, "$none", "hi", `,"allowWaiting":true`), "busy")
	wantErr(t, req(7, "$unk", "hi", ""), "busy")
	wantErr(t, req(8, "$idle", "  \n", ""), "bad_request")
	wantErr(t, req(9, "$idle", strings.Repeat("x", 16<<10+1), ""), "bad_request")
	wantErr(t, req(10, "$nope", "hi", ""), "not_found")
	wantErr(t, rpc(t, ctx, c, `{"type":"sessions.sendPrompt","id":11,"host":"local","text":"hi"}`), "bad_request")

	b.mu.Lock()
	defer b.mu.Unlock()
	if got := fmt.Sprint(b.calls); got != "[prompt $idle|hi\nthere|false prompt $wait|hi|true]" {
		t.Fatal(got)
	}
}
