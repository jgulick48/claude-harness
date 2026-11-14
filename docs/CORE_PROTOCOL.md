# Core ↔ UI protocol

How the Electron app launches `harness-core` and talks to it. See
[ARCHITECTURE.md](ARCHITECTURE.md) §3 and §7 for background. Protocol version: **7** (v2 added requests/results, events, sessions and terminals in M1; v3 added SSH host management in M2; v4 added auto-reconnect, `~/.ssh/config` import and directory suggestions in M3; v5 added Claude Code status in M4; v6 adds the host setup check in M5; v7 adds run-as-user sessions).

## 1. Launch

1. The Electron main process generates a token: 32 random bytes, hex-encoded
   (64 chars).
2. It spawns the core binary:

   ```
   harness-core --listen 127.0.0.1:0
   ```

   with the environment variable `HARNESS_TOKEN=<token>` and stdin, stdout and
   stderr all piped.
3. Core binds the listen address (`:0` means the OS picks a free port) and writes
   **exactly one line** to stdout, then nothing else to stdout:

   ```json
   {"type":"ready","addr":"127.0.0.1:54321","port":54321,"version":"0.1.0-dev","protocol":6}
   ```

4. All logs go to **stderr** (`log/slog` text format). The UI forwards them to
   its own log.

### Core startup rules

| Condition | Behavior |
|---|---|
| `HARNESS_TOKEN` empty or unset | Message to stderr, exit code **2** |
| `--listen` host isn't loopback (`127.0.0.1`, `::1`, `localhost`) | Message to stderr, exit code **2** |
| Can't bind | Message to stderr, exit code **1** |
| `--version` | Print the version to stdout and exit 0, without starting the server |

The default for `--listen` is `127.0.0.1:0`.

## 2. Lifetime

- Core shuts down gracefully, with exit code 0, when **stdin reaches EOF**,
  meaning the parent closed it or died, or on SIGINT/SIGTERM.
- To stop core, the UI first closes core's stdin. If core hasn't exited after
  3 seconds, the UI kills it.
- If the UI waits 10 seconds after spawning without seeing a `ready` line, it
  treats startup as failed and kills the process.

## 3. HTTP endpoints

Core binds loopback only. Every endpoint requires the token, and tokens are
compared in constant time.

| Endpoint | Auth | Response |
|---|---|---|
| `GET /healthz` | Header `Authorization: Bearer <token>` | `200 {"ok":true,"version":"…","protocol":6}`; `401` if the token is missing or wrong |
| `GET /ws?token=<token>` | Query parameter (browser WebSockets can't set headers) | WebSocket upgrade; `401` **before** upgrade if the token is missing or wrong |

The WebSocket server doesn't check the Origin header, because the Electron
renderer's origin varies (`http://localhost:<port>` in dev, `file://` when
packaged). The token is the access control.

## 4. WebSocket messages

Control messages are JSON in **text** frames. Terminal data travels in
**binary** frames (§6).

### 4.1 Basics

| Direction | Message | Notes |
|---|---|---|
| core → UI | `{"type":"hello","version":"0.1.0-dev","protocol":6}` | Sent right after connecting |
| UI → core | `{"type":"ping","id":1}` | `id` is an integer chosen by the client |
| core → UI | `{"type":"pong","id":1}` | Echoes the `id` |
| core → UI | `{"type":"error","id":1,"message":"…"}` | Reply to a failed request, or to an unknown `type`. `id` is echoed if the request had one, otherwise omitted. The connection stays open. |
| core → UI | `{"type":"error","message":"invalid JSON"}` | Malformed message. The connection stays open. |

If the protocol number in `hello` doesn't match the UI's expected protocol,
the UI shows a version-mismatch error instead of continuing.

### 4.2 Requests and results

A request is `{"type":"<method>","id":<int>, ...params}`. Every request gets
exactly one reply with the same `id`: either
`{"type":"result","id":<int>,"result":<value>}` or
`{"type":"error","id":<int>,"message":"…","code":"<code>"}`.

