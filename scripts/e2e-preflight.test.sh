#!/usr/bin/env bash
# Acceptance tests for scripts/e2e-preflight.sh (RUYI-632).
#
# A throwaway MULTICA_SLOTS_HOME + one stub HTTP process (node, loopback,
# ~zero load) plays the slot's api and web: /health answers the slot-bound
# commit, / serves the entry, /api/config serves feature flags. Closed ports
# play the unreachable Redis. Scenarios:
#   1. healthy slot with release manifest → preflight exits 0, cites flags;
#   2. composio_mcp_apps off → explicit WARN naming the agent-mcp consequence;
#   3. REDIS_URL pointing at a dead port → WARN naming DB fallback attribution;
#   4. web down → FAIL, exit 1;
#   5. api serving a foreign commit → WARN on the commit mismatch.
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
script="$root_dir/scripts/e2e-preflight.sh"
tmp_dir="$(mktemp -d)"
cleanup() {
  [ -n "${stub_pid:-}" ] && kill "$stub_pid" 2>/dev/null || true
  rm -rf "$tmp_dir"
}
trap cleanup EXIT

mkdir -p "$tmp_dir/home"
export HOME="$tmp_dir/home"
export MULTICA_SLOTS_HOME="$tmp_dir/slots"

passed=0
check() {
  if [ "$2" = "$3" ]; then
    passed=$((passed + 1))
  else
    printf 'FAIL %s\n  expected: %s\n  actual:   %s\n' "$1" "$2" "$3" >&2
    exit 1
  fi
}
expect_line() { # $1 = output, $2 = grep -F pattern, $3 = label
  if printf '%s' "$1" | grep -qF "$2"; then
    passed=$((passed + 1))
  else
    printf 'FAIL %s — output missing: %s\n--- output ---\n%s\n' "$3" "$2" "$1" >&2
    exit 1
  fi
}

API_PORT=23801
WEB_PORT=13811
DEAD_PORT=13991 # nothing listens here in the test env
SHA="$(printf 'a%.0s' $(seq 1 40))"

mkdir -p "$MULTICA_SLOTS_HOME/dev1" "$tmp_dir/fake-wt/e2e-artifacts"
ENV_FILE="$tmp_dir/slot-env"
cat > "$ENV_FILE" <<EOF
PORT=$API_PORT
FRONTEND_PORT=$WEB_PORT
REDIS_URL=redis://127.0.0.1:$DEAD_PORT
DATABASE_URL=postgres://dev1_app:x@127.0.0.1:$API_PORT/multica_dev1?sslmode=disable
EOF
cat > "$MULTICA_SLOTS_HOME/dev1/manifest.env" <<EOF
NAME=dev1
ISSUE=RUYI-632
DIR=$tmp_dir/fake-wt
CODE_SHA=$SHA
ENV_FILE=$ENV_FILE
BACKEND_PORT=$API_PORT
FRONTEND_PORT=$WEB_PORT
EOF

# Minimal fake worktree: e2e-release-entry verify needs a git repo whose HEAD
# matches the manifest sha and an .next/BUILD_ID plus hashes file. Fabricate
# the artifact evidence directly instead of running a real build.
mkdir -p "$tmp_dir/fake-wt/apps/web/.next"
cd "$tmp_dir/fake-wt"
git init -q
git config user.email t@t
git config user.name t
echo '{"name":"fake"}' > package.json
git add -A
GIT_AUTHOR_DATE="2020-01-01T00:00:00Z" GIT_COMMITTER_DATE="2020-01-01T00:00:00Z" git commit -qm init --no-gpg-sign
# Rewind the repo until HEAD == manifest SHA is impractical; instead record a
# manifest whose sha we take from this repo (deterministic fixture).
FIXTURE_SHA="$(git rev-parse HEAD)"
sed -i "s/^CODE_SHA=.*/CODE_SHA=$FIXTURE_SHA/" "$MULTICA_SLOTS_HOME/dev1/manifest.env"
echo "stub-build-id" > apps/web/.next/BUILD_ID
HASHES="$tmp_dir/fake-wt/e2e-artifacts/file-hashes.txt"
printf '%s  .next/BUILD_ID\n' "$(sha256sum apps/web/.next/BUILD_ID | cut -d' ' -f1)" > "$HASHES"
node -e '
  const fs = require("fs");
  const crypto = require("crypto");
  const hashes = fs.readFileSync(process.argv[1]);
  fs.writeFileSync(process.argv[2], JSON.stringify({
    schema: "multica-e2e-release-entry/1",
    source: { git_sha: process.argv[3], git_branch: "master", worktree_path: process.argv[4], dirty_exempt_files: ["AGENTS.md"] },
    build: { built_at: "2026-10-10T00:00:00+08:00", mode: "production", env_file: "env", env_file_sha256: "x", next_public_bake: "", tool_versions: {}, build_id: "stub-build-id" },
    artifact: { root: "apps/web/.next", file_count: 1, total_bytes: hashes.length, sha256: crypto.createHash("sha256").update(hashes).digest("hex"), hashes_file: "e2e-artifacts/file-hashes.txt" },
  }, null, 2) + "\n");
