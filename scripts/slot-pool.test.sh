#!/usr/bin/env bash
# slot-pool.test.sh — E3 slot tool layer pool backend (skeleton contract).
#
# Everything runs against a fake curl: it records every request (headers +
# body, in order) and serves canned JSON-RPC responses from a response dir.
# The test pins the MCP conversation (initialize → tools/call), the exact
# acquire parameters, the guard-before-release ordering, fail-closed and
# override semantics, and the lease manifest lifecycle. Negative assertions
# pin error messages: removing the owner guard or the guard pre-hook must
# flip the matching case red.
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
pool="$root_dir/scripts/slot-pool.sh"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

fake_bin="$tmp_dir/bin"; resp_dir="$tmp_dir/resp"; log_dir="$tmp_dir/log"
mkdir -p "$fake_bin" "$resp_dir" "$log_dir"
export PATH="$fake_bin:$PATH"

home="$tmp_dir/home"; mkdir -p "$home"
export HOME="$home"
export MULTICA_SLOT_POOL_HOME="$tmp_dir/pool-home"
export MULTICA_POOL_URL="http://pool.example:8772"
export MULTICA_POOL_TOKEN="test-token"
unset MULTICA_CALLER_OWNER MULTICA_RECYCLE_OVERRIDE MULTICA_GC_GUARD_ENABLED 2>/dev/null || true

PASS=0; FAIL=0
fail() { printf 'FAIL: %s\n' "$1" >&2; FAIL=$((FAIL + 1)); }
ok()   { PASS=$((PASS + 1)); }

# --- fake curl: log request, serve canned response by JSON-RPC method -------
cat >"$fake_bin/curl" <<'CUREOF'
#!/usr/bin/env bash
dir="$(dirname "$0")"
log="$dir/../log"; resp="$dir/../resp"
n="$(ls "$log" 2>/dev/null | wc -l)"
n=$((n + 1))
{
  printf 'URL: '
  prev=""
  for a in "$@"; do
    case "$prev" in
      -X|--request|-H|--header) printf '[%s] ' "$a" ;;
      *) case "$a" in -*) ;; *) printf '%s ' "$a" ;; esac ;;
    esac
    prev="$a"
  done
  printf '\n'
} > "$log/req.$n"
body=""
prev=""
for a in "$@"; do
  case "$prev" in
    --data-binary|-d|--data) body="$a" ;;
  esac
  prev="$a"
done
if [ -n "$body" ]; then
  printf '%s\n' "${body#@}" >> "$log/req.$n"
  method="$(printf '%s' "${body#@}" | sed -n 's/.*"method": *"\([a-z/]*\)".*/\1/p')"
  name="$(printf '%s' "${body#@}" | sed -n 's/.*"name": *"\([a-z_]*\)".*/\1/p')"
  key="$method"
  [ -n "$name" ] && key="$name"
  key="${key//\//_}"
  if [ -f "$resp/$key" ]; then
    cat "$resp/$key"
    exit 0
  fi
fi
printf '{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"no canned response for this request"}}'
exit 0
CUREOF
chmod +x "$fake_bin/curl"

resp() { # <key> <json> — canned response for method or tool name
  printf '%s' "$2" >"$resp_dir/$1"
}
tool_result() { # <tool> <payload-json> — wrap a payload as a tool result
  resp "$1" "$(printf '{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":%s}]}}' \
    "$(python3 -c 'import json,sys; print(json.dumps(sys.argv[1]))' "$2")")"
}
reqs() { # list of logged request numbers containing a pattern
  local pat=$1 n f
  for f in "$log_dir"/req.*; do
    [ -f "$f" ] || continue
    if grep -q -- "$pat" "$f"; then printf '%s\n' "${f##*.}"; fi
  done
}
last_method_order() { # print method sequence in request order
  local f out=""
  for f in $(ls "$log_dir"/req.* 2>/dev/null | sort -t. -k2 -n); do
    m="$(sed -n '2p' "$f" | sed -n 's/.*"method": *"\([a-z/]*\)".*/\1/p')"
    t="$(sed -n '2p' "$f" | sed -n 's/.*"name": *"\([a-z_]*\)".*/\1/p')"
    out="$out ${t:-$m}"
  done
  printf '%s\n' "${out# }"
}
# reset only the request log; canned responses persist on purpose — every
# verb re-issues initialize/initialized, so the common fixtures below must
# stay servable across cases. Cases that need a different reply overwrite
# their key via tool_result before running.
reset_log() { rm -f "$log_dir"/req.*; }