Error codes: `bad_request`, `not_found` (unknown host/session/stream),
`unavailable` (host can't run tmux), `internal`.

### 4.3 Events

Events are pushed by core and have no `id`: `{"type":"event","event":"<name>", ...fields}`.

## 5. Hosts and sessions

The objects mirror `internal/session` (`Host`, `Session`):

```json
Host:    {"id":"local","name":"This computer","kind":"local","state":"connected","home":"/home/me","tmuxVersion":"3.2a"}
         {"id":"devbox","name":"devbox","kind":"ssh","target":"me@devbox.lan","state":"host_key_unknown",
          "hostKey":{"address":"devbox.lan","keyType":"ssh-ed25519","fingerprint":"SHA256:…"}}
         state: "connected" | "connecting" | "disconnected" | "unavailable" | "host_key_unknown" | "error"
         ("error" carries the reason for disconnected / unavailable / error;
          "retryAt" (unix ms) is set while disconnected with an automatic retry scheduled;
          "claudeHooks": "installed" | "outdated" | "missing" | "unknown", whether the
          Claude status hooks are installed on that host, omitted until checked)
Session: {"id":"$3","name":"h-api-1","harnessName":"","managed":true,"cwd":"/home/me/work/api",
          "windows":1,"attached":0,"created":1794420293,
          "claudeStatus":"waiting","claudeStatusAt":1794420400,"runAs":"bob"}
         runAs: the user the session switched to; omitted when not set.
         A run-as session reports Claude status through a status file, not the tmux
         options (§5.2).
         claudeStatus: "working" | "waiting" | "idle" | "unknown" | "" (Claude not running);
         see §5.2
```

| Method | Params | Result |
|---|---|---|
| `hosts.list` | none | `[Host]`: `local` first, then SSH hosts in the order added. |
| `hosts.add` | `target` (an `~/.ssh/config` alias or `user@host[:port]`), `name` (optional) | `Host`. The host is saved and starts connecting in the background; follow `hosts.changed`. |
| `hosts.remove` | `host` | `null`. Disconnects and forgets the host. Its tmux sessions keep running there. `local` can't be removed. |
| `hosts.connect` | `host` | `null`. Reconnects a host that is `disconnected` or in `error`. |
| `hosts.configHosts` | none | `[ConfigHost]`: concrete `Host` entries from `~/.ssh/config` (wildcard patterns skipped), each `{"alias","hostName","user","port","added"}`. `added` is true when a saved host already uses that alias as its target. |
| `hosts.installHooks` | `host` | `null`. Installs the Claude status hooks on the host (§5.2) after backing up its settings file. Idempotent. The host's `claudeHooks` updates via `hosts.changed`. |
| `hosts.removeHooks` | `host` | `null`. Removes only our hook entries and script. Idempotent. |
| `hosts.check` | `host` | `HostReport` (§5.3). A read-only setup check of the host. It changes nothing there. |
| `hosts.trustKey` | `host`, `fingerprint` | `null`. Adds the pending host key to `~/.ssh/known_hosts` and reconnects. `fingerprint` must match `hostKey.fingerprint`; this guards against a key changing between the prompt and the click. |
| `sessions.list` | `host` | `[Session]` |
| `sessions.create` | `host`, `dir` (optional, `~` allowed, default home), `name` (optional), `command` (`"shell"` default or `"claude"`), `runAs` (optional Unix user name; the session's shell runs `sudo -iu <user>` then cds to `dir` (interpreted as that user, `~` is their home) and, for `command: "claude"`, starts Claude with the status hooks attached (§5.2); sudo asks for a password in the terminal if it needs one. Invalid names are `bad_request`.) | `Session` |
| `sessions.kill` | `host`, `session` | `null` |
| `sessions.rename` | `host`, `session`, `name` (`""` clears it) | `null`. Sets the display name only. |

Event `hosts.changed`, pushed on connect and whenever any host's info or the
set of hosts changes: `{"type":"event","event":"hosts.changed","hosts":[Host]}`.

A host whose connection drops ends its open streams with `term.closed`, reason
`"connection lost"`. Its sessions are still running on the host.

**Automatic reconnect.** After a connection is lost, or after a connect attempt
fails for a reason that may be temporary (network unreachable, timeout,
connection refused, DNS failure), core retries on its own with exponential
backoff: 1 s, 2 s, 4 s and so on, capped at 60 s, with ±20 % jitter. While
waiting, the host is `disconnected` with `retryAt` set. Failures that need the
user (`host_key_unknown`, a changed host key, no usable SSH keys, an
authentication failure) are **not** retried. They stay in `host_key_unknown`
or `error` until the user acts. `hosts.connect` retries immediately and resets
the backoff. The backoff also resets when a connection is lost after staying
up for at least 30 s, so a host that accepts and then drops at once isn't retried every second. The UI reattaches open views
once the host is back.

Event `sessions.changed`, pushed on connect for each host and whenever that
host's list changes:
`{"type":"event","event":"sessions.changed","host":"local","sessions":[Session]}`.

**Display label rule (UI):** `harnessName` if non-empty, otherwise the
basename of `cwd`, otherwise `name`. `managed: false` means the session was
started outside the app, so the UI tags it `external`.

### 5.2 Claude status

`claudeStatus` comes from Claude Code hooks that the app installs, with the
user's confirmation, on each host. A hook writes the tmux **session options**
`@claude_status` and `@claude_status_at` (unix seconds) for the session its pane
belongs to. Core reads those options with the session list, so a change shows
up within one poll (≤ 2 s) in `sessions.changed`.

| `claudeStatus` | Meaning | Set by |
|---|---|---|
| `working` | Processing a prompt or running tools | `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `PostToolUseFailure` |
| `waiting` | Needs the user: a permission decision or a question | `PermissionRequest`; `Notification` whose `notification_type` is `permission_prompt`, `elicitation_dialog`, `elicitation_url_dialog` or `agent_needs_input` |
| `idle` | Finished its turn; nothing pending | `Stop`, `StopFailure`, `SessionStart` (other notification types leave the status unchanged) |
| `unknown` | Claude is running (the active pane's command is `claude`) but no hook has reported | none (core derives it) |
| `""` | Claude isn't running | `SessionEnd` clears the options. Core also reports `""` whenever the active pane's command is a shell (`sh`, `bash`, `zsh`, `fish`, `dash`, `ksh`, `tcsh`), in case Claude exited without `SessionEnd`. |

The hooks never affect Claude: the script always exits 0 and prints nothing.

**Run-as sessions.** Claude runs as another user via `sudo -i`, so `$TMUX` is
unset and it can't reach the login user's tmux server, and that user may not
have the hooks installed. Instead:

- Core keeps a status directory on the host, owned by the login user, mode
  `1733` (sticky; others can create files but not list or delete them):
  `/tmp/claude-harness-status-<login uid>` (override: `HARNESS_STATUS_DIR`).
  Before the first run-as session (and again if its files go missing) core
  verifies the directory is a real directory it owns and writes
  `hook-v<N>.sh` (the hook script) and `settings-v<N>.json` (only our hook
  entries) into it. If this fails the session is still created, just without
  status.
- Each run-as session gets a random status file `<dir>/<32 hex>`, stored in the
  tmux session option `@harness_status_file`. Core types
  `sudo -iu <user> -- env HARNESS_STATUS_FILE=<file> sh -c '…claude --settings <dir>/settings-v<N>.json…'`.
  Claude merges `--settings` with the user's own settings.
- With `HARNESS_STATUS_FILE` set, the v2 hook script writes `<state> <unix
  seconds>` to that file (atomically; `SessionEnd` empties it) instead of
  touching tmux.
- Core reads the status files of all run-as sessions in one command per
  `sessions.list` poll and uses the file's state and time in place of the tmux
  options. A reported state wins over the active-pane check (the pane's command
  is `sudo`, or the wrapper shell where sudo doesn't use its own pty); an empty file falls back to `unknown`/`""` as above.
  A reported state only counts while a `claude` process runs under one of the
  session's panes (checked with one `ps` in the same exec that reads the
  files); when none does, because Claude was killed without `SessionEnd`, the
  session reports `""`. The check is skipped when `ps` can't see other users'
  processes (e.g. `hidepid`).
  Core removes the file when the session is killed. Files of sessions that
  ended some other way (the user exited the shell, the tmux server died) are
  swept: at most every 10 minutes, after a successful list, core deletes status
  files (and the hook's `<file>.<pid>` temp files) not written for an hour that
  no live session references. A file still used by a session on another tmux
  socket may be swept too; the hook recreates it on its next event.

So status works for `command: "claude"` run-as sessions. A run-as `shell`
session also has `HARNESS_STATUS_FILE` set, so it reports status only if Claude
is started there with our (v2) hooks configured.

### 5.3 Host setup check

`hosts.check` returns:

```json
{"host":"devbox","checkedAt":1794420500,"items":[
  {"id":"tmux","label":"tmux","status":"ok","detail":"3.4 at /usr/bin/tmux"},
  {"id":"claude","label":"Claude Code","status":"warn","detail":"not found on PATH",
   "fix":"Install Claude Code on this host to run claude sessions here.",
   "fixCommand":"curl -fsSL https://claude.ai/install.sh | bash"}]}
```

`status` is `ok`, `warn` (works, but worth fixing) or `error` (blocks using
the host). `fix` and `fixCommand` are present only for `warn` and `error`.
`fixCommand` is a suggestion for the user to copy; the app never runs it.

Items, in this order. An item is omitted when it doesn't apply.

| `id` | Checks | `error` / `warn` when |
|---|---|---|
| `connection` | SSH hosts only: the connection and its target | `error` if the host isn't connected; the other items are then omitted |
| `tmux` | tmux is found (path) and its version | `error` if missing (install hint for the host's OS: `brew install tmux`, `sudo apt install tmux`, `sudo dnf install tmux`) or older than 3.2 |
| `shell` | The user's login shell (`$SHELL`) | `warn` if unset |
| `home` | The home directory | `error` if it can't be determined |
| `claude` | `claude` is on the PATH of a login shell (located only, never run) | `warn` if not found |
| `config` | The Claude Code config dir (`$CLAUDE_CONFIG_DIR` or `~/.claude`) and whether `settings.json` is valid JSON | `warn` if `settings.json` is invalid; a missing file is ok |
| `hooks` | Claude status hooks: installed, outdated or missing | `warn` if missing or outdated, with the fix "Use Install… in the sidebar" |

### 5.1 Directory suggestions

Used by the new-terminal dialog.

| Method | Params | Result |
|---|---|---|
| `remote.completeDir` | `host`, `prefix` (`""`, `~`, `~/…` or absolute) | `[{"path":"~/src/app/","gitRepo":true}]`. Directories matching `prefix`, in the same form as the prefix, each ending in `/`, sorted, at most 100. A prefix ending in `/` lists that directory's children. Hidden directories appear only when the typed name starts with `.`. A missing directory gives `[]`. |
| `remote.suggestDirs` | `host` | `{"recent":["~/work/api"],"repos":["~/src/app/"]}`. `recent` holds the directories used to create sessions on this host, most recent first, at most 10, kept across restarts. `repos` holds git repositories found under home, up to 3 levels deep, at most 50. The search takes at most ~3 s; when it times out, `repos` is partial. |

A host without filesystem browsing gives `[]` / `{"recent":[…],"repos":[]}`.
A successful `sessions.create` records its `dir` (as given; `""` is recorded as
`~`) in that host's recent list. Removing a host forgets its list.

## 6. Terminals

| Method | Params | Result |
|---|---|---|
| `term.attach` | `host`, `session`, `cols`, `rows` | `{"stream":<uint32>}` |
| `term.resize` | `stream`, `cols`, `rows` | `null` |
| `term.detach` | `stream` | `null`. The tmux session keeps running. |

**Binary frames**, in both directions: a 4-byte **big-endian** stream id
followed by the payload bytes.

- core → UI: terminal output. The first frames after attach are the
  **screen seed** (current screen contents and cursor position), followed by
  live output. The UI writes everything to xterm.js in order.
- UI → core: input bytes (keystrokes, paste), UTF-8 encoded as xterm.js
  produces them.
- A frame for an unknown stream id is ignored.

Event `term.closed`, sent when a stream ends for any reason other than
`term.detach`:
`{"type":"event","event":"term.closed","stream":7,"reason":"session ended"}`.

Streams belong to the WebSocket connection that opened them. When the
connection closes, all its streams are detached.
