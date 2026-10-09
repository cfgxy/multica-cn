#!/usr/bin/env bash
# Contract test for scripts/web-release.sh.
#
# The script is the QA entry for serving a prebuilt Next.js standalone
# release on a slot frontend port. The cases below pin the pieces QA relies
# on: the manifest/verify digest chain (byte-identity with the packaged
# artifact), the start/stop lifecycle on a scratch port, and the env wiring —
# the slot env's bare PORT names the *backend*, and the launched server must
# never see it as its bind port.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$ROOT_DIR/scripts/web-release.sh"

failures=0

fail() {
  echo "FAIL: $1"
  failures=$((failures + 1))
}

command -v node > /dev/null || { echo "node is required for lifecycle cases"; exit 1; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"; [ -n "${FOREIGN_PID:-}" ] && kill "$FOREIGN_PID" 2>/dev/null' EXIT

export MULTICA_WEB_RELEASE_STATE_DIR="$TMP/state"

free_port() {
  local p
  for _ in $(seq 1 50); do
    p=$(( (RANDOM % 20000) + 40000 ))
    if ! ss -lHtn "sport = :$p" 2>/dev/null | grep -q .; then
      echo "$p"
      return 0
    fi
  done
  echo "no free port found" >&2
  return 1
}

# --- fixture: a minimal standalone-shaped release -------------------------
make_release() {
  local dir=$1
  mkdir -p "$dir/apps/web/.next/static/chunks" "$dir/apps/web/public"
  cat > "$dir/apps/web/server.js" <<'EOF'
const http = require("http");
const port = Number(process.env.PORT);
http.createServer((req, res) => {
  if (req.url === "/__env") {
    res.setHeader("content-type", "application/json");
    res.end(JSON.stringify({
      port: process.env.PORT,
      remote: process.env.REMOTE_API_URL,
      node_env: process.env.NODE_ENV,
      marker: process.env.MULTICA_TEST_MARKER,
    }));
    return;
  }
  res.end("ok");
}).listen(port, process.env.HOSTNAME || "127.0.0.1");
EOF
  echo "console.log('chunk')" > "$dir/apps/web/.next/static/chunks/main.test.js"
  echo "<svg/>" > "$dir/apps/web/public/logo.svg"
  mkdir -p "$dir/node_modules/traced-pkg"
  echo "module.exports = 1" > "$dir/node_modules/traced-pkg/index.js"
}

make_env_file() {
  local file=$1 web_port=$2
  cat > "$file" <<EOF
# synthetic slot env for the contract test
PORT=1
FRONTEND_PORT=$web_port
DATABASE_URL=postgres://qa:pw@127.0.0.1:1/db
MULTICA_TEST_MARKER=hello
EOF
}

# --- 1. manifest -> verify roundtrip --------------------------------------
REL="$TMP/rel"
make_release "$REL"
if ! bash "$SCRIPT" manifest --release-dir "$REL" --source-sha abcdef0123456789abcdef0123456789abcdef01 > "$TMP/m1.out" 2>&1; then
  fail "manifest exited non-zero: $(cat "$TMP/m1.out")"
fi
if ! bash "$SCRIPT" verify --release-dir "$REL" > "$TMP/v1.out" 2>&1; then
  fail "verify failed on pristine tree: $(cat "$TMP/v1.out")"
fi
grep -q "verify PASS" "$TMP/v1.out" || fail "verify output missing PASS line"

# --- 2. manifest is excluded from its own digest --------------------------
T1=$(sed -n 's/.*tree_sha256=\([0-9a-f]*\).*/\1/p' "$TMP/m1.out" | head -1)
bash "$SCRIPT" manifest --release-dir "$REL" --source-sha deadbeef --force > "$TMP/m2.out" 2>&1 \
  || fail "re-manifest --force exited non-zero"
T2=$(sed -n 's/.*tree_sha256=\([0-9a-f]*\).*/\1/p' "$TMP/m2.out" | head -1)
[ -n "$T1" ] && [ "$T1" = "$T2" ] || fail "digest changed when rewriting the manifest itself ($T1 vs $T2)"
grep -q '"source_sha": "deadbeef"' "$REL/RELEASE_MANIFEST.json" || fail "manifest did not record --source-sha"

# --- 3. tamper detection ---------------------------------------------------
echo "tampered" >> "$REL/apps/web/.next/static/chunks/main.test.js"
if bash "$SCRIPT" verify --release-dir "$REL" > "$TMP/v2.out" 2>&1; then
  fail "verify passed on a tampered tree"
fi
grep -q "verify FAIL" "$TMP/v2.out" || fail "verify output missing FAIL line"

# --- 4. missing manifest -> verify fails -----------------------------------
REL2="$TMP/rel2"
make_release "$REL2"
if bash "$SCRIPT" verify --release-dir "$REL2" > /dev/null 2>&1; then
  fail "verify passed without a manifest"
fi

# --- 5. start lifecycle on a scratch port ---------------------------------
REL3="$TMP/rel3"
make_release "$REL3"
bash "$SCRIPT" manifest --release-dir "$REL3" --source-sha abcdef0123456789abcdef0123456789abcdef01 > /dev/null
TEST_PORT=$(free_port)
ENV_FILE="$TMP/slot.env"
make_env_file "$ENV_FILE" "$TEST_PORT"

if ! bash "$SCRIPT" start --release-dir "$REL3" --env-file "$ENV_FILE" > "$TMP/s1.out" 2>&1; then
  fail "start exited non-zero: $(cat "$TMP/s1.out")"
fi
grep -q "serving verified tree" "$TMP/s1.out" || fail "start did not report the verified tree"

# env wiring: bind port must be FRONTEND_PORT, PORT(=1) and REMOTE_API_URL
# must be overridden, slot vars must pass through, NODE_ENV=production
env_json=$(curl -sf "http://127.0.0.1:$TEST_PORT/__env")
echo "$env_json" | grep -q "\"port\":\"$TEST_PORT\"" || fail "server not bound to FRONTEND_PORT: $env_json"
echo "$env_json" | grep -q '"remote":"http://127.0.0.1:1"' || fail "REMOTE_API_URL not derived from backend PORT: $env_json"
echo "$env_json" | grep -q '"node_env":"production"' || fail "NODE_ENV not forced to production: $env_json"
echo "$env_json" | grep -q '"marker":"hello"' || fail "slot env vars did not pass through: $env_json"
curl -sf "http://127.0.0.1:$TEST_PORT/" > /dev/null || fail "root probe failed"

if ! bash "$SCRIPT" status --env-file "$ENV_FILE" > "$TMP/st.out" 2>&1; then
  fail "status exited non-zero while running"
fi
grep -q "release_tree_sha256" "$TMP/st.out" || fail "status did not echo state.json (provenance chain)"

# idempotent start
if ! bash "$SCRIPT" start --release-dir "$REL3" --env-file "$ENV_FILE" > "$TMP/s2.out" 2>&1; then
  fail "second start exited non-zero (expected already-running idempotency)"
fi
grep -q "already running" "$TMP/s2.out" || fail "second start did not report already-running"

if ! bash "$SCRIPT" stop --env-file "$ENV_FILE" > "$TMP/stop.out" 2>&1; then
  fail "stop exited non-zero: $(cat "$TMP/stop.out")"
fi
if bash "$SCRIPT" status --env-file "$ENV_FILE" > /dev/null 2>&1; then
  fail "status still reports running after stop"
fi
ss -lHtn "sport = :$TEST_PORT" 2>/dev/null | grep -q . && fail "port $TEST_PORT still listening after stop"

# --- 6. start refuses a foreign port occupant -----------------------------
TEST_PORT2=$(free_port)
make_env_file "$TMP/slot2.env" "$TEST_PORT2"
node -e "require('http').createServer(()=>{}).listen($TEST_PORT2,'127.0.0.1')" &
FOREIGN_PID=$!
sleep 1
if bash "$SCRIPT" start --release-dir "$REL3" --env-file "$TMP/slot2.env" > "$TMP/s3.out" 2>&1; then
  fail "start succeeded on a foreign-occupied port"
fi
grep -q "foreign process" "$TMP/s3.out" || fail "start did not name the foreign occupant"

# --- 7. env file sanity ----------------------------------------------------
echo "FRONTEND_PORT=" > "$TMP/bad.env"
if bash "$SCRIPT" start --release-dir "$REL3" --env-file "$TMP/bad.env" > /dev/null 2>&1; then
  fail "start accepted an env file without FRONTEND_PORT"
fi

# --- 8. usage errors -------------------------------------------------------
if bash "$SCRIPT" bogus-verb > /dev/null 2>&1; then
  fail "unknown verb exited zero"
fi
if bash "$SCRIPT" verify > /dev/null 2>&1; then
  fail "verify without --release-dir exited zero"
fi

# --- summary ---------------------------------------------------------------
if [ "$failures" -gt 0 ]; then
  echo "web-release contract test: $failures failure(s)"
  exit 1
fi
echo "web-release contract test: all cases passed"
