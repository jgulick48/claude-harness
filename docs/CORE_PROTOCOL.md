# Core ↔ UI protocol

How the Electron app launches `harness-core` and talks to it. See
[ARCHITECTURE.md](ARCHITECTURE.md) §3 and §7 for background. Protocol version: **1**.

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
   {"type":"ready","addr":"127.0.0.1:54321","port":54321,"version":"0.1.0-dev","protocol":1}
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
| `GET /healthz` | Header `Authorization: Bearer <token>` | `200 {"ok":true,"version":"…","protocol":1}`; `401` if the token is missing or wrong |
| `GET /ws?token=<token>` | Query parameter (browser WebSockets can't set headers) | WebSocket upgrade; `401` **before** upgrade if the token is missing or wrong |

The WebSocket server doesn't check the Origin header, because the Electron
renderer's origin varies (`http://localhost:<port>` in dev, `file://` when
packaged). The token is the access control.

## 4. WebSocket messages (v1)

All messages are JSON in text frames. Binary frames are reserved for terminal
data from M1 onward.

| Direction | Message | Notes |
|---|---|---|
| core → UI | `{"type":"hello","version":"0.1.0-dev","protocol":1}` | Sent right after connecting |
| UI → core | `{"type":"ping","id":1}` | `id` is an integer chosen by the client |
| core → UI | `{"type":"pong","id":1}` | Echoes the `id` |
| core → UI | `{"type":"error","id":1,"message":"unknown message type: foo"}` | Unknown `type`. `id` is echoed if the request had one, otherwise omitted. The connection stays open. |
| core → UI | `{"type":"error","message":"invalid JSON"}` | Malformed message. The connection stays open. |

If the protocol number in `hello` doesn't match the UI's expected protocol,
the UI shows a version-mismatch error instead of continuing.
