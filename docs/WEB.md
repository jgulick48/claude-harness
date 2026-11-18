# Web UI and Docker server

`harness-core serve` runs core as a long-lived server that also serves the UI
to browsers. Run it in Docker on an always-on box; every computer on the
network opens the same hosts and tmux sessions in a browser, so switching
computers keeps your sessions (they are tmux sessions on the SSH hosts and
never stop).

The desktop app is unchanged: it still launches its own loopback core
(CORE_PROTOCOL.md §1). `serve` is a separate mode of the same binary.

## 1. Shape

```
browser ──HTTPS (ingress / reverse proxy, or --tls-*)──▶ harness-core serve
   │  /login, /auth/oidc/*   local accounts + Authelia OIDC
   │  /api/me                 who am I, the server's SSH public key
   │  /ws                     same WebSocket protocol as the desktop (CORE_PROTOCOL.md)
   │  /*                      the renderer, built for the web (embedded)
   └───────────────────────────────────────────────────────▶ SSH hosts (own key)
```

- The front handler (`internal/webserve`) authenticates the browser with a
  session cookie, then hands `/ws` to the unchanged `api.NewServer` handler,
  injecting a random per-process internal token. The WebSocket protocol is
  exactly the desktop one.
- There is no local host in serve mode by default: "local" would be the
  container. `--local` keeps it (useful when running serve on a real box).

## 2. Running

```
harness-core serve [--listen :8080] [--data /data] [--public-url https://harness.example.com]
                   [--tls-cert f --tls-key f] [--trust-proxy] [--local] [--web-dir dir]
```

| Flag / env | Meaning |
|---|---|
| `--listen` / `HARNESS_LISTEN` | Default `:8080`. Any address is allowed (unlike the desktop mode). |
| `--data` / `HARNESS_DATA_DIR` | State directory. Core sets `HOME` and `XDG_CONFIG_HOME` under it at startup if they are not already inside it, so hosts.json, recent dirs, usage history, `~/.ssh/known_hosts`, `~/.ssh/config` and the key all live here. Default `/data`. |
| `--public-url` / `HARNESS_PUBLIC_URL` | External URL. Required for OIDC (redirect URI) and used for the Origin check. |
| `--tls-cert`, `--tls-key` | Serve HTTPS directly. Without them core serves HTTP and expects a TLS-terminating proxy in front. |
| `--trust-proxy` / `HARNESS_TRUST_PROXY=1` | Honour `X-Forwarded-For` / `X-Forwarded-Proto` (rate limiting, Secure cookies). |
| `--web-dir` | Serve the UI from a directory instead of the embedded copy (development). |

Startup refuses to run (exit 2) when no login method is configured, and
warns loudly when neither TLS nor a `https://` public URL is set: the cookie
then travels in clear text and browsers block clipboard paste.

Shutdown: SIGINT/SIGTERM, graceful. No stdin lifetime and no `ready` line.

## 3. SSH key

On first start core generates `<data>/.ssh/id_ed25519` (0600) and `.pub`
(comment `claude-harness@<hostname>`). Because `HOME` is the data dir, sshconn
already tries this key for every host. `GET /api/me` returns the public key;
the web UI shows it in **Server SSH key…** with a copy button and the line to
add on a host:

```
echo 'ssh-ed25519 AAAA… claude-harness@box' >> ~/.ssh/authorized_keys
```

Host keys are trusted the same way as in the desktop app (the add-host flow
asks), stored in `<data>/.ssh/known_hosts`.

## 4. Login

Same pattern as the other services behind Authelia (Grafana, ArgoCD): single
sign-on through Authelia OIDC, with a local account kept as a break-glass
login. Either method may be configured alone.

### Local accounts

- Stored in `<data>/users.json` (0600), bcrypt hashes (cost 12).
- Bootstrap: `HARNESS_ADMIN_USER` (default `admin`) and `HARNESS_ADMIN_PASSWORD`
  or `HARNESS_ADMIN_PASSWORD_FILE` create or reset that account at startup.
- `harness-core user set <name>` (password on stdin, or prompted) and
  `harness-core user rm <name>`, both with `--data`.
- Login is rate limited: 5 failures per IP per 15 minutes, then 429.
  Comparison runs bcrypt even for unknown users (no username probing).
- Disable local login entirely with `HARNESS_LOCAL_LOGIN=0`.

### Authelia (OIDC)

| Env | Meaning |
|---|---|
| `HARNESS_OIDC_ISSUER` | e.g. `https://auth.threegm.com` |
| `HARNESS_OIDC_CLIENT_ID` | e.g. `claude-harness` |
| `HARNESS_OIDC_CLIENT_SECRET` / `_FILE` | Client secret |
| `HARNESS_OIDC_NAME` | Button label, default `Authelia` |
| `HARNESS_OIDC_ALLOWED_GROUPS` | Comma list; default `admins`. Empty string allows any authenticated user. |
| `HARNESS_OIDC_SCOPES` | Default `openid profile email groups` |

Authorization code flow with PKCE (S256), `state` and `nonce` in a short-lived
signed cookie. Redirect URI is `<public-url>/auth/oidc/callback`. The user is
`preferred_username` (fallback `email`, then `sub`); groups from the ID token
`groups` claim (fallback: userinfo). Not in an allowed group → 403 page.

