# Pre-push checks and the build host

`make check` runs what the Ubuntu CI job runs (`.github/workflows/ci.yml`), in
the same order: the file-name case check, gofmt, `go vet`, `go test -race`, the
UI typecheck and unit tests, the browser e2e suites in light and dark, and the
Electron e2e suite under Xvfb. Each step prints its time.
`CHECK_SKIP_E2E=1 make check` stops after the unit tests.

Run it before pushing. CI still has to run on macOS and Windows; this check
catches the Linux failures beforehand.

`make check-remote` runs the same check on a build host over SSH
(`CHECK_HOST`, default `casp-build-01`):

- It rsyncs the working tree as it is, including uncommitted changes, to
  `~/claude-harness-check` on the host.
- Ignored files (`node_modules`, `bin`, `.env`, test output) are not sent.
- The host keeps its own `node_modules`; `npm ci` reruns only when
  `package-lock.json` changes.
- On failure, the host's `ui/e2e/test-results` (traces, screenshots) are copied
  back to the same place locally.
- Only one run at a time per host directory (`flock`).

## tmux safety

The tests start their own tmux servers on private sockets (`-L`). As a guard,
`scripts/check.sh` points `TMUX` at a throwaway decoy server and fails the run
if the decoy didn't survive. Never run the suites against a shell whose `TMUX`
is your real session without that guard.

## Setting up a build host (Ubuntu)

The host's user needs Go (the `toolchain` version in `go.mod`), Node 22, a C
compiler (`-race` needs cgo), tmux, Xvfb and Chromium's system libraries.
`scripts/check.sh` lists anything missing.

1. Go and Node, per user and without sudo: unpack the official tarballs, after
   checking their sha256 against go.dev/dl and nodejs.org's `SHASUMS256.txt`,
   into `~/.local/opt/{go,node}`, and link the binaries into `~/.local/bin`.
   `check.sh` puts `~/.local/bin` first on `PATH`.
2. System packages:

   ```sh
   sudo apt-get update && sudo apt-get install -y gcc tmux xvfb rsync
   # Electron needs GTK, which Playwright's Chromium deps don't include:
   sudo apt-get install -y libgtk-3-0t64   # libgtk-3-0 before Ubuntu 24.04
   ```

3. Chromium for Playwright, after one sync (`CHECK_SKIP_E2E=1 make check-remote`)
   so `ui/node_modules` exists on the host:

   ```sh
   cd ~/claude-harness-check/ui
   npx playwright install chromium
   sudo env PATH="$HOME/.local/bin:$PATH" npx playwright install-deps chromium
   ```

The local side needs `rsync` and an SSH config entry for the host.

`casp-build-01` (12 cores) is set up this way: Go and Node live in the `build`
user's `~/.local`. A full `make check-remote` takes about 70s there, against
about 3.5 minutes for `make check` on a 4-core machine. The browser e2e suites
take most of that time: 50s there and 150s on the 4-core machine.
