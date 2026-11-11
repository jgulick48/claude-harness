# Core ↔ UI protocol

How the Electron app launches `harness-core` and talks to it. See
[ARCHITECTURE.md](ARCHITECTURE.md) §3 and §7 for background. Protocol version: **2** (v2 adds requests/results, events, sessions and terminals; M1).

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
   {"type":"ready","addr":"127.0.0.1:54321","port":54321,"version":"0.1.0-dev","protocol":2}
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
| `GET /healthz` | Header `Authorization: Bearer <token>` | `200 {"ok":true,"version":"…","protocol":2}`; `401` if the token is missing or wrong |
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
| core → UI | `{"type":"hello","version":"0.1.0-dev","protocol":2}` | Sent right after connecting |
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
Host:    {"id":"local","name":"This computer","state":"connected","home":"/home/me","tmuxVersion":"3.2a"}
         state: "connected" | "unavailable" (+ "error": "tmux not found")
Session: {"id":"$3","name":"h-api-1","harnessName":"","managed":true,"cwd":"/home/me/work/api",
          "windows":1,"attached":0,"created":1794420293}
```

| Method | Params | Result |
|---|---|---|
| `hosts.list` | none | `[Host]`. M1 has only `local`. |
| `sessions.list` | `host` | `[Session]` |
| `sessions.create` | `host`, `dir` (optional, `~` allowed, default home), `name` (optional), `command` (`"shell"` default or `"claude"`) | `Session` |
| `sessions.kill` | `host`, `session` | `null` |
| `sessions.rename` | `host`, `session`, `name` (`""` clears it) | `null`. Sets the display name only. |

Event `sessions.changed`, pushed on connect for each host and whenever that
host's list changes:
`{"type":"event","event":"sessions.changed","host":"local","sessions":[Session]}`.

**Display label rule (UI):** `harnessName` if non-empty, otherwise the
basename of `cwd`, otherwise `name`. `managed: false` means the session was
started outside the app, so the UI tags it `external`.

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