Authelia client:

```yaml
identity_providers:
  oidc:
    clients:
      - client_id: claude-harness
        client_name: Claude Harness
        client_secret: '$pbkdf2-sha512$...'   # authelia crypto hash generate
        public: false
        authorization_policy: two_factor
        require_pkce: true
        pkce_challenge_method: S256
        redirect_uris:
          - https://harness.threegm.com/auth/oidc/callback
        scopes: [openid, profile, email, groups]
        response_types: [code]
        grant_types: [authorization_code]
        token_endpoint_auth_method: client_secret_basic
```

### Sessions

- Cookie `harness_session`: HMAC-SHA256-signed `{user, method, exp}`,
  `HttpOnly`, `SameSite=Lax`, `Path=/`, `Secure` when the request is HTTPS
  (directly or via a trusted proxy). Lifetime 7 days.
- Signing key `<data>/session.key` (32 random bytes, 0600), so logins survive
  restarts. Deleting it logs everyone out.
- CSRF: every POST and the `/ws` upgrade require `Origin` to match the
  public URL (or the request's own host when no public URL is set).

## 5. Endpoints

| Endpoint | Auth | Notes |
|---|---|---|
| `GET /livez` | none | `200 ok`, for Docker/k8s health checks. Reveals nothing. |
| `GET /login` | none | Server-rendered page: username/password form (if local login is on) and a "Sign in with Authelia" button (if OIDC is on). `?error=` shows a message. |
| `POST /login` | none | Form `username`, `password`, `next`. 303 to `next` (same-origin paths only) or `/login?error=…`. |
| `GET /auth/oidc/start` | none | Redirects to the issuer. |
| `GET /auth/oidc/callback` | none | Completes the flow, sets the cookie, 303 to `/`. |
| `POST /logout` | cookie | Clears the cookie, 303 to `/login`. |
| `GET /api/me` | cookie | `{"user":"…","method":"local"\|"oidc","sshPublicKey":"ssh-ed25519 …","version":"…"}`; 401 JSON without a valid cookie. |
| `GET /ws` | cookie + Origin | Forwarded to the api handler. 401 before upgrade otherwise. |
| `GET /*` | cookie | Static UI; unknown paths serve `index.html`. Without a cookie: 303 to `/login?next=…`. Responses carry `Content-Security-Policy: default-src 'self'; connect-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: same-origin`. |

## 6. Web build of the UI

`npm run build:web` (in `ui/`) builds the renderer with a browser entry,
`src/renderer/src/web/main.tsx`, into `ui/out/web`. That entry installs
`window.harness` before rendering the app:

| Bridge method | Browser version |
|---|---|
| `getCoreInfo` | `{url: ws(s)://<location.host>/ws, token: ''}` (the cookie authenticates) |
| `onCoreStatus` | Emits `ready` once |
| `onUpdateStatus`, update calls | `unsupported` / no-ops (the Docker image is updated by pulling) |
| `onSystemResume` | Fires on `visibilitychange` to visible after being hidden > 30 s, and on `online` |
| `setAttentionCount` | Prefixes the tab title: `(2) Claude Harness` |
| `focusWindow` | `window.focus()` |
| `read/writeClipboard` | `navigator.clipboard` |
| `setThemeSource` | No-op |
| `showTerminalMenu` | Small in-page menu at the pointer with the same items |
| `openLink` | `window.open(url, '_blank', 'noopener')` for http(s) only |

Web-only extras live on `window.harnessWeb` (`me`, `logout()`), not on
`window.harness`, so the Electron bridge keys stay the same. When present the
app shows the signed-in user, **Sign out**, and **Server SSH key…**. A `/ws`
close right after connecting, or a 401 from `/api/me`, sends the browser to
`/login`.

Layouts and appearance stay in each browser's localStorage.

## 7. Docker

The `Dockerfile` at the repo root has three stages: node 22 builds the web
UI, Go builds `harness-core` with the UI embedded (`internal/webserve/dist/web`),
then `gcr.io/distroless/static-debian12:nonroot` runs it as 65532 with
`HOME=/data`, `VOLUME /data`, `EXPOSE 8080`, and the default
`ENTRYPOINT ["/harness-core", "serve"]`.

```
docker run -d --name claude-harness -p 8080:8080 -v harness-data:/data \
  -e HARNESS_PUBLIC_URL=https://harness.threegm.com \
  -e HARNESS_ADMIN_PASSWORD_FILE=/run/secrets/admin \
  -e HARNESS_OIDC_ISSUER=https://auth.threegm.com \
  -e HARNESS_OIDC_CLIENT_ID=claude-harness \
  -e HARNESS_OIDC_CLIENT_SECRET_FILE=/run/secrets/oidc \
  docker.io/jgulick48/claude-harness:latest
```

`deploy/docker-compose.yml` has the same as a compose file. `make docker.build`
and `make docker.push` (buildx, `linux/amd64,linux/arm64`, the shared
`casp-build` builder) build and publish the image, tagged with the
`ui/package.json` version.
