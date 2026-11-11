package tmux

import "strings"

// Quote quotes one argument for a tmux command line. Inside single quotes tmux
// takes everything literally, so an embedded quote is written as '\” (close,
// escaped quote, reopen). The result must not be used with newlines: a command
// line is one line in control mode.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
