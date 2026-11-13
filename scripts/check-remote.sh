#!/usr/bin/env bash
# Runs scripts/check.sh on a build host over SSH, on the working tree as it is
# now (committed or not). The tree is rsynced to a scratch directory there;
# ignored files (node_modules, bin, .env, test output) are not sent, and the
# host keeps its own node_modules between runs. On failure the e2e results
# are copied back to ui/e2e/test-results.
#
#   make check-remote                  # default host
#   CHECK_HOST=other make check-remote
set -euo pipefail
cd "$(dirname "$0")/.."
host=${CHECK_HOST:-casp-build-01}
dir=${CHECK_REMOTE_DIR:-claude-harness-check} # relative to the remote home

echo "==> syncing the working tree to $host:~/$dir"
rsync -az --delete --filter=':- .gitignore' --exclude=/.env \
  ./ "$host:$dir/"

# One run at a time per host directory. With a terminal, -t lets Ctrl-C
# reach the remote run.
tty=()
if [ -t 0 ]; then tty=(-t); fi
status=0
ssh "${tty[@]}" "$host" "cd $dir && flock -n .check.lock env CHECK_SKIP_E2E=${CHECK_SKIP_E2E:-} scripts/check.sh" || status=$?
if [ "$status" != 0 ]; then
  rsync -az "$host:$dir/ui/e2e/test-results/" ui/e2e/test-results/ 2>/dev/null &&
    echo "e2e results copied to ui/e2e/test-results/" || true
fi
exit "$status"
