# claude-harness Architecture

Status: **Draft / planning** — no implementation yet.

## 1. Goal

A cross-platform desktop application (Windows, macOS, Linux) that connects to
tmux sessions on many remote machines and presents them in one window. The
primary use case is running Claude Code in many sessions, repos, and machines
from one place, without managing individual SSH sessions in the background.

### Requirements

- Desktop client runs on **Windows, macOS, and Linux**.
- Remote hosts are **macOS and Linux**.
- Sessions **persist** when the app closes or the network drops, and can be
  re-attached later.
- Can **attach to existing tmux sessions**, including ones started outside the
  app (e.g. from a plain `ssh` + `tmux`).
- All hosts are **reachable directly over SSH** (LAN, VPN, Tailscale, etc.); no
  relay/NAT traversal needed.
- **Single user** for now; all config is local to the desktop.
- Easy to open new terminals **in different directories** on a remote host, and
  either **name** them or **display their directory**.
- A **left sidebar** shows all connections and sessions at once, **grouped by
  host**.
- Each session shows its **Claude status**: working, waiting for a response,
  or idle.
- Backend in **Go**, UI in **Electron**.

### Non-goals (for now)

- Windows remote hosts (possible later via WSL+tmux or a native agent backend — see §9).
- Multi-user / shared sessions, RBAC.
- Relays, NAT traversal, or a hosted service.
- Mobile clients.

## 2. Key decision: wrap real tmux

Two approaches were considered:

| | **A. Drive real tmux over SSH (chosen)** | B. Custom Go agent per host |
|---|---|---|
| Persistence & screen restore | Free — tmux already does it | Must build (headless VT emulator) |
| Attach to existing tmux sessions | Yes | No (without an adapter) |
| Remote install | None beyond tmux | Bootstrap/upgrade an agent binary |
| Windows remote hosts | No (WSL only) | Yes via ConPTY |
| Claude awareness | Via tmux user options + hooks | Via agent metadata |

Because remote hosts are macOS/Linux only and attaching to existing sessions is
required, **A** wins. The session layer is still written behind a
`SessionBackend` interface so a native agent could be added later.

tmux is driven in **control mode** (`tmux -C`), which provides a structured,
line-based protocol (`%output`, `%window-add`, `%session-changed`,
`%subscription-changed`, …) instead of a rendered terminal stream.

**Minimum tmux version: 3.2** (needed for `refresh-client -B` format
subscriptions).

## 3. Components

```
┌──────────────────── Desktop (Windows / macOS / Linux) ────────────────────┐
│                                                                           │
│  Electron UI  (TypeScript + React + xterm.js)                             │
│     │                                                                     │
│     │  localhost WebSocket, random per-launch auth token                  │
│     │  (JSON control messages + binary terminal frames)                   │
│     ▼                                                                     │
│  harness-core  (Go sidecar, spawned and supervised by Electron)           │
│     • host/connection manager, auto-reconnect with backoff                │
│     • SSH client: ~/.ssh/config, ssh-agent, known_hosts, ProxyJump        │
│     • tmux control-mode client per host                                   │
│     • local config & state (hosts, recent dirs, favorites)                │
└─────┬───────────────────────────────┬─────────────────────────────────────┘
      │ one SSH connection per host   │
      ▼                               ▼
┌───────────────┐               ┌───────────────┐
│ host: devbox  │               │ host: macmini │
│ tmux ≥ 3.2    │               │ tmux ≥ 3.2    │
│ (+ optional   │               │               │
│  Claude hooks)│               │               │
└───────────────┘               └───────────────┘
```

### 3.1 harness-core (Go)

Responsibilities:

- **SSH transport** using `golang.org/x/crypto/ssh`:
  - Parse `~/.ssh/config` (e.g. `kevinburke/ssh_config`): `HostName`, `User`,
    `Port`, `IdentityFile`, `ProxyJump`.
  - Authenticate through an SSH agent, which holds and loads the keys:
    - **macOS / Linux:** the agent at `SSH_AUTH_SOCK`, with identity files from
      `~/.ssh/config` as a fallback.
    - **Windows:** **Pageant** (PuTTY's agent). Pageant loads and unlocks the
      keys; the app never reads private key files on Windows. Core talks to
      Pageant through `golang.org/x/crypto/ssh/agent`. It uses Pageant's
      OpenSSH-compatible named pipe (Pageant 0.75+) if available, or its
      window-message interface otherwise (e.g. via `github.com/davidmz/go-pageant`).
      If Pageant isn't running or has no keys loaded, the UI shows a clear
      message explaining how to start it and add keys.
  - Verify host keys against `~/.ssh/known_hosts`; prompt the UI on unknown keys.
  - One SSH connection per host; multiple channels multiplexed over it.
- **tmux control-mode client** per host:
  - Runs `tmux -C` over an SSH channel (or a local process) (see §4).
  - Parses notifications, issues commands, correlates `%begin`/`%end` replies.
  - Subscribes to formats such as `#{pane_current_path}` and `#{@claude_status}`.
- **Remote helpers** over separate SSH exec channels: directory autocomplete,
  git repo discovery, tmux version check, hook installation.
- **Local API server** for the UI (localhost only, token-authenticated).
- **Reconnect**: exponential backoff; on reconnect, re-list sessions and
  re-attach the views that were open.

Proposed package layout:

```
/cmd/harness-core
/internal/sshconn      SSH dialing, config parsing, agent, known_hosts
/internal/tmux         control-mode parser, command client, subscriptions
/internal/session      SessionBackend interface + tmux implementation
/internal/remote       dir autocomplete, repo discovery, host checks
/internal/api          WebSocket server, message types
/internal/config       local config/state persistence
/proto                 (optional) message schema shared with the UI
```

### 3.2 Electron UI

- **TypeScript + React + Vite**, **xterm.js** (with fit, search, and web-links
  addons) for terminal rendering.
- The main process spawns `harness-core` and passes it a random auth token.
  The renderer connects to it over a localhost WebSocket.
- `harness-core` binaries for each platform are bundled through
  electron-builder `extraResources`.

## 4. tmux integration details

### 4.1 Listing sessions

```
tmux list-sessions -F '#{session_id}\t#{session_name}\t#{session_attached}\t#{@harness_name}\t#{pane_current_path}'
```

Sessions with no `@harness_name` option were created outside the app and are
shown as **external**. They can be attached exactly like app-created sessions.

### 4.2 Creating a session

```
tmux new-session -d -s <id> -c <dir> [<command>]
tmux set-option -t <id> @harness_name '<user name or empty>'
tmux set-option -t <id> @harness_managed 1
```

- `<id>` is unique. It's the user's name, sanitized, if one was given;
  otherwise it's generated (e.g. `h-<dir-basename>-<n>`). tmux session names
  must be unique and cannot contain `.` or `:`.
- `<command>` is empty (login shell) or `claude`.

### 4.3 Attaching

1. Start a control client: `tmux -C attach-session -t <session_id>`, over an
   SSH channel or, for the local host, a local process. Use `-C`, not `-CC`:
   `-CC` is meant for a real terminal, while plain `-C` works over pipes.
   Replies to our commands arrive as `%begin … 1` / `%end … 1` blocks. The
   first block after attaching has flags `0`; it isn't ours and is skipped.
2. Set the client size: `refresh-client -C <cols>x<rows>`.
3. Seed the screen. Read the active pane and cursor with `display -p` and the
   format `#{pane_id} #{cursor_x} #{cursor_y} #{alternate_on}`, then run
   `capture-pane -p -e -N -S -<scrollback>`. Don't use `-J`, because lines
   must stay the shape they have on screen. Write the result into a fresh
   xterm.js, then put the cursor in place with a CSI sequence. If the
   alternate screen is active, switch to it first and capture only the
   visible screen.
4. Stream live `%output` for that pane into xterm.js. Output that arrived
   before the capture reply is dropped, because the capture already
   includes it.
5. Forward keystrokes with `send-keys -t <pane> -H <hex…>`, in chunks, so
   any bytes and escape sequences pass through safely.
6. Detach by closing the control client's stdin. The session keeps running.

### 4.4 Resizing and shared clients

- UI resizes go to `refresh-client -C <cols>x<rows>`.
- If the same session is also attached from a regular terminal, client sizes
  conflict. Use `window-size latest` (session option) so tmux follows the most
  recently active client.

### 4.5 One view per session (v1)

Each UI terminal view maps to **one tmux session** (its active window/pane).
tmux windows and panes within a session are not mirrored as UI tabs/splits in
v1. The UI does its own switching and (later) splits. Mapping tmux
windows/panes natively is a possible future enhancement.

## 5. UI design

### 5.1 Session sidebar (left pane)

All connections and sessions are visible at once in a persistent left pane,
grouped by host. The terminal fills the rest of the window.

```
┌──────────────────────────────┬──────────────────────────────────────────┐
│ SESSIONS               [+]   │ devbox / claude-harness                  │
│                              │ ~/go/src/github.com/…/claude-harness     │
│ ▾ ● devbox          3   [+]  │──────────────────────────────────────────│
│   ▌◐ claude-harness  Working │                                          │
│      ~/go/src/…/claude-harn… │                                          │
│    ◉ api-refactor    Waiting │            (xterm.js terminal)           │
│      ~/work/api              │                                          │
│    ○ main  external          │                                          │
│      ~                       │                                          │
│                              │                                          │
│ ▾ ● macmini         2   [+]  │                                          │
│    ✓ infra           Idle    │                                          │
│      ~/infra                 │                                          │
│    ○ scratch                 │                                          │
│      ~/tmp                   │                                          │
│                              │                                          │
│ ▸ ○ buildbox  disconnected   │                                          │
│                              │                                          │
│ ⌕ Filter…          Cmd/Ctrl+K│                                          │
└──────────────────────────────┴──────────────────────────────────────────┘
```

**Host groups**
- One collapsible group per host. The header shows connection state
  (connected / connecting / disconnected / error), the session count, and a
  per-host **[+]** that opens the new-terminal dialog with that host selected.
- Disconnected hosts stay listed (collapsed); clicking reconnects.
- If a collapsed group contains a session that is **Waiting**, the group header
  shows a waiting indicator so it isn't missed.

**Session rows**
- **Label rule:** show `@harness_name` if set, otherwise the **basename of the
  current directory**. The second line shows the full current path (truncated
  in the middle, full path on hover). It updates live through the
  `#{pane_current_path}` subscription.
- **Status** (see §6) appears as an icon plus a text label on the right of the
  row. Color is never the only signal.
- External sessions are tagged `external`.
- The active session is highlighted with a left accent bar.
- Right-click menu: rename, open in new split (later), kill session.

**Sidebar behavior**
- Resizable width, and can be collapsed to a narrow rail showing only status
  icons (Cmd/Ctrl+B).
- Sort within each host: manual order (default) or **"needs attention
  first"** (Waiting → Working → Idle → other).
- A filter box at the bottom (Cmd/Ctrl+K focuses it) fuzzy-matches session
  name, path, and host.
- Keyboard: Up/Down to move, Enter to attach, Cmd/Ctrl+1…9 to jump.

### 5.2 New terminal dialog ([+])

| Field | Behavior |
|---|---|
| Host | Defaults to the host whose **[+]** was clicked, or the current terminal's host |
| Directory | Autocompletes remote paths (core runs a listing over SSH exec); suggests recent dirs on that host and discovered git repos |
| Name | Optional; blank → sidebar shows the directory |
| Command | Shell (default) or `claude` |

The core records recent directories for each host in its local state.

### 5.3 Other UI

- Host management: import from `~/.ssh/config` or add manually; shows tmux
  version and setup-check results.
- Desktop notifications for Claude sessions that need input.
- Later: splits showing sessions from different hosts, saved layouts, themes.

## 6. Claude Code awareness and session status

Goal: show in the sidebar whether Claude is **working**, **waiting for a
response**, or **idle** in each session, without scraping the screen.

### 6.1 Status states

| State | Icon | Meaning |
|---|---|---|
| **Working** | ◐ (animated) | Claude is processing a prompt or running tools |
| **Waiting** | ◉ (accent color) | Claude needs you: a permission prompt or a question it's waiting on |
| **Idle** | ✓ | Claude finished its turn; nothing pending |
| *(none)* | ○ | Claude isn't running in this session (plain shell or another program) |
| **Unknown** | ? | Claude is running but no hook data yet (e.g. hooks not installed on this host) |

### 6.2 How status is produced

- **The script.** On each host the app writes one POSIX `sh` script,
  `<config dir>/claude-harness/claude-status.sh`, where the config dir is
  `$CLAUDE_CONFIG_DIR` or `~/.claude`. The script carries a version marker line.
  When run by a hook it:
  - reads the hook's JSON from stdin, with no `jq` dependency;
  - picks a state from the event name and, for `Notification`, from
    `notification_type`. The mapping is in docs/CORE_PROTOCOL.md §5.2;
  - runs `tmux set-option -q -t "$TMUX_PANE" @claude_status <state>` and sets
    `@claude_status_at`, or unsets both on `SessionEnd`.

  It does nothing outside tmux (no `$TMUX`/`$TMUX_PANE`), **always exits 0, and
  never writes to stdout**. Claude Code injects hook stdout into context on some
  events, and exit 2 blocks, so the script must never do either.
- **The installer.** It merges one hook entry per event into the user settings
  file `<config dir>/settings.json`, the same file Claude Code reads. Each entry
  runs the script with a short timeout. It reads the existing JSON (an invalid
  file is refused, never overwritten), keeps every other key and hook, and adds
  our entries only if they're missing. Entries are identified by the script
  path. Before writing, it saves a timestamped backup next to the file. Removal
  deletes only our entries (and the script). Hooks from other settings files
  merge with these, so project-level hooks keep working.
- **Status reporting.** Each host reports `claudeHooks`: `installed`,
  `outdated` (our entries or script are an older version), `missing` or
  `unknown`. The UI offers **Install** or **Update**, and changes nothing
  without the user's confirmation.

### 6.3 How status reaches the UI

- v1 reads `#{@claude_status}`, `#{@claude_status_at}` and the active pane's
  `#{pane_current_command}` in the existing `list-sessions` poll, every 2 s for
  each host, including sessions that aren't attached. Changes reach the UI in
  `sessions.changed`. A push-based control-mode monitor client
  (`refresh-client -B` subscriptions) is a possible later improvement to cut
  the latency.
- **Stale-status guard:** if `pane_current_command` shows Claude is no longer
  running (e.g. it crashed without firing `SessionEnd`), core shows *(none)*
  regardless of the stored option.
- On a transition into **Waiting**, the UI raises a desktop notification
  (configurable per host or session; suppressed if that session is focused).
  Optionally it notifies on Working → Idle too.

## 7. Core ↔ UI API (sketch)

Control messages (JSON over the WebSocket):

| Direction | Message |
|---|---|
| UI → core | `hosts.list`, `hosts.add`, `hosts.remove`, `hosts.connect` |
| UI → core | `sessions.list{host}`, `sessions.create{host, dir, name?, command}`, `sessions.kill{host, id}`, `sessions.rename` |
| UI → core | `term.attach{host, id, cols, rows}` → `{streamId}`, `term.detach{streamId}`, `term.resize{streamId, cols, rows}` |
| UI → core | `remote.completeDir{host, prefix}`, `remote.recentDirs{host}` |
| core → UI | `event.hostState`, `event.sessionAdded/Removed/Changed` (name, cwd, claude status), `event.hostKeyPrompt` |

Terminal data travels as binary WebSocket frames: `[streamId:u32][payload]`
in both directions.

The UI and core are released together, but the API still carries a protocol
version so mismatches fail clearly.

## 8. Security

- Core listens on `127.0.0.1` only and requires the per-launch token, so other
  local processes cannot drive SSH sessions.
- Strict host key checking against `known_hosts`; new keys need explicit
  confirmation in the UI.
- No private keys are stored by the app; it uses ssh-agent and existing
  identity files.
- Remote commands built from user input (dirs, names) are shell-quoted, and
  tmux names are sanitized.

## 9. Future / extension points

- **Native agent backend** (`SessionBackend` implementation) for Windows hosts
  or features tmux can't provide.
- Mirror tmux windows/panes as UI tabs/splits.
- Multi-user or shared sessions.
- Local (non-SSH) tmux as a first-class "host" (also used for development in M1).

## 10. Milestones

Each milestone is its own feature branch and PR.

| # | Milestone | Done when |
|---|---|---|
| M0 | Skeleton: Go module, Electron + React + Vite shell, sidecar spawn, CI building on all 3 OSes | CI green on Windows/macOS/Linux |
| M1 | Core drives **local** tmux in control mode; xterm.js renders it; basic left sidebar listing sessions | Can use a local tmux session in the app |
| M2 | SSH to one host: list/attach existing sessions, create with dir + name, detach/reattach | Sessions survive app restart |
| M3 | Multiple hosts, sidebar grouped by host with connection state, per-host [+], auto-reconnect, `~/.ssh/config` import, remote dir autocomplete, recent dirs | 3+ hosts visible and usable at once |
| M4 | Claude status: hook installer, per-host monitor client, Working/Waiting/Idle in the sidebar, notifications, "needs attention first" sort | Each session's Claude status is visible in the sidebar |
| M5 | Host setup check (tmux version, hooks), signed installers (packaging and auto-update were pulled forward: GitHub Releases + `electron-updater` on Windows/Linux; macOS auto-install pending signing, until then macOS updates are manual downloads) | One-click install on all 3 OSes |
| M6 | Sidebar filter and keyboard navigation, collapsed rail, splits across hosts, saved layouts, themes | — |

## 11. Risks and open questions

- **Control-mode edge cases:** output escaping (octal-escaped `%output`),
  `%extended-output` and flow control (`pause-after`) on slow links. Mitigation:
  a thorough parser test suite built from recorded tmux transcripts.
- **Screen seeding fidelity:** `capture-pane -e` restores content and colors
  but not every terminal mode (e.g. alternate screen or mouse modes).
  Validate early with Claude Code's full-screen UI in M1.
- **tmux version spread** across hosts. Mitigation: version check with a clear
  error below 3.2.
- **Windows key handling: decided.** Pageant is the agent on Windows and
  handles loading keys (§3.1). Remaining work for M3: test both Pageant
  transports (named pipe and window messages) and the "Pageant not running"
  path.
- **Code signing/notarization** costs and setup for macOS and Windows (M5).