# Common canned responses ------------------------------------------------------
resp initialize '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","serverInfo":{"name":"pool","version":"t"}}}'
resp 'notifications_initialized' ''
tool_result acquire_lease '{"lease_id":"ls-1","instance_id":"ri-1","state":"active","expires_at":"2030-01-01T00:00:00Z","template":{"id":"tpl-test-dev"}}'
tool_result get_lease '{"lease_id":"ls-1","instance_id":"ri-1","state":"active","expires_at":"2030-01-01T00:00:00Z","owner":"RUYI-TEST","issue_id":"RUYI-TEST"}'
tool_result release_lease '{"lease_id":"ls-1","state":"released"}'

# --- 1. use (dev): acquire params + manifest ---------------------------------
reset_log
out="$(MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" pdev1 use --phase dev --template tpl-test-dev --ttl 60)"; rc=$?
acq_req="$(reqs acquire_lease | head -1)"
body="$(sed -n '2p' "$log_dir/req.$acq_req")"
if [ "$rc" = 0 ] \
   && grep -q '"template_id": *"tpl-test-dev"' <<<"$body" \
   && grep -q '"owner": *"RUYI-TEST"' <<<"$body" \
   && grep -q '"issue_id": *"RUYI-TEST"' <<<"$body" \
   && grep -q '"ttl_minutes": *60' <<<"$body" \
   && grep -q '"request_id"' <<<"$body"; then ok; else
  fail "use dev: rc=$rc acquire body=$body out=$out"; fi
if [ -f "$MULTICA_SLOT_POOL_HOME/pdev1/lease.env" ] \
   && grep -q '^LEASE_ID=ls-1$' "$MULTICA_SLOT_POOL_HOME/pdev1/lease.env" \
   && grep -q '^PHASE=dev$' "$MULTICA_SLOT_POOL_HOME/pdev1/lease.env"; then ok; else
  fail "use dev manifest missing"; fi

# --- 2. write verb without MULTICA_CALLER_OWNER: refused, no acquire ---------
reset_log
rc=0; out="$(bash "$pool" pdev2 use --phase dev --template tpl-test-dev 2>&1)" || rc=$?
if [ "$rc" != 0 ] && [ -z "$(reqs acquire_lease)" ] \
   && grep -q 'MULTICA_CALLER_OWNER' <<<"$out"; then ok; else
  fail "owner guard: rc=$rc out=$out"; fi

# --- 3. status: get_lease with manifest lease id -----------------------------
reset_log
out="$(bash "$pool" pdev1 status)"; rc=$?
gl="$(reqs get_lease | head -1)"
if [ "$rc" = 0 ] && grep -q '"lease_id": *"ls-1"' "$log_dir/req.$gl"; then ok; else
  fail "status: rc=$rc out=$out"; fi

# --- 4. status auto-renews a near-expiry lease -------------------------------
reset_log
tool_result get_lease '{"lease_id":"ls-1","state":"active","expires_at":"1970-01-01T00:10:00Z","owner":"RUYI-TEST","issue_id":"RUYI-TEST"}'
tool_result renew_lease '{"lease_id":"ls-1","expires_at":"2030-01-01T00:00:00Z"}'
out="$(bash "$pool" pdev1 status)"; rc=$?
if [ "$rc" = 0 ] && [ -n "$(reqs renew_lease)" ]; then ok; else
  fail "auto renew: rc=$rc out=$out"; fi
tool_result get_lease '{"lease_id":"ls-1","state":"active","expires_at":"2030-01-01T00:00:00Z","owner":"RUYI-TEST","issue_id":"RUYI-TEST"}'

# --- 5. exec: wait=true call, exit_code surfaced ------------------------------
reset_log
tool_result exec '{"execution_id":"ex-1","state":"succeeded","exit_code":0}'
out="$(bash "$pool" pdev1 exec -- echo hello)"; rc=$?
ex="$(reqs '"name": *"exec"' | head -1)"
body="$(sed -n '2p' "$log_dir/req.$ex")"
if [ "$rc" = 0 ] && grep -q '"wait": *true' <<<"$body" \
   && grep -q '"lease_id": *"ls-1"' <<<"$body"; then ok; else
  fail "exec: rc=$rc body=$body"; fi

