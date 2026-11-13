#!/usr/bin/env bash
# Runs what the Ubuntu CI job runs (.github/workflows/ci.yml), in the same
# order, so failures show up before a push. Run from anywhere; `make check`.
#
# Tests never touch the user's tmux: they use private sockets (-L), and TMUX is
# pointed at a throwaway decoy server whose survival is checked at the end.
#
# Env: CHECK_SKIP_E2E=1 skips the Playwright suites.
set -euo pipefail
cd "$(dirname "$0")/.."
root=$PWD
export PATH="$HOME/.local/bin:$PATH" # per-user Go/Node installs (e.g. the build host)

step_start=0
step() {
  if [ "$step_start" != 0 ]; then printf '   (%ss)\n' $(($(date +%s) - step_start)); fi
  step_start=$(date +%s)
  printf '\n==> %s\n' "$*"
}
total_start=$(date +%s)

# Everything the run needs, reported at once.
missing=()
for t in go node npm tmux git; do command -v "$t" >/dev/null || missing+=("$t"); done
command -v cc >/dev/null || missing+=("a C compiler (go test -race needs cgo)")
if [ "${CHECK_SKIP_E2E:-}" != 1 ] && [ "$(uname)" != Darwin ]; then
  command -v xvfb-run >/dev/null || missing+=("xvfb-run (Electron e2e)")
fi
if [ ${#missing[@]} -gt 0 ]; then
  printf 'Missing: %s\n' "${missing[@]}" >&2
  echo "On Ubuntu: sudo apt-get install -y gcc tmux xvfb libgtk-3-0t64, plus Go and Node 22 (see docs/BUILD_HOST.md)" >&2
  exit 1
fi

# Decoy tmux server: anything that wrongly uses $TMUX hits this, not the user's.
decoy=$(mktemp -d "${TMPDIR:-/tmp}/hdecoy.XXXXXX")
tmux -S "$decoy/decoy" new-session -d
export TMUX="$decoy/decoy,0,0"
unset TMUX_PANE
cleanup() {
  status=$?
  if tmux -S "$decoy/decoy" has-session 2>/dev/null; then
    tmux -S "$decoy/decoy" kill-server 2>/dev/null || true
  else
    echo "!! the decoy tmux server is gone: a test killed the server \$TMUX pointed at" >&2
    status=1
  fi
  rm -rf "$decoy"
  if [ "$status" = 0 ]; then
    printf '   (%ss)\n\nAll checks passed in %ss.\n' $(($(date +%s) - step_start)) $(($(date +%s) - total_start))
  else
    printf '\nCHECK FAILED (after %ss).\n' $(($(date +%s) - total_start)) >&2
  fi
  exit "$status"
}
trap cleanup EXIT

if git rev-parse --git-dir >/dev/null 2>&1; then
  step "case-colliding file names"
  dups="$(git ls-files | sort -f | uniq -di)"
  if [ -n "$dups" ]; then
    printf 'These paths collide on case-insensitive file systems:\n%s\n' "$dups"
    exit 1
  fi
fi

step "gofmt"
unformatted="$(gofmt -l $(go list -f '{{.Dir}}' ./...))"
if [ -n "$unformatted" ]; then
  printf 'Not gofmt-formatted:\n%s\n' "$unformatted"
  exit 1
fi

step "go vet"
go vet ./...

step "go test -race"
go test -race ./...

step "build harness-core and sshtestd"
mkdir -p bin
go build -o bin/harness-core ./cmd/harness-core
go build -o bin/sshtestd ./internal/sshconn/sshtest/cmd/sshtestd

cd "$root/ui"
# npm ci only when the lockfile changed since the last install.
lock_sum=$(sha256sum package-lock.json | cut -d' ' -f1)
if [ ! -f node_modules/.check-lock-sum ] || [ "$(cat node_modules/.check-lock-sum)" != "$lock_sum" ]; then
  step "npm ci"
  npm ci --no-audit --no-fund
  echo "$lock_sum" >node_modules/.check-lock-sum
fi

step "UI typecheck"
npm run typecheck

step "UI unit tests"
npm test

if [ "${CHECK_SKIP_E2E:-}" = 1 ]; then
  step "e2e skipped (CHECK_SKIP_E2E=1)"
  exit 0
fi

step "Playwright Chromium"
npx playwright install chromium

step "E2E renderer tests (light and dark)"
npm run test:e2e:renderer

step "E2E electron tests"
if command -v xvfb-run >/dev/null; then
  xvfb-run -a npm run test:e2e:electron
elif [ "$(uname)" = Darwin ]; then
  npm run test:e2e:electron
else
  echo "xvfb-run not found (apt-get install xvfb); CI runs this suite under Xvfb" >&2
  exit 1
fi
