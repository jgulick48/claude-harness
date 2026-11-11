package tmuxbackend

import (
	"context"
	"errors"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jgulick48/claude-harness/internal/session"
	"github.com/jgulick48/claude-harness/internal/tmux"
)

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"My Project":                   "My-Project",
		"  a  b  ":                     "a-b",
		"a/b.c:d":                      "a-b-c-d",
		"--x--":                        "x",
		"ok_name-1":                    "ok_name-1",
		"日本語":                          "",
		"é-x":                          "x",
		"a   ---   b":                  "a-b",
		strings.Repeat("a", 60):        strings.Repeat("a", 40),
		strings.Repeat("a", 39) + " b": strings.Repeat("a", 39),
	}
	for in, want := range cases {
		if got := sanitizeName(in); got != want {
			t.Errorf("sanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDefaultName(t *testing.T) {
	cases := map[string]string{
		"/home/u/my proj": "h-my-proj",
		"/":               "h-home",
		"/tmp/日本":         "h-home",
		"/a/b/":           "h-b",
	}
	for in, want := range cases {
		if got := defaultName(in); got != want {
			t.Errorf("defaultName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUniqueName(t *testing.T) {
	taken := map[string]bool{"a": true, "a-2": true, "a-3": true}
	if got := uniqueName("a", taken); got != "a-4" {
		t.Errorf("got %q", got)
	}
	if got := uniqueName("b", taken); got != "b" {
		t.Errorf("got %q", got)
	}
	long := strings.Repeat("x", 40)
	got := uniqueName(long, map[string]bool{long: true})
	if got != strings.Repeat("x", 38)+"-2" || len(got) != 40 {
		t.Errorf("long: %q", got)
	}
}

func TestSendKeysCommands(t *testing.T) {
	if got := sendKeysCommands("%3", nil); len(got) != 0 {
		t.Errorf("empty input: %v", got)
	}
	got := sendKeysCommands("%3", []byte("echo\r"))
	if !reflect.DeepEqual(got, []string{"send-keys -t '%3' -H 65 63 68 6f 0d"}) {
		t.Errorf("got %v", got)
	}
	data := make([]byte, 600)
	for i := range data {
		data[i] = byte(i)
	}
	got = sendKeysCommands("%0", data)
	if len(got) != 3 {
		t.Fatalf("chunks = %d", len(got))
	}
	var n int
	for i, c := range got {
		k := len(strings.Fields(c)) - 4 // send-keys -t %0 -H
		if k > 256 {
			t.Errorf("chunk %d has %d bytes", i, k)
		}
		n += k
	}
	if n != 600 || !strings.HasSuffix(got[2], " 57") {
		t.Errorf("total %d, last chunk %q", n, got[2])
	}
}

func TestBuildSeed(t *testing.T) {
	got := string(buildSeed([]string{"hist", "a", "b"}, false, 3, 1, 2))
	want := "hist\x1b[0m\r\na\x1b[0m\r\nb\x1b[0m\x1b[2;4H"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
	// Padded up to rows; alternate screen prefix.
	got = string(buildSeed([]string{"x"}, true, 0, 0, 3))
	want = "\x1b[?1049hx\x1b[0m\r\n\x1b[0m\r\n\x1b[0m\x1b[1;1H"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
	// Cursor row clamped to the screen.
	if got := string(buildSeed([]string{"x", "y"}, false, 0, 9, 2)); !strings.HasSuffix(got, "\x1b[2;1H") {
		t.Errorf("clamp: %q", got)
	}
}

type fakeRunner struct {
	out string
	err error
}

func (f fakeRunner) Output(context.Context, ...string) ([]byte, error) { return []byte(f.out), f.err }
func (f fakeRunner) Start(context.Context, ...string) (tmux.Proc, error) {
	return nil, errors.New("unsupported")
}

func TestHostVersions(t *testing.T) {
	cases := []struct {
		out, state, errMsg, ver string
	}{
		{"tmux 3.2a\n", session.HostConnected, "", "3.2a"},
		{"tmux 3.4\n", session.HostConnected, "", "3.4"},
		{"tmux 3.10\n", session.HostConnected, "", "3.10"},
		{"tmux 4.0\n", session.HostConnected, "", "4.0"},
		{"tmux next-3.5\n", session.HostConnected, "", "next-3.5"},
		{"tmux master\n", session.HostConnected, "", "master"},
		{"tmux 3.1c\n", session.HostUnavailable, "tmux 3.2 or newer required (found 3.1c)", "3.1c"},
		{"tmux 2.9\n", session.HostUnavailable, "tmux 3.2 or newer required (found 2.9)", "2.9"},
	}
	for _, c := range cases {
		b := newBackend("local", "This computer", fakeRunner{out: c.out}, Options{Home: "/h"})
		h := b.Host(context.Background())
		if h.State != c.state || h.Error != c.errMsg || h.TmuxVersion != c.ver || h.Home != "/h" || h.ID != "local" {
			t.Errorf("%q: %+v", c.out, h)
		}
	}
}

func TestHostMissingBinaryAndCache(t *testing.T) {
	b := newBackend("local", "x", fakeRunner{err: &tmux.Error{Err: fs.ErrNotExist}}, Options{})
	h := b.Host(context.Background())
	if h.State != session.HostUnavailable || h.Error != "tmux not found" {
		t.Errorf("%+v", h)
	}

	r := &countingRunner{out: "tmux 3.3\n"}
	b = newBackend("local", "x", r, Options{})
	b.Host(context.Background())
	b.Host(context.Background())
	if r.n != 1 {
		t.Errorf("probe ran %d times, want 1 (cached)", r.n)
	}
	b.hostAt = time.Now().Add(-time.Minute)
	b.Host(context.Background())
	if r.n != 2 {
		t.Errorf("cache did not expire")
	}
}

type countingRunner struct {
	fakeRunner
	out string
	n   int
}

func (c *countingRunner) Output(context.Context, ...string) ([]byte, error) {
	c.n++
	return []byte(c.out), nil
}

func TestExpandDirAndBadCommand(t *testing.T) {
	b := newBackend("local", "x", fakeRunner{}, Options{Home: "/home/u"})
	for in, want := range map[string]string{"": "/home/u", "~": "/home/u", "~/a/b": "/home/u/a/b", "/etc": "/etc"} {
		if got := b.expandDir(in); got != want {
			t.Errorf("expandDir(%q) = %q, want %q", in, got, want)
		}
	}
	_, err := b.Create(context.Background(), session.CreateOptions{Dir: "/", Command: "rm"})
	if !errors.Is(err, session.ErrBadRequest) {
		t.Errorf("err = %v", err)
	}
}