# --- 6. lock-release: guard exec BEFORE release_lease, then manifest cleared --
reset_log
tool_result exec '{"execution_id":"ex-g","state":"succeeded","exit_code":0}'
out="$(MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" pdev1 lock-release)"; rc=$?
order="$(last_method_order)"
guard_pos="$(printf '%s' "$order" | tr ' ' '\n' | grep -n '^exec$' | head -1 | cut -d: -f1)"
rel_pos="$(printf '%s' "$order" | tr ' ' '\n' | grep -n '^release_lease$' | head -1 | cut -d: -f1)"
if [ "$rc" = 0 ] && [ -n "$guard_pos" ] && [ -n "$rel_pos" ] && [ "$guard_pos" -lt "$rel_pos" ]; then ok; else
  fail "release order: rc=$rc order=[$order]"; fi
if [ ! -f "$MULTICA_SLOT_POOL_HOME/pdev1/lease.env" ]; then ok; else fail "release: manifest not cleared"; fi

# --- 7. lock-release blocked: refuse, no release call -------------------------
reset_log
out="$(MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" pdev1 use --phase dev --template tpl-test-dev --ttl 60)" >/dev/null
tool_result exec '{"execution_id":"ex-g2","state":"succeeded","exit_code":1}'
rc=0; out="$(MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" pdev1 lock-release 2>&1)" || rc=$?
if [ "$rc" = 1 ] && [ -z "$(reqs release_lease)" ] && grep -qi 'blocked' <<<"$out" \
   && [ -f "$MULTICA_SLOT_POOL_HOME/pdev1/lease.env" ]; then ok; else
  fail "release blocked: rc=$rc out=$out"; fi

# --- 8. override: guard sees the env, blocked verdict released ----------------
reset_log
tool_result exec '{"execution_id":"ex-g3","state":"succeeded","exit_code":0}'
rc=0; out="$(MULTICA_CALLER_OWNER=RUYI-TEST MULTICA_RECYCLE_OVERRIDE=1 bash "$pool" pdev1 lock-release 2>&1)" || rc=$?
ex="$(reqs '"name": *"exec"' | head -1)"
body="$(sed -n '2p' "$log_dir/req.$ex")"
if [ "$rc" = 0 ] && grep -q 'MULTICA_RECYCLE_OVERRIDE' <<<"$(grep -o '"env": *{[^}]*}' <<<"$body" || true)" \
   && [ -n "$(reqs release_lease)" ]; then ok; else
  fail "override release: rc=$rc body=$body"; fi

# --- 9. fail-closed: guard exec transport failure refuses release -------------
reset_log
out="$(MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" pdev1 use --phase dev --template tpl-test-dev --ttl 60)" >/dev/null
resp exec '{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"provider unreachable"}],"isError":true}}'
rc=0; out="$(MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" pdev1 lock-release 2>&1)" || rc=$?
if [ "$rc" = 1 ] && [ -z "$(reqs release_lease)" ] && grep -qi 'fail-closed\|failed' <<<"$out" \
   && [ -f "$MULTICA_SLOT_POOL_HOME/pdev1/lease.env" ]; then ok; else
  fail "fail-closed: rc=$rc out=$out"; fi

# --- 10. kill switch: guard skipped, straight release --------------------------
reset_log
rc=0; out="$(MULTICA_CALLER_OWNER=RUYI-TEST MULTICA_GC_GUARD_ENABLED=false bash "$pool" pdev1 lock-release 2>&1)" || rc=$?
if [ "$rc" = 0 ] && [ -z "$(reqs '"name": *"exec"')" ] && [ -n "$(reqs release_lease)" ]; then ok; else
  fail "kill switch: rc=$rc out=$out"; fi

