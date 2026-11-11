# claude-harness

A cross-platform desktop app (Windows, macOS, Linux) for running Claude Code across tmux sessions on many machines over SSH.

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the design and [docs/CORE_PROTOCOL.md](docs/CORE_PROTOCOL.md) for the core protocol.

## Claude Code config

Version-controlled configuration for Claude Code.

### Contents

- `CLAUDE.md` — global instructions loaded into every Claude Code session:
  - **Plan and delegate by cost** — plan tasks first and route subtasks to the cheapest suitable model (Haiku / Sonnet / Opus).
  - **Feature branches and PRs** — one branch per feature, PR into `main` with full notes when ready.

### Install

Symlink the repo's `CLAUDE.md` into place so edits here apply globally:

```sh
ln -sf "$(pwd)/CLAUDE.md" ~/.claude/CLAUDE.md
```

## Development

Prerequisites:

- Go 1.25+
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

## Releases & updates

To cut a release: bump `version` in `ui/package.json`, commit, tag `vX.Y.Z` (matching that version) and push the tag. The Release workflow builds installers for macOS (dmg/zip, arm64 + x64), Windows (NSIS) and Linux (AppImage) and publishes them to GitHub Releases.

Installed apps check for updates shortly after startup and every 4 hours (also via **Check for Updates…**). Windows and Linux (AppImage) download updates in the background and offer **Restart to install** (otherwise the update installs on next quit). Set `HARNESS_DISABLE_UPDATES=1` to turn update checks off.

macOS builds are unsigned (no Apple Developer account yet). On first launch use right-click → Open, or run `xattr -cr "/Applications/Claude Harness.app"`. Updates on macOS are manual: the app shows "Update vX available" with a button that opens the GitHub release page.
