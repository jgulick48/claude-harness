package tmux

import (
	"slices"
	"testing"
)

// A tmux command run with an inherited TMUX (core or tests started inside a
// tmux session) and no -L/-S would target the user's own server.
func TestTmuxEnvDropsTmuxVars(t *testing.T) {
	in := []string{"HOME=/h", "TMUX=/tmp/tmux-1000/default,123,0", "TMUX_PANE=%1", "TMUX_TMPDIR=/x", "PATH=/bin"}
	got := TmuxEnv(in)
	want := []string{"HOME=/h", "TMUX_TMPDIR=/x", "PATH=/bin"}
	if !slices.Equal(got, want) {
		t.Fatalf("TmuxEnv = %v, want %v", got, want)
	}
}
