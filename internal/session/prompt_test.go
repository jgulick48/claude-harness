package session

import (
	"errors"
	"strings"
	"testing"
)

func TestCleanPrompt(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"hello", "hello"},
		{"a\r\nb\rc\n\n\n", "a\nb\nc"},
		{"a\tb\nc", "a\tb\nc"},
		{"x\x1b[201~y\x00z\x07\x7f", "x[201~yz"},
		{"é ü 日本", "é ü 日本"},
	} {
		got, err := CleanPrompt(c.in)
		if err != nil || got != c.want {
			t.Errorf("CleanPrompt(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	for _, in := range []string{"", "\n\r\n", "  \n", "\x1b\x00", strings.Repeat("a", MaxPromptBytes+1)} {
		if _, err := CleanPrompt(in); !errors.Is(err, ErrBadRequest) {
			t.Errorf("CleanPrompt(len %d) err = %v, want ErrBadRequest", len(in), err)
		}
	}
	if _, err := CleanPrompt(strings.Repeat("a", MaxPromptBytes)); err != nil {
		t.Errorf("max size: %v", err)
	}
}

func TestPromptAllowed(t *testing.T) {
	for _, c := range []struct {
		status string
		wait   bool
		ok     bool
	}{
		{ClaudeIdle, false, true}, {ClaudeIdle, true, true},
		{ClaudeWaiting, false, false}, {ClaudeWaiting, true, true},
		{ClaudeWorking, false, false}, {ClaudeWorking, true, false},
		{ClaudeUnknown, true, false}, {"", true, false}, {"bogus", true, false},
	} {
		err := PromptAllowed(c.status, c.wait)
		if (err == nil) != c.ok || (err != nil && !errors.Is(err, ErrBusy)) {
			t.Errorf("PromptAllowed(%q,%v) = %v", c.status, c.wait, err)
		}
	}
}
