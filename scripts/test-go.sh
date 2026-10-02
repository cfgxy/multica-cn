#!/usr/bin/env bash
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
GUARD_SCRIPT="$SCRIPT_DIR/go-test-with-agent-cli-guard.sh"

usage() {
  echo "usage: $0 [--race] [--verbose]" >&2
}

# Flags compose so CI and local runs share one entrypoint. `--verbose` emits
# per-test lines: at package level a bare `ok` cannot distinguish a passed
# suite from a skipped one, which is how the REDIS_TEST_URL-gated tests used
# to disappear from every log without a trace (RUYI-331).
go_test_args=(test)
while [ "$#" -gt 0 ]; do
  case "$1" in
    --race) go_test_args+=(-race) ;;
    --verbose) go_test_args+=(-v) ;;
    *)
      usage
      exit 2
      ;;
  esac
  shift
done

cd "$REPO_ROOT/server"
packages=$(go list ./...)
regular_packages=()
for package in $packages; do
  case "$package" in
    */pkg/agent|*/pkg/agent/*) ;;
    *) regular_packages+=("$package") ;;
  esac
done

"$GUARD_SCRIPT" -- go "${go_test_args[@]}" "${regular_packages[@]}"
# Subprocess-backed agent tests have hard deadlines. Limit both package and
# within-package parallelism so race builds do not starve their parent loops.
"$GUARD_SCRIPT" -- go "${go_test_args[@]}" -p 2 -parallel 2 ./pkg/agent/...
