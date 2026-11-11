# claude-harness

A cross-platform desktop app (Windows, macOS, Linux) for running Claude Code across tmux sessions on many machines over SSH.

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the design and [docs/CORE_PROTOCOL.md](docs/CORE_PROTOCOL.md) for the core protocol.

## Claude Code instructions

`CLAUDE.md` holds the instructions Claude Code follows when working on this repository. Claude Code loads it automatically from the repo root; it isn't meant to be installed anywhere else.

- **Plan and delegate by cost:** plan tasks first and route subtasks to the cheapest suitable model (Haiku / Sonnet / Opus).
- **Feature branches and PRs:** one branch per feature, with a PR into `main` and full notes when it's ready.

## Development

Prerequisites:

- Go 1.27+
- Node 22
- tmux 3.2+ on remote hosts

Common tasks (macOS/Linux):

```sh
make ui-install   # install Electron app dependencies
make dev          # build bin/harness-core and start the app in dev mode
make test         # Go tests (-race) and UI tests
make package      # build bin/harness-core and create an unsigned app build
make help         # list all targets
```

The app launches the core binary from `bin/`. Set `HARNESS_CORE_PATH` to override the core binary path the app uses.

The core uses the local tmux server by default. For development and tests you can point it elsewhere:

| Variable | Effect |
|---|---|
| `HARNESS_TMUX_BIN` | tmux binary to run (default: `tmux` on `PATH`) |
| `HARNESS_TMUX_SOCKET` | use a separate tmux server, passed as `tmux -L <name>` |
| `HARNESS_TMUX_CONFIG` | tmux config file, passed as `-f` (e.g. `/dev/null`) |
| `HARNESS_HOSTS_FILE` | saved SSH hosts (default `<user config dir>/claude-harness/hosts.json`) |
| `HARNESS_SSH_CONFIG` | ssh config file (default `~/.ssh/config`) |
| `HARNESS_KNOWN_HOSTS` | known_hosts file (default `~/.ssh/known_hosts`) |

The test suites always use a private socket and `/dev/null` config, so they never touch your real tmux sessions.

### Browser end-to-end tests

Playwright drives the real UI against a real core and a private tmux server. There are two suites:

```sh
cd ui
npx playwright install chromium        # once
npm run test:e2e:renderer              # the renderer in headless Chromium (light and dark)
xvfb-run -a npm run test:e2e:electron  # the real Electron app (omit xvfb-run on macOS)
```

The suites need tmux, so they don't run on Windows. Screenshots go to `ui/e2e/screenshots/`, which is gitignored.

Every test core runs isolated: a private tmux socket, and a temporary `HOME`, `CLAUDE_CONFIG_DIR`, hosts file and state file. The tests drop `$TMUX` and refuse to run tmux without an explicit `-L` or `-S`. If you run them from inside tmux, it's still worth pointing `TMUX` at a throwaway server, for example `tmux -S /tmp/decoy new -d` and then `TMUX=/tmp/decoy,<pid>,0 npm run test:e2e:renderer`. That way, a future mistake can't reach your own session.

## SSH hosts

Click the server icon in the sidebar header and enter an `~/.ssh/config` alias or `user@host[:port]`. The app uses your existing SSH setup:

- **Keys** come from your SSH agent: `ssh-agent`/`SSH_AUTH_SOCK` on macOS and Linux, **Pageant** on Windows. On macOS and Linux, unencrypted key files (`IdentityFile`, `~/.ssh/id_*`) also work. Passphrase-protected keys must be loaded into the agent (`ssh-add`).
- **`~/.ssh/config`**: `HostName`, `User`, `Port`, `IdentityFile` and `ProxyJump` are honored.
- **Host keys** are checked against `~/.ssh/known_hosts`. On the first connection, the app shows the key's fingerprint and only trusts it when you confirm. A changed key is refused.
- The remote host needs **tmux 3.2 or newer**. Homebrew's tmux is found even when it's only on the login-shell `PATH`.

Saved hosts live in `hosts.json` in the app's config directory (`~/.config/claude-harness/` on Linux, `~/Library/Application Support/claude-harness/` on macOS, `%AppData%\claude-harness\` on Windows). Removing a host leaves its tmux sessions running on that machine.

## Releases & updates

To cut a release:

1. In the feature or fix PR itself, bump the version with `cd ui && npm version X.Y.Z --no-git-tag-version`, which updates `package.json` and `package-lock.json`. `main` is protected, so there's no separate version-bump PR. Use a minor bump for features and a patch bump for fixes.
2. After the PR merges, tag the merge commit `vX.Y.Z` (it must match `ui/package.json`) and push the tag.

The Release workflow builds installers for macOS (dmg/zip, arm64 + x64), Windows (NSIS) and Linux (AppImage) and publishes them to GitHub Releases.

Installed apps check for updates shortly after startup and every 4 hours (also via **Check for Updates…**). Windows and Linux (AppImage) download updates in the background and offer **Restart to install** (otherwise the update installs on next quit). Set `HARNESS_DISABLE_UPDATES=1` to turn update checks off.

Until signing is configured (see [docs/SIGNING.md](docs/SIGNING.md)), macOS builds are unsigned. On first launch use right-click → Open, or run `xattr -cr "/Applications/Claude Harness.app"`. Updates on macOS are manual until then: the app shows "Update vX available" with a button that opens the GitHub release page.

Signing and notarization (macOS Developer ID, Windows Azure Trusted Signing or a `.pfx`) turn on automatically when the repo secrets are added, and signed macOS builds auto-update. [docs/SIGNING.md](docs/SIGNING.md) lists what to obtain and the secret names. To test a signed build without cutting a tag, run a dry run, which uploads the installers as workflow artifacts and publishes nothing:

```sh
gh workflow run release.yml -f publish=false
```
