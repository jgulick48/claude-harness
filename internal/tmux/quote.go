package tmux

import "strings"

// Quote quotes one argument for a tmux command line. Inside single quotes tmux
// takes everything literally, so an embedded quote is written as '\” (close,
// escaped quote, reopen). The result must not be used with newlines: a command
// line is one line in control mode.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ShellQuote quotes s as one POSIX shell word using single quotes. Every byte
// is literal inside single quotes, including the \x1f field separator and
// newlines. An embedded single quote is written as close, backslash-quote,
// reopen; the empty string becomes an empty quoted pair.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
