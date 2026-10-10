#!/usr/bin/env bash
# Acceptance tests for scripts/e2e-release-entry.sh (RUYI-632).
#
# Style follows dev-env-resource.test.sh: no real build, no real load — a
# throwaway MULTICA_SLOTS_HOME with a fake slot manifest, a fake git worktree
# and a fabricated .next artifact prove manifest writing, digest aggregation,
# verify pass/mismatch paths and the dirty-tree guard. The only external
# process invoked is sha256sum/find/node — no pnpm, no next build.
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
script="$root_dir/scripts/e2e-release-entry.sh"
tmp_dir="$(mktemp -d)"
cleanup() { rm -rf "$tmp_dir"; }
trap cleanup EXIT

mkdir -p "$tmp_dir/home"
export HOME="$tmp_dir/home"
export MULTICA_SLOTS_HOME="$tmp_dir/slots"
export PATH="$tmp_dir/bin:$PATH"

passed=0
check() { # $1 = label, $2 = expected, $3 = actual
  if [ "$2" = "$3" ]; then
    passed=$((passed + 1))
  else
    printf 'FAIL %s\n  expected: %s\n  actual:   %s\n' "$1" "$2" "$3" >&2
    exit 1
  fi
}

# A fake `pnpm` so cmd_build's node_modules check never triggers a real
# install; the fake build path skips pnpm entirely (node_modules pre-exists).
mkdir -p "$tmp_dir/bin"
printf '#!/bin/sh\nexit 0\n' > "$tmp_dir/bin/pnpm"
chmod +x "$tmp_dir/bin/pnpm"

# --- fixture: slot manifest + fake worktree with a committed state ---------
SLOTS="$MULTICA_SLOTS_HOME"
WT="$tmp_dir/fake-checkout"
mkdir -p "$SLOTS/dev1" "$WT/apps/web/.next/static" "$WT/apps/web/.next/cache/webpack" "$WT/node_modules"
git -C "$WT" init -q
git -C "$WT" config user.email t@t
git -C "$WT" config user.name t
echo '{"name":"fake"}' > "$WT/package.json"
echo "buildid-1234" > "$WT/apps/web/.next/BUILD_ID"
echo "window.x=1" > "$WT/apps/web/.next/static/chunk.js"
echo "cache-junk" > "$WT/apps/web/.next/cache/webpack/0.pack"
git -C "$WT" add -A
git -C "$WT" commit -qm init
SHA="$(git -C "$WT" rev-parse HEAD)"

ENV_FILE="$tmp_dir/slot-env"
cat > "$ENV_FILE" <<EOF
FRONTEND_PORT=13801
NEXT_PUBLIC_API_URL=http://localhost:21801
NEXT_PUBLIC_WS_URL=ws://localhost:21801/ws
EOF

cat > "$SLOTS/dev1/manifest.env" <<EOF
NAME=dev1
ISSUE=RUYI-632
DIR=$WT
CODE_SHA=$SHA
ENV_FILE=$ENV_FILE
BACKEND_PORT=21801
FRONTEND_PORT=13801
EOF

usage_exit() { bash "$script" "$@" >/dev/null 2>&1; echo $?; }

# --- usage / precondition guards ------------------------------------------
check "missing args exit 2" "2" "$(usage_exit)"
check "unknown verb exit 2" "2" "$(usage_exit dev1 frobnicate)"
check "missing manifest fails" "1" "$(usage_exit dev2 build)"

# --- build: manifest content and digest definition -------------------------
out="$(bash "$script" dev1 build)"
manifest="$WT/e2e-artifacts/manifest.json"
hashes="$WT/e2e-artifacts/file-hashes.txt"
[ -f "$manifest" ] || { echo "FAIL build did not write manifest" >&2; exit 1; }