' "$HASHES" "$tmp_dir/fake-wt/e2e-artifacts/manifest.json" "$FIXTURE_SHA" "$tmp_dir/fake-wt"
cd "$root_dir"

# Stub slot: api + web on the two loopback ports. /health reports the commit,
# /api/config reports composio_mcp_apps=true for scenario 1.
cat > "$tmp_dir/stub.js" <<EOF
const http = require("http");
const commit = process.env.STUB_COMMIT;
const composio = process.env.STUB_COMPOSIO === "true";
const handlers = {
  $API_PORT: (req, res) => {
    if (req.url === "/health") { res.end(JSON.stringify({ status: "ok", commit, pid: 1, started_at: new Date().toISOString() })); return; }
    res.statusCode = 404; res.end();
  },
  $WEB_PORT: (req, res) => {
    if (req.url === "/api/config") { res.end(JSON.stringify({ feature_flags: { composio_mcp_apps: composio } })); return; }
    res.end("<html>stub web entry</html>");
  },
};
for (const [port, handler] of Object.entries(handlers)) {
  http.createServer(handler).listen(Number(port), "127.0.0.1");
}
EOF
STUB_COMMIT="$FIXTURE_SHA" STUB_COMPOSIO=true node "$tmp_dir/stub.js" &
stub_pid=$!
sleep 0.5

# --- scenario 1: healthy slot, release entry verified ----------------------
out="$(bash "$script" dev1)"; rc=$?
check "healthy slot exits 0" "0" "$rc"
expect_line "$out" "[PASS] api health" "scenario1 api health"
expect_line "$out" "[PASS] release entry" "scenario1 release entry verified"
expect_line "$out" '"composio_mcp_apps":true' "scenario1 composio flag recorded"
# REDIS_URL points at the dead port → must WARN, never PASS.
if printf '%s' "$out" | grep -qF '[PASS] redis'; then
  echo "FAIL scenario1 — redis should be unreachable (dead port)" >&2
  exit 1
fi
passed=$((passed + 1))
expect_line "$out" "DB fallback" "scenario1 dead redis warns about DB fallback"

# --- scenario 2: composio flag off → WARN names the agent-mcp consequence --
kill "$stub_pid" 2>/dev/null || true
stub_pid=""
sleep 0.3
STUB_COMMIT="$FIXTURE_SHA" STUB_COMPOSIO=false node "$tmp_dir/stub.js" &
stub_pid=$!
sleep 0.4
out="$(bash "$script" dev1)"
expect_line "$out" "e2e/agent-mcp.spec.ts" "scenario2 warn names the dependent spec"
expect_line "$out" "FF_COMPOSIO_MCP_APPS=true" "scenario2 warn names the remediation"
kill "$stub_pid" 2>/dev/null || true
sleep 0.2

# Restart the passing stub for the remaining scenarios.
STUB_COMMIT="$FIXTURE_SHA" STUB_COMPOSIO=true node "$tmp_dir/stub.js" &
stub_pid=$!
sleep 0.4

# --- scenario 3: api bound-SHA mismatch → commit-mismatch WARN -------------
sed -i "s/^CODE_SHA=.*/CODE_SHA=$SHA/" "$MULTICA_SLOTS_HOME/dev1/manifest.env"
out="$(bash "$script" dev1 || true)"
expect_line "$out" "[WARN] api commit" "scenario3 foreign commit warned"
sed -i "s/^CODE_SHA=.*/CODE_SHA=$FIXTURE_SHA/" "$MULTICA_SLOTS_HOME/dev1/manifest.env"

# --- scenario 4: web down → FAIL, exit 1 -----------------------------------
kill "$stub_pid" 2>/dev/null || true
stub_pid=""
sleep 0.4
rc=0
out="$(bash "$script" dev1 2>/dev/null)" || rc=$?
check "web down exits 1" "1" "$rc"
expect_line "$out" "[FAIL] web entry" "scenario4 web entry FAIL"

printf 'e2e-preflight.test: %d checks passed\n' "$passed"
