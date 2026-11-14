# Pending features

Planned work, in the suggested build order. Each item gets its own branch and
PR, with a `ui/package.json` version bump inside that PR. `make check-remote`
runs before every push.

Sizes: **S** is under a day, **M** is one to two days, **L** is several PRs.
"Models" follows the plan-and-delegate rule: Sonnet implements, Opus designs
and reviews, Haiku runs the mechanical steps.

| # | Feature | Size | Branch |
|---|---|---|---|
| 1 | ~~[Status of a crashed Claude (run-as)](#1-status-of-a-crashed-claude-in-run-as-sessions)~~ (done, 0.9.4) | S–M | `fix/runas-stale-status` |
| 2 | ~~[Jump to the next waiting session](#2-jump-to-the-next-waiting-session)~~ (done, 0.9.5) | S | `feature/next-waiting` |
| 3 | ~~[Time in state in the sidebar](#3-time-in-state-and-git-branch-in-the-sidebar)~~ (done, 0.9.6) | S | `feature/status-age` |
| 4 | ~~[Find in terminal](#4-find-in-terminal)~~ (done, 0.9.7) | S–M | `feature/terminal-search` |
| 5 | ~~[Claude usage, phase 1: live limits](#5-claude-usage)~~ (done, 0.10.0) | L | `feature/usage-live` |
| 6 | ~~[Colour tags for sessions](#6-colour-tags-for-sessions)~~ (done, 0.11.0) | S | `feature/session-colors` |
| 7 | [Pinned directories](#7-pinned-directories) | S | `feature/pinned-dirs` |
| 8 | [Git branch in the sidebar](#3-time-in-state-and-git-branch-in-the-sidebar) | M | `feature/session-branch` |
| 9 | [Claude usage, phase 2: history](#5-claude-usage) | M–L | `feature/usage-history` |
| 10 | [Send a prompt to several sessions](#8-send-a-prompt-to-several-sessions) | M | `feature/broadcast` |

## Already done (no work needed)

- **Recent directories:** `remote.suggestDirs` returns the last 10 per host
  (`internal/recent`). #7 builds on this.
- **Renaming sessions:** `sessions.rename` sets `@harness_name`.
- **Opening a session from a notification:** clicking a "waiting"
  notification focuses the window and attaches the session
  (`CoreContext.tsx`, `notifyWaiting`).
- **Normal sessions after a crash:** core reports `""` when the active pane is
  back at a shell, so a crashed Claude doesn't stay "working" there.

---

## 1. Status of a crashed Claude in run-as sessions

**Done in 0.9.4.**

**Problem.** For run-as sessions, the status file's state wins over the
active-pane check, because the pane's command is `sudo` or the wrapper shell.
If Claude is killed without `SessionEnd` (SIGKILL, OOM, a dropped sudo), the
file keeps saying `working` or `waiting` indefinitely. A stale `waiting` also
counts towards the attention badge.

**Plan.**
- In `sessions.list`, for run-as sessions whose file says `working` or
  `waiting`, check whether a `claude` process is still running under the pane:
  one batched `ps -A -o pid=,ppid=,comm=` per poll, walking down from
  `#{pane_pid}`. It runs only when such a session exists.
- If none is found, report `""`, the same as a normal session back at its
  shell. Leave the file alone; the sweep removes it later.
- Make the check a pure function from the ps output to a set of live pane pids,
  so it can be unit-tested.

**Files.** `internal/session/tmuxbackend/backend.go` (list format: add
`#{pane_pid}`), `runas_status.go`, CORE_PROTOCOL.md §5.2.
**Tests.** Unit tests on the process-tree walk. An integration test where a
fake `claude` script writes `working` to the status file and is then killed;
expect `""` on the next list.
**Models.** Sonnet implements; Opus reviews.

## 2. Jump to the next waiting session

**Done in 0.9.5.**

**What.** A shortcut (proposed: Ctrl/Cmd+Shift+J) that attaches the next
session whose status is `waiting`, longest-waiting first, across all hosts.
Pressing it again moves to the next one. If nothing is waiting, a short toast
says so. Also offered in the sidebar's settings menu.

**Plan.** A pure `nextWaiting(sessions, current)` in `sidebarNav.ts`, ordered
by `claudeStatusAt` ascending. Wire it into `shortcuts.ts` and the shortcut
help.
**Tests.** Unit tests for ordering, wrap-around and skipping the current
session. A renderer e2e test in `sidebar.spec.ts` with two sessions set to
`waiting` through tmux options.
**Models.** Sonnet.

## 3. Time in state and git branch in the sidebar

**Time in state (S), done in 0.9.6.** Show "waiting 12m" or "working 3m" on the row.
`claudeStatusAt` is already in the session list, so only the renderer changes.
One shared 30-second tick re-renders the labels; no per-row timers.

**Git branch (M, done separately as #8).** Show the branch of the active
pane's directory.
- Core adds `#{pane_current_path}` to the list format.
- Every 30 s per host, and only for directories that changed, core runs one
  batched `git -C <dir> symbolic-ref --short -q HEAD` command, then caches the
  result.
- The session gets a new `gitBranch` field. Empty means not a repo, or a
  detached HEAD.
- This keeps SSH traffic flat: one extra command per host every 30 s, not one
  per session per poll.

**Tests.** A unit test for the age formatting. The branch cache gets unit
tests; an integration test checks a real repo directory.
**Models.** Sonnet.

## 4. Find in terminal

**Done in 0.9.7.**

**What.** Ctrl/Cmd+F opens a small search bar over the focused pane, with
previous/next, case-sensitive and regex toggles, and a match count. Esc closes
it and returns focus to the terminal.

**Plan.** Use `@xterm/addon-search` (pin an exact version). It searches the
xterm buffer: what has been received since attach, plus the history replayed
on attach. Matches older than that need tmux's own history, which can come in a
later step by having core fetch it with `capture-pane -p -S -<n>`.
**Note.** Ctrl+F is also a key in shells and in Claude. Check this against
`terminalKeys.ts`; Cmd+F on macOS has no such conflict. Pick the binding there,
and make it a setting if needed.
**Tests.** A renderer e2e test that prints known text and finds it; unit tests
for the key handling.
**Models.** Sonnet.

## 5. Claude usage

**Goal.** See plan usage (the 5-hour and weekly limits, and the spend limit
where one is set) with reset times, plus each session's cost and context-window
fill, without running `/usage` in each session.

**Where the data comes from.** Hooks get no usage data. The only
machine-readable source Claude Code documents is the JSON it passes to a
**status line command**. That includes:
- `rate_limits.five_hour` / `seven_day` / `spend_limit`, each with
  `used_percentage` and `resets_at`. This is only for Pro, Max and Ultra plans
  (or behind a gateway), and only after the session's first API response.
- `cost.total_cost_usd`, an estimate at list price.
- `context_window.used_percentage`.
- `model.display_name`.

Past token counts are in the transcripts
(`~/.claude/projects/*/*.jsonl`, the `message.usage` of each assistant entry).

### Phase 1: live limits and per-session cost (L)

**Done in 0.10.0.** As built: run-as sessions get no usage (decided: the
wrapper would override the other user's own status line). The limits show as
a row of bars under each host header rather than in a status bar.

- **Status line wrapper.** Add a `claude-harness-statusline` script (POSIX sh,
  versioned like the hook script). It reads stdin, writes the JSON atomically
  to a usage file for the session, then runs the user's original status line
  command with the same stdin and passes its output through. The status line
  the user sees doesn't change.
  - The usage file goes next to the run-as status file (`<file>.usage`). For
    normal sessions it goes in a per-user directory keyed by the tmux session
    id, set up the same way as the status directory.
- **Installer.** `statusLine` holds a single value in `settings.json`. The
  installer stores the user's existing command in a sidecar file, and
  uninstalling restores it. This goes through the same consent dialog as the
  hooks (`HooksDialog.tsx`), as a separate checkbox.
  - Run-as sessions: `--settings` would override that user's own status line,
    so the run-as settings file includes the wrapper only when that user has no
    status line of their own. To decide at install time.
- **Core.** It reads the usage files with the status files (the same single
  command per poll, only for running Claude sessions) and parses them in Go.
  - New session fields: `usage: {costUsd, contextPct, model}`.
  - A new `usage.limits` result per account (host + Unix user), keeping the
    newest reading. A reading past its `resets_at` is dropped.
- **UI.**
  - A usage panel, opened from the status bar or the settings menu, with one
    bar per window per account, a reset countdown, and the age of the reading
    ("as of 2 min ago").
  - The pane header shows cost and context %, with a warning colour at 80 %
    context.
  - Optional: notify when a window crosses 80 % or 95 %.
- **Docs.** CORE_PROTOCOL.md gets a new §5.4; HooksDialog wording.

**Limits to state in the UI.**
- With no Claude running on an account, the panel shows the last reading with
  its age.
- API-key accounts get cost only, no plan bars.
- The cost is Claude Code's own estimate, not a bill.

**Tests.**
- The wrapper: pass-through output, an unchanged exit code, and a missing
  original command. The wrapper must never break the user's status line.
- The installer: merge and restore, with and without an existing `statusLine`.
- The core parser: fixtures with and without `rate_limits`, and an expired
  window.
- An integration test with a fake `claude` that calls the status line.
- Renderer e2e tests for the panel in light and dark.

**Models.** Opus designs the install and merge rules and reviews them; Sonnet
implements; Haiku runs the check passes.

### Phase 2: history (M–L)

- Tokens and estimated cost per day, per model and per project, read from the
  transcripts on each host.
- Core keeps a byte offset per transcript file and only reads appended data
  (`tail -c +<offset>` over SSH), so repeat scans are cheap.
- The results are cached locally per host.
- The view shows the last 7 and 30 days, as a chart plus a table.
- **Open question.** Pricing for the cost estimate: use a bundled price table,
  or show tokens only. Prefer tokens only, with cost shown only where the
  status line reported it.

## 6. Colour tags for sessions

**Done in 0.11.0.** Six colours (red, orange, yellow, green, blue, purple);
the filter matches a colour by name prefix.

A colour dot on the row and in the rail, picked from the row's context menu
from about 6 theme-aware colours. It's stored as the tmux session option
`@harness_color`, so every client and reconnect sees it, just like
`@harness_name`. New `sessions.setColor`; the filter matches the colour name.
**Models.** Sonnet.

## 7. Pinned directories

Recent directories already exist. Add pinning: a pin button on a suggestion
in New Terminal keeps it at the top and exempt from the 10-entry limit. Stored
per host in the same file (`internal/recent`, new `pinned` key, file version
2). The new methods `remote.pinDir` and `remote.unpinDir` return the updated
list.
**Models.** Sonnet.

## 8. Send a prompt to several sessions

Select sessions (Ctrl/Cmd+click on rows, or "select all waiting/idle" in the
filter), then "Send prompt…". A dialog takes the text and lists every target by
host and name, and the user confirms.
- Core sends it with `send-keys -l` followed by `Enter`.
- It only goes to sessions whose Claude status is `idle` or `waiting`.
  Sessions that are busy or not running Claude are listed and skipped.

**Risks.** A prompt typed into a permission dialog answers the dialog, so
`waiting` targets are opt-in and off by default. Nothing is sent to a plain
shell.
**Tests.** Unit tests for target selection; an e2e test that the confirmation
lists the targets and skips busy ones; an integration test that the text
arrives.
**Models.** Opus designs the safety rules; Sonnet implements.
