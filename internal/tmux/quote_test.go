package tmux

import (
	"os/exec"
	"runtime"
	"testing"
)

func TestShellQuote(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs /bin/sh")
	}
	cases := []string{
		"", "plain", "has space", "it's", "'", "''", "a'b'c", `"dq" $HOME $(x) ` + "`x`",
		"a\x1fb\x1fc", "line1\nline2", `back\slash`, "*?[a-z]~", "-F", "#{session_id}\x1f#{session_name}",
	}
	for _, in := range cases {
		out, err := exec.Command("/bin/sh", "-c", "printf %s "+ShellQuote(in)).Output()
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if string(out) != in {
			t.Errorf("round trip %q -> %q (quoted %s)", in, out, ShellQuote(in))
		}
	}
	if got := ShellQuote(""); got != "''" {
		t.Errorf("empty = %s", got)
	}
}
