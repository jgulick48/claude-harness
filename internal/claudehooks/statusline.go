package claudehooks

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/jgulick48/claude-harness/internal/session"
)

// StatusLineVersion is the status line wrapper's version; bump it whenever
// StatusLineScript changes so installed copies show up as outdated.
const StatusLineVersion = 1

// statusLineScript wraps the user's status line command (kept in
// statusline-orig next to it). Claude Code feeds the status line command a
// JSON object on stdin, including the rate limits the hooks never see. The
// script records that JSON per tmux pane, then runs the original command with
// the same input and passes its output and exit status through. The recording
// writes nothing to stdout: stdout is what Claude displays.
const statusLineScript = `#!/bin/sh
# claude-harness-statusline v1
# Claude Code status line wrapper: records the JSON Claude passes on stdin in
# <dir>/usage/<tmux server pid>-<pane number>, then runs the user's original
# status line command (from <dir>/statusline-orig), if any.
input=$(cat)
dir=$(dirname "$0")

record() {
	[ -n "$TMUX" ] && [ -n "$TMUX_PANE" ] || return 0
	t=${TMUX%,*}
	pid=${t##*,}
	case "$TMUX_PANE" in %*) ;; *) return 0 ;; esac
	pane=${TMUX_PANE#%}
	case "$pid" in '' | *[!0-9]*) return 0 ;; esac
	case "$pane" in '' | *[!0-9]*) return 0 ;; esac
	mkdir -p "$dir/usage" || return 0
	f="$dir/usage/$pid-$pane"
	line=$(printf '%s' "$input" | tr -d '\r\n')
	printf '%s\t%s\n' "$(date +%s)" "$line" >"$f.$$" && mv -f "$f.$$" "$f" || rm -f "$f.$$"
}
record >/dev/null 2>&1

if [ -s "$dir/statusline-orig" ]; then
	printf '%s' "$input" | sh -c "$(cat "$dir/statusline-orig")"
else
	exit 0
fi
`

// StatusLineScript returns the status line wrapper script.
func StatusLineScript() []byte { return []byte(statusLineScript) }

// StatusLineCommand returns the status line command registered for the script
// at scriptPath (run through sh, the path single-quoted so spaces are safe).
func StatusLineCommand(scriptPath string) string { return "sh " + Command(scriptPath) }

// statusLineOf returns settings["statusLine"] as an object (nil when absent
// or null). Anything else is an error: we must not clobber it.
func statusLineOf(m obj) (obj, error) {
	v, ok := m["statusLine"]
	if !ok || v == nil {
		return nil, nil
	}
	sl, ok := v.(obj)
	if !ok {
		return nil, fmt.Errorf("invalid settings: \"statusLine\" is not an object")
	}
	return sl, nil
}

func slCommand(sl obj) string {
	c, _ := sl["command"].(string)
	return c
}

func isOurStatusLine(sl obj, scriptPath string) bool {
	c := slCommand(sl)
	return c != "" && strings.Contains(c, scriptPath)
}

// MergeStatusLine registers our wrapper as the status line command. orig is
// the command that was there before (hadOrig reports whether there was one);
// the caller must store it (statusline-orig) so it can be restored and run.
// If the status line is already ours, already is true, orig is meaningless
// and the settings come back unchanged (an out-of-date command of ours is
// rewritten). Other statusLine keys such as padding are kept. A statusLine
// that isn't an object is an error.
func MergeStatusLine(settings []byte, scriptPath string) (out []byte, orig string, hadOrig, already bool, err error) {
	m, err := parse(settings)
	if err != nil {
		return nil, "", false, false, err
	}
	sl, err := statusLineOf(m)
	if err != nil {
		return nil, "", false, false, err
	}
	ours := StatusLineCommand(scriptPath)
	if sl != nil && isOurStatusLine(sl, scriptPath) {
		if t, _ := sl["type"].(string); slCommand(sl) == ours && t == "command" {
			return settings, "", false, true, nil
		}
		sl["type"], sl["command"] = "command", ours
		out, err = marshal(m)
		return out, "", false, true, err
	}
	if sl == nil {
		sl = obj{}
	} else {
		orig = slCommand(sl)
		hadOrig = orig != ""
	}
	sl["type"], sl["command"] = "command", ours
	m["statusLine"] = sl
	out, err = marshal(m)
	return out, orig, hadOrig, false, err
}

// UnmergeStatusLine undoes MergeStatusLine when the status line is ours:
// with hadOrig the original command is restored (other keys kept), otherwise
// the statusLine key is removed. A status line that isn't ours is left alone.
func UnmergeStatusLine(settings []byte, scriptPath, orig string, hadOrig bool) ([]byte, error) {
	m, err := parse(settings)
	if err != nil {
		return nil, err
	}
	sl, err := statusLineOf(m)
	if err != nil {
		return nil, err
	}
	if sl == nil || !isOurStatusLine(sl, scriptPath) {
		return settings, nil
	}
	if hadOrig {
		sl["command"] = orig
	} else {
		delete(m, "statusLine")
	}
	return marshal(m)
}

// StatusLineStatus classifies the status line install: session.HooksInstalled
// when our command is registered and scriptBody equals StatusLineScript();
// HooksOutdated when ours is registered but the command or script differs or
// is missing; HooksMissing when the status line isn't ours; HooksUnknown when
// the settings can't be parsed.
func StatusLineStatus(settings, scriptBody []byte, scriptPath string) string {
	m, err := parse(settings)
	if err != nil {
		return session.HooksUnknown
	}
	sl, err := statusLineOf(m)
	if err != nil {
		return session.HooksUnknown
	}
	if sl == nil || !isOurStatusLine(sl, scriptPath) {
		return session.HooksMissing
	}
	if t, _ := sl["type"].(string); t == "command" && slCommand(sl) == StatusLineCommand(scriptPath) &&
		bytes.Equal(scriptBody, StatusLineScript()) {
		return session.HooksInstalled
	}
	return session.HooksOutdated
}
