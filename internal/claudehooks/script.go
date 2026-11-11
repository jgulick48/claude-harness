// Package claudehooks holds the Claude Code status hook script and the pure
// logic for merging it into (and out of) a Claude Code settings.json. It does
// no I/O: callers pass bytes in and get bytes back.
package claudehooks

// Version is the script version; bump it whenever Script changes so installed
// copies show up as outdated.
const Version = 1

// Events lists every Claude Code hook event we register the script for.
var Events = []string{
	"UserPromptSubmit",
	"PreToolUse",
	"PostToolUse",
	"PostToolUseFailure",
	"PermissionRequest",
	"Notification",
	"Stop",
	"StopFailure",
	"SessionStart",
	"SessionEnd",
}

// script is the POSIX sh hook. It must never write to stdout (Claude injects
// it into context on some events) and must always exit 0 (exit 2 blocks).
const script = `#!/bin/sh
# claude-harness-status v1
# Claude Code hook: records Claude's state in the tmux session options
# @claude_status and @claude_status_at. Always exits 0 and prints nothing.
exec >/dev/null 2>&1
input=$(cat)
[ -n "$TMUX" ] && [ -n "$TMUX_PANE" ] || exit 0

# field NAME: first string value of "NAME" in the JSON on stdin.
field() {
	printf '%s' "$input" | tr -d '\r\n' |
		grep -o "\"$1\"[[:space:]]*:[[:space:]]*\"[^\"]*\"" | head -n 1 |
		sed 's/^[^:]*:[[:space:]]*"//; s/"$//'
}
event=$(field hook_event_name)
case "$event" in
UserPromptSubmit | PreToolUse | PostToolUse | PostToolUseFailure) state=working ;;
PermissionRequest) state=waiting ;;
Stop | StopFailure | SessionStart) state=idle ;;
SessionEnd) state= ;;
Notification)
	case "$(field notification_type)" in
	permission_prompt | elicitation_dialog | elicitation_url_dialog | agent_needs_input) state=waiting ;;
	*) exit 0 ;;
	esac
	;;
*) exit 0 ;;
esac

tmux=$(command -v tmux)
if [ -z "$tmux" ]; then
	for d in /opt/homebrew/bin /usr/local/bin /usr/bin; do
		[ -x "$d/tmux" ] && tmux="$d/tmux" && break
	done
fi
[ -n "$tmux" ] || exit 0

if [ "$event" = SessionEnd ]; then
	"$tmux" set-option -qu -t "$TMUX_PANE" @claude_status
	"$tmux" set-option -qu -t "$TMUX_PANE" @claude_status_at
else
	"$tmux" set-option -q -t "$TMUX_PANE" @claude_status "$state"
	"$tmux" set-option -q -t "$TMUX_PANE" @claude_status_at "$(date +%s)"
fi
exit 0
`

// Script returns the hook script, starting with "#!/bin/sh" and a
// "# claude-harness-status v<Version>" marker line.
func Script() []byte { return []byte(script) }
