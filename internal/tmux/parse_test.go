package tmux

import (
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestUnescape(t *testing.T) {
	cases := []struct{ in, want string }{
		{`echo hi\015\012`, "echo hi\r\n"},
		{`a\134b`, `a\b`},
		{`\033[1m`, "\x1b[1m"},
		{"caf\xc3\xa9 \xe2\x9c\x93", "caf\xc3\xa9 \xe2\x9c\x93"}, // high bytes pass through
		{`\377\200`, "\xff\x80"},
		{`bad\9zz\1`, `bad\9zz\1`},
		{`end\01`, `end\01`},
		{``, ``},
	}
	for _, c := range cases {
		if got := string(Unescape([]byte(c.in))); got != c.want {
			t.Errorf("Unescape(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func parseAll(t *testing.T, s string) []Message {
	t.Helper()
	r := NewReader(strings.NewReader(s))
	var out []Message
	for {
		m, err := r.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
}

func TestParseBlocks(t *testing.T) {
	in := "%begin 100 1 0\n%end 100 1 0\n" +
		"%begin 101 2 1\nline one\n\n%end 101 2 1\n" +
		"%begin 102 3 1\nunknown command: x\n%error 102 3 1\n"
	got := parseAll(t, in)
	if len(got) != 3 {
		t.Fatalf("got %d messages", len(got))
	}
	if r := got[0].Reply; r.Flags != 0 || r.Num != 1 || len(r.Lines) != 0 {
		t.Errorf("block 0: %+v", r)
	}
	if r := got[1].Reply; r.Flags != 1 || r.Err || !reflect.DeepEqual(r.Lines, []string{"line one", ""}) {
		t.Errorf("block 1: %+v", r)
	}
	if r := got[2].Reply; !r.Err || r.Lines[0] != "unknown command: x" {
		t.Errorf("block 2: %+v", r)
	}
}

func TestParseNotifications(t *testing.T) {
	in := "%output %0 echo hi\\015\\012\n" +
		"%session-changed $1 main\n" +
		"%window-pane-changed @1 %2\n" +
		"%exit server exited\n" +
		"%exit\n" +
		"%future-thing a b c\n"
	got := parseAll(t, in)
	if n := got[0].Note; n.Name != "output" || n.Pane != "%0" || string(n.Data) != "echo hi\r\n" {
		t.Errorf("output: %+v", n)
	}
	if n := got[1].Note; n.Name != "session-changed" || n.Args != "$1 main" {
		t.Errorf("session-changed: %+v", n)
	}
	if n := got[2].Note; n.Name != "window-pane-changed" || n.Args != "@1 %2" {
		t.Errorf("window-pane-changed: %+v", n)
	}
	if n := got[3].Note; n.Name != "exit" || n.Args != "server exited" {
		t.Errorf("exit: %+v", n)
	}
	if n := got[4].Note; n.Name != "exit" || n.Args != "" {
		t.Errorf("bare exit: %+v", n)
	}
	if n := got[5].Note; n.Name != "future-thing" || n.Args != "a b c" {
		t.Errorf("unknown: %+v", n)
	}
}

func TestParseLongBinaryLine(t *testing.T) {
	long := strings.Repeat("x", 200_000)
	got := parseAll(t, "%output %1 "+long+"\\000\xff\n")
	if n := got[0].Note; len(n.Data) != 200_002 || n.Data[200_000] != 0 || n.Data[200_001] != 0xff {
		t.Errorf("long line mangled (len %d)", len(n.Data))
	}
}

func TestParseTruncatedBlock(t *testing.T) {
	r := NewReader(strings.NewReader("%begin 1 1 1\npartial\n"))
	if _, err := r.Next(); err != io.ErrUnexpectedEOF {
		t.Errorf("err = %v", err)
	}
}
