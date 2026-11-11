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

tmux is driven in **control mode** (`tmux -CC`), which provides a structured,
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
  - Authenticate via ssh-agent (Unix socket; Windows OpenSSH agent named pipe;
    Pageant) and identity files.
  - Verify host keys against `~/.ssh/known_hosts`; prompt the UI on unknown keys.
  - One SSH connection per host; multiple channels multiplexed over it.
- **tmux control-mode client** per host:
  - Runs `tmux -CC` on an SSH channel (see §4).
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

1. Open an SSH channel and run `tmux -CC attach-session -t <id>`.
2. Set the client size: `refresh-client -C <cols>x<rows>`.
3. Seed the screen: `capture-pane -p -e -J -S -<scrollback>` and write the
   result into xterm.js.
4. Stream live `%output` for the session's active pane into xterm.js.
5. Forward keystrokes with `send-keys -H` (hex) so arbitrary bytes and escape
   sequences pass through safely.

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

### 5.1 Terminal selector (dropdown)

```
┌────────────────────────────────────────────────────────┬─────┐
│ ● devbox / claude-harness   ~/go/src/…/claude-harness ▾ │ [+] │
└────────────────────────────────────────────────────────┴─────┘
  devbox
    ● claude-harness      ~/go/src/…/claude-harness    ⏳ waiting
    ○ api-refactor        ~/work/api                   ⚙ working
    ○ main  (external)    ~
  macmini
    ○ infra               ~/infra
```

- Grouped by host; host connection state shown on the group header.
- **Label rule:** show `@harness_name` if set, otherwise the **basename of the
  current directory**. The full current path appears dimmed beside it and
  updates live through the `#{pane_current_path}` subscription.
- External sessions are tagged `(external)`.
- Claude status badges (§6); optional "waiting first" sort.
- **Cmd/Ctrl+K** opens the same list as a fuzzy-search quick switcher.

### 5.2 New terminal dialog ([+])

| Field | Behavior |
|---|---|
| Host | Defaults to the current terminal's host |
| Directory | Autocompletes remote paths (core runs a listing over SSH exec); suggests recent dirs on that host and discovered git repos |
| Name | Optional; blank → dropdown shows the directory |
| Command | Shell (default) or `claude` |

The core records recent directories for each host in its local state.

### 5.3 Other UI

- Host management: import from `~/.ssh/config` or add manually; shows tmux
  version and setup-check results.
- Desktop notifications for Claude sessions that need input.
- Later: splits showing sessions from different hosts, saved layouts, themes.

## 6. Claude Code awareness

Goal: know which sessions are **working**, **waiting for input**, or **idle**
without scraping the screen.

- `harness-core` can install (with user confirmation) a Claude Code hook
  config on each host. The hooks set a tmux pane/session user option:
  - `Notification` hook → `tmux set -p @claude_status waiting`
  - `Stop` hook → `tmux set -p @claude_status idle`
  - `UserPromptSubmit` / `PreToolUse` → `tmux set -p @claude_status working`
- The control-mode client subscribes to `#{@claude_status}` via
  `refresh-client -B`. Changes arrive as `%subscription-changed` and are pushed
  to the UI.
- Hooks are guarded so they no-op outside tmux (`[ -n "$TMUX" ]`).

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
| M1 | Core drives **local** tmux in control mode; xterm.js renders it; basic dropdown | Can use a local tmux session in the app |
| M2 | SSH to one host: list/attach existing sessions, create with dir + name, detach/reattach | Sessions survive app restart |
| M3 | Multiple hosts, grouped dropdown, auto-reconnect, `~/.ssh/config` import, remote dir autocomplete, recent dirs | 3+ hosts usable in one window |
| M4 | Claude awareness: hook installer, status badges, notifications, waiting-first sort | Waiting Claude sessions are visible at a glance |
| M5 | Host setup check (tmux version, hooks), packaging, signed installers, auto-update | One-click install on all 3 OSes |
| M6 | Cmd/Ctrl+K switcher, splits across hosts, saved layouts, themes | — |

## 11. Risks and open questions

- **Control-mode edge cases:** output escaping (octal-escaped `%output`),
  `%extended-output` and flow control (`pause-after`) on slow links. Mitigation:
  a thorough parser test suite built from recorded tmux transcripts.
- **Screen seeding fidelity:** `capture-pane -e` restores content and colors
  but not every terminal mode (e.g. alternate screen or mouse modes).
  Validate early with Claude Code's full-screen UI in M1.
- **tmux version spread** across hosts. Mitigation: version check with a clear
  error below 3.2.
- **Windows ssh-agent variants** (OpenSSH named pipe vs. Pageant). Test in M3.
- **Code signing/notarization** costs and setup for macOS and Windows (M5).