# --- 11. handoff --to qa: guarded release + re-acquire with qa template --------
reset_log
tool_result exec '{"execution_id":"ex-g4","state":"succeeded","exit_code":0}'
tool_result acquire_lease '{"lease_id":"ls-2","instance_id":"ri-2","state":"active","expires_at":"2030-01-01T00:00:00Z","template":{"id":"tpl-test-qa"}}'
MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" pdev1 use --phase dev --template tpl-test-dev --ttl 60 >/dev/null
reset_log
tool_result exec '{"execution_id":"ex-g4","state":"succeeded","exit_code":0}'
tool_result acquire_lease '{"lease_id":"ls-2","instance_id":"ri-2","state":"active","expires_at":"2030-01-01T00:00:00Z","template":{"id":"tpl-test-qa"}}'
out="$(MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" pdev1 handoff --to qa --template tpl-test-qa)"; rc=$?
order="$(last_method_order)"
rel_pos="$(printf '%s' "$order" | tr ' ' '\n' | grep -n '^release_lease$' | tail -1 | cut -d: -f1)"
acq_pos="$(printf '%s' "$order" | tr ' ' '\n' | grep -n '^acquire_lease$' | tail -1 | cut -d: -f1)"
if [ "$rc" = 0 ] && [ -n "$rel_pos" ] && [ -n "$acq_pos" ] && [ "$rel_pos" -lt "$acq_pos" ] \
   && grep -q '^PHASE=qa$' "$MULTICA_SLOT_POOL_HOME/pdev1/lease.env" \
   && grep -q '^LEASE_ID=ls-2$' "$MULTICA_SLOT_POOL_HOME/pdev1/lease.env"; then ok; else
  fail "handoff: rc=$rc order=[$order] out=$out"; fi

# --- 12. audit: foreign lease reported -----------------------------------------
reset_log
tool_result list_leases '{"leases":[{"lease_id":"ls-9","owner":"someone-else","issue_id":"RUYI-OTHER","state":"active","template_id":"tpl-x"}]}'
rc=0; out="$(bash "$pool" audit 2>&1)" || rc=$?
if [ "$rc" = 1 ] && grep -q 'someone-else' <<<"$out"; then ok; else
  fail "audit foreign: rc=$rc out=$out"; fi

# --- 13. audit --json on clean ledger ------------------------------------------
reset_log
tool_result list_leases '{"leases":[]}'
out="$(bash "$pool" audit --json)"; rc=$?
if [ "$rc" = 0 ] && [ "$(jq -r .findings <<<"$out")" = "0" ]; then ok; else
  fail "audit clean: rc=$rc out=$out"; fi

# --- 14. orphans: exec inventory in VM, JSON parsed -----------------------------
reset_log
tool_result exec '{"execution_id":"ex-i","state":"succeeded","exit_code":0,"logs":"{\"clean\": true, \"port_residues\": [], \"process_residues\": [], \"git_carriers\": 0}"}'
out="$(bash "$pool" pdev1 orphans)"; rc=$?
if [ "$rc" = 0 ]; then ok; else fail "orphans clean: rc=$rc out=$out"; fi

reset_log
tool_result exec '{"execution_id":"ex-i2","state":"succeeded","exit_code":1,"logs":"{\"clean\": false}"}'
rc=0; out="$(bash "$pool" pdev1 orphans 2>&1)" || rc=$?
if [ "$rc" = 1 ]; then ok; else fail "orphans residue: rc=$rc out=$out"; fi

# --- 15. load: upload_package params pinned -------------------------------------
reset_log
tool_result upload_package '{"package_id":"pkg-1","upload":{"method":"PUT","url":"http://files.example/put-here"}}'
f="$tmp_dir/pkg.zip"; printf 'PK' >"$f"
out="$(MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" pdev1 load --file "$f" --name app.zip)"; rc=$?
up="$(reqs upload_package | head -1)"
body="$(sed -n '2p' "$log_dir/req.$up")"
if [ "$rc" = 0 ] && grep -q '"name": *"app.zip"' <<<"$body" \
   && grep -q '"sha256"' <<<"$body" && grep -q '"size_bytes": *2' <<<"$body" \
   && grep -q '^ARTIFACT_PACKAGE=app.zip$' "$MULTICA_SLOT_POOL_HOME/pdev1/lease.env"; then ok; else
  fail "load: rc=$rc body=$body out=$out manifest=$(cat "$MULTICA_SLOT_POOL_HOME/pdev1/lease.env" 2>/dev/null)"; fi

printf '\nslot-pool.test.sh: %d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" = 0 ]