check "manifest git_sha" "$SHA" "$(node -e "console.log(require('$manifest').source.git_sha)")"
check "manifest branch" "master" "$(node -e "console.log(require('$manifest').source.git_branch)")"
check "manifest build_id" "buildid-1234" "$(node -e "console.log(require('$manifest').build.build_id)")"
check "manifest mode" "production" "$(node -e "console.log(require('$manifest').build.mode)")"
check "manifest next_public_bake recorded" "true" "$(node -e "const b=require('$manifest').build.next_public_bake;console.log(b.includes('NEXT_PUBLIC_API_URL=http://localhost:21801')?'true':'false')")"
check "hashes cover served artifact only (cache excluded)" "2" "$(wc -l < "$hashes" | tr -d ' ')"
check "hashes line format" "1" "$(grep -cE '^[0-9a-f]{64}  \.next/' "$hashes" >/dev/null && echo 1 || echo 0)"
expected_agg="$(sha256sum "$hashes" | cut -d' ' -f1)"
check "aggregate = sha256 of hashes file" "$expected_agg" "$(node -e "console.log(require('$manifest').artifact.sha256)")"
check "40-hex git sha in manifest" "1" "$(node -e "console.log(/^[0-9a-f]{40}$/.test(require('$manifest').source.git_sha)?1:0)")"

# --- verify: pass on untouched artifact ------------------------------------
out="$(bash "$script" dev1 verify)"
check "verify PASS line" "1" "$(printf '%s' "$out" | grep -c '^e2e-release-entry: PASS')"

# --- verify: detects artifact drift, evidence file untouched ---------------
echo "tampered" > "$WT/apps/web/.next/static/chunk.js"
hashes_before="$(sha256sum "$hashes" | cut -d' ' -f1)"
verify_exit="$(bash "$script" dev1 verify >/dev/null 2>&1; echo $?)"
check "verify fails on drift" "1" "$verify_exit"
check "evidence hashes file not overwritten by verify" "$hashes_before" "$(sha256sum "$hashes" | cut -d' ' -f1)"
printf 'window.x=1\n' > "$WT/apps/web/.next/static/chunk.js"

# --- dirty-tree guard: uncommitted tracked change blocks build -------------
echo "dirty" > "$WT/package.json"
build_exit="$(bash "$script" dev1 build >/dev/null 2>&1; echo $?)"
check "build refuses dirty tree" "1" "$build_exit"
git -C "$WT" checkout -q -- package.json

# --- dirty-tree guard: AGENTS.md exemption ---------------------------------
echo "platform injection" > "$WT/AGENTS.md"
out="$(bash "$script" dev1 build >/dev/null 2>&1; echo $?)"
check "build tolerates AGENTS.md-only dirty state" "0" "$out"
rm -f "$WT/AGENTS.md"

printf 'e2e-release-entry.test: %d checks passed\n' "$passed"

# --- static assertions: slot + Makefile + docs integration ------------------
# (config-level proof, no processes started — dev-env-resource.test.sh style)
grep -q 'MULTICA_WEB_MODE:-dev' "$root_dir/scripts/dev-env.sh" || { echo "FAIL dev-env.sh missing web mode switch" >&2; exit 1; }
grep -q 'MULTICA_WEB_MODE=release' "$root_dir/scripts/dev-env.sh" || { echo "FAIL dev-env.sh missing release usage hint" >&2; exit 1; }
grep -q 'web-release:' "$root_dir/Makefile" || { echo "FAIL Makefile missing web-release target" >&2; exit 1; }
grep -q 'BUILD_ID' "$root_dir/scripts/dev-env.sh" || { echo "FAIL dev-env.sh missing BUILD_ID guard for release mode" >&2; exit 1; }
grep -q 'e2e-release-entry' "$root_dir/e2e/README.md" || { echo "FAIL e2e/README.md missing runbook wiring" >&2; exit 1; }
grep -q '^e2e-artifacts/' "$root_dir/.gitignore" || { echo "FAIL .gitignore missing e2e-artifacts/" >&2; exit 1; }
passed=$((passed + 5))
printf 'e2e-release-entry.test: %d checks passed (incl. 5 static)\n' "$passed"
