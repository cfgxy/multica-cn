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
export SLOT_POOL_BOOTSTRAP_CREDENTIAL="test-bc-ruyi-780.R32v1"
unset MULTICA_CALLER_OWNER MULTICA_RECYCLE_OVERRIDE MULTICA_GC_GUARD_ENABLED 2>/dev/null || true

PASS=0; FAIL=0
fail() { printf 'FAIL: %s\n' "$1" >&2; FAIL=$((FAIL + 1)); }
ok()   { PASS=$((PASS + 1)); }

# --- fake curl: log request, serve canned response by JSON-RPC method -------
# Bare transfers (artifact PUT / artifact GET download, no JSON-RPC body) are
# served raw from resp/<basename-of-url>; their headers still land in the log.
cat >"$fake_bin/curl" <<'CUREOF'
#!/usr/bin/env bash
dir="$(dirname "$0")"
log="$dir/../log"; resp="$dir/../resp"
n="$(ls "$log" 2>/dev/null | wc -l)"
n=$((n + 1))
url=""
{
  printf 'URL: '
  prev=""
  for a in "$@"; do
    case "$a" in http*) url="$a" ;; esac
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
if [ -z "$body" ] && [ -n "$url" ]; then
  key="$(basename "$url")"
  if [ -f "$resp/$key" ]; then
    cat "$resp/$key"
    exit 0
  fi
  printf 'no canned bare response for %s' "$url"
  exit 0
fi
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
  # sort -V: the temp dir name contains dots, so -t. field math is unreliable;
  # version sort orders req.2 before req.10 regardless of the path shape.
  local f out=""
  for f in $(ls "$log_dir"/req.* 2>/dev/null | sort -V); do
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
tool_result acquire_lease '{"lease_id":"ls-1","instance_id":"ri-1","state":"active","expires_at":"2030-01-01T00:00:00Z","template":{"id":"tpl-test-dev"},"execution_capability":"cap-dev-1"}'
tool_result get_lease '{"lease_id":"ls-1","instance_id":"ri-1","state":"active","expires_at":"2030-01-01T00:00:00Z","owner":"RUYI-TEST","issue_id":"RUYI-TEST"}'
tool_result release_lease '{"lease_id":"ls-1","state":"released"}'

# --- 1. use (deprecated dev alias): acquire deploy params + manifest ----------
reset_log
out="$(MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" pdev1 use --phase dev --template tpl-test-dev --ttl 60)"; rc=$?
acq_req="$(reqs acquire_lease | head -1)"
body="$(sed -n '2p' "$log_dir/req.$acq_req")"
if [ "$rc" = 0 ] \
   && grep -q '"template_id": *"tpl-test-dev"' <<<"$body" \
   && grep -q '"owner": *"RUYI-TEST"' <<<"$body" \
   && grep -q '"issue_id": *"RUYI-TEST"' <<<"$body" \
   && grep -q '"ttl_minutes": *60' <<<"$body" \
   && grep -q '"request_id"' <<<"$body" \
   && grep -q '"bootstrap_credential"' <<<"$body"; then ok; else
  fail "use dev: rc=$rc acquire body=$body out=$out"; fi
if grep -q '^CAPABILITY=cap-dev-1$' "$MULTICA_SLOT_POOL_HOME/pdev1/lease.env" \
   && grep -q '^REQUEST_ID=RUYI-TEST:pdev1:deploy$' "$MULTICA_SLOT_POOL_HOME/pdev1/lease.env" \
   && ! grep -q 'test-bc-ruyi-780' "$MULTICA_SLOT_POOL_HOME/pdev1/lease.env"; then ok; else
  fail "use manifest: capability/request recorded, bootstrap credential absent (manifest: $(cat $MULTICA_SLOT_POOL_HOME/pdev1/lease.env 2>/dev/null | head -20))"; fi
if [ -f "$MULTICA_SLOT_POOL_HOME/pdev1/lease.env" ] \
   && grep -q '^LEASE_ID=ls-1$' "$MULTICA_SLOT_POOL_HOME/pdev1/lease.env" \
   && grep -q '^PHASE=deploy$' "$MULTICA_SLOT_POOL_HOME/pdev1/lease.env"; then ok; else
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
tool_result exec '{"execution_id":"ex-g","state":"succeeded","exit_code":0,"log_tail":"verdict: pass"}'
tool_result list_artifacts '{"artifacts":[{"artifact_id":"ar-ev","path":".recycle-evidence/guard-pass.txt","size_bytes":5,"sha256":"2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"}]}'
tool_result download_artifact '{"artifact_id":"ar-ev","content_base64":"aGVsbG8="}'
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
tool_result exec '{"execution_id":"ex-g3","state":"succeeded","exit_code":0,"log_tail":"verdict: pass"}'
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

# --- 11. handoff --to qa: guard, then pool-side transition CAS --------------
reset_log
tool_result exec '{"execution_id":"ex-g4","state":"succeeded","exit_code":0,"log_tail":"verdict: pass"}'
MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" pdev1 use --phase dev --template tpl-test-dev --ttl 60 >/dev/null
reset_log
tool_result exec '{"execution_id":"ex-g4","state":"succeeded","exit_code":0,"log_tail":"verdict: pass"}'
tool_result get_lease '{"lease_id":"ls-1","state":"active","generation":1,"expires_at":"2030-01-01T00:00:00Z"}'
tool_result transition_lease_phase '{"lease":{"lease_id":"ls-1","state":"active","phase":"QA","generation":2},"execution_capability":"cap-qa-1"}'
out="$(MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" pdev1 handoff --to qa)"; rc=$?
tr_req="$(reqs transition_lease_phase | head -1)"
tr_body="$(sed -n '2p' "$log_dir/req.$tr_req")"
if [ "$rc" = 0 ] && [ -n "$tr_req" ] \
   && grep -q '"target_phase": *"QA"' <<<"$tr_body" \
   && grep -q '"expected_generation": *1' <<<"$tr_body" \
   && [ -z "$(reqs release_lease)" ] && [ -z "$(reqs acquire_lease)" ] \
   && grep -q '^PHASE=qa$' "$MULTICA_SLOT_POOL_HOME/pdev1/lease.env" \
   && grep -q '^LEASE_ID=ls-1$' "$MULTICA_SLOT_POOL_HOME/pdev1/lease.env" \
   && grep -q '^CAPABILITY=cap-qa-1$' "$MULTICA_SLOT_POOL_HOME/pdev1/lease.env"; then ok; else
  fail "handoff: rc=$rc body=$tr_body out=$out manifest=$(cat $MULTICA_SLOT_POOL_HOME/pdev1/lease.env 2>/dev/null | tr '\n' ' ')"; fi

# --- 11b. handoff CAS miss: re-read generation, retry once -------------------
reset_log
tool_result get_lease '{"lease_id":"ls-1","state":"active","generation":3,"expires_at":"2030-01-01T00:00:00Z"}'
resp transition_lease_phase '{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"阶段代际冲突：expected=3 current=4"}],"isError":true}}'
rc=0; out="$(MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" pdev1 handoff --to qa 2>&1)" || rc=$?
tr_calls="$(reqs transition_lease_phase | wc -l)"
if [ "$rc" = 1 ] && [ "$tr_calls" = 2 ] && grep -q 'transition' <<<"$out"; then ok; else
  fail "handoff CAS miss: rc=$rc calls=$tr_calls out=$out"; fi

# --- 11c. use without a bootstrap credential: fail-closed, no acquire --------
reset_log
rc=0; out="$(MULTICA_CALLER_OWNER=RUYI-TEST SLOT_POOL_BOOTSTRAP_CREDENTIAL= bash "$pool" pnew1 use --phase deploy 2>&1)" || rc=$?
[ -n "$(cat "$home/.multica/pool-bootstrap/RUYI-TEST" 2>/dev/null)" ] && rm -f "$home/.multica/pool-bootstrap/RUYI-TEST"
if [ "$rc" != 0 ] && [ -z "$(reqs acquire_lease)" ] && grep -q 'bootstrap credential required' <<<"$out"; then ok; else
  fail "use no-credential: rc=$rc out=$out"; fi

# --- 11d. use with pending provisioning: poll get_lease to active -----------
reset_log
tool_result acquire_lease '{"lease_id":"ls-50","instance_id":"ri-50","state":"pending","expires_at":"2030-01-01T00:00:00Z","execution_capability":"cap-p-1"}'
tool_result get_lease '{"lease_id":"ls-50","state":"active","generation":1,"expires_at":"2030-01-01T00:00:00Z","execution_capability":"cap-p-1"}'
out="$(MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" ppoll1 use --phase deploy --version 42a9c2bd84731e20264421730086d95eef0c634c 2>&1)"; rc=$?
gl_poll="$(reqs get_lease | head -1)"
gl_poll_headers=""
[ -n "$gl_poll" ] && gl_poll_headers="$(sed -n '1p' "$log_dir/req.$gl_poll")"
if [ "$rc" = 0 ] && [ -n "$gl_poll" ] \
   && grep -q 'X-Execution-Capability: cap-p-1' <<<"$gl_poll_headers" \
   && grep -q '^LEASE_ID=ls-50$' "$MULTICA_SLOT_POOL_HOME/ppoll1/lease.env" \
   && grep -q '^VERSION_SHA=42a9c2bd84731e20264421730086d95eef0c634c$' "$MULTICA_SLOT_POOL_HOME/ppoll1/lease.env" \
   && grep -q '^CAPABILITY=cap-p-1$' "$MULTICA_SLOT_POOL_HOME/ppoll1/lease.env"; then ok; else
  fail "use pending poll capability: rc=$rc headers=$gl_poll_headers out=$out manifest=$(cat $MULTICA_SLOT_POOL_HOME/ppoll1/lease.env 2>/dev/null | tr '\n' ' ')"; fi

# --- 11d2. use --request-id: out-of-band lease binding replays the same id ---
reset_log
tool_result acquire_lease '{"lease_id":"ls-60","instance_id":"ri-60","state":"active","expires_at":"2030-01-01T00:00:00Z","execution_capability":"cap-b-1"}'
out="$(MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" pbind1 use --phase deploy --request-id RUYI-780-dev-use-3 --version 42a9c2bd84731e20264421730086d95eef0c634c 2>&1)"; rc=$?
bd="$(reqs acquire_lease | head -1)"
body="$(sed -n '2p' "$log_dir/req.$bd")"
if [ "$rc" = 0 ] && grep -q '"request_id": *"RUYI-780-dev-use-3"' <<<"$body" \
   && grep -q '^REQUEST_ID=RUYI-780-dev-use-3$' "$MULTICA_SLOT_POOL_HOME/pbind1/lease.env" \
   && grep -q '^VERSION_SHA=42a9c2bd84731e20264421730086d95eef0c634c$' "$MULTICA_SLOT_POOL_HOME/pbind1/lease.env"; then ok; else
  fail "use --request-id: rc=$rc body=$body out=$out"; fi

reset_log
tool_result acquire_lease '{"lease_id":"ls-50","instance_id":"ri-50","state":"active","expires_at":"2030-01-01T00:00:00Z","execution_capability":"cap-p-2"}'
tool_result get_lease '{"lease_id":"ls-50","state":"active","generation":1,"expires_at":"2030-01-01T00:00:00Z"}'
out="$(MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" ppoll1 refresh 2>&1)"; rc=$?
rf_req="$(reqs acquire_lease | head -1)"
rf_body="$(sed -n '2p' "$log_dir/req.$rf_req")"
rf_headers="$(sed -n '1p' "$log_dir/req.$rf_req")"
rf_get="$(reqs get_lease | head -1)"
rf_get_headers=""
[ -n "$rf_get" ] && rf_get_headers="$(sed -n '1p' "$log_dir/req.$rf_get")"
if [ "$rc" = 0 ] && grep -q '"request_id": *"RUYI-TEST:ppoll1:deploy"' <<<"$rf_body" \
   && ! grep -q 'X-Execution-Capability: cap-p-1' <<<"$rf_headers" \
   && grep -q 'X-Execution-Capability: cap-p-2' <<<"$rf_get_headers" \
   && grep -q '^CAPABILITY=cap-p-2$' "$MULTICA_SLOT_POOL_HOME/ppoll1/lease.env"; then ok; else
  fail "refresh expired capability: rc=$rc acquire_headers=$rf_headers get_headers=$rf_get_headers body=$rf_body out=$out"; fi

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

reset_log
# ssh transport noise (known_hosts notice) ahead of the inventory JSON must
# not flip a clean inventory into rc=1 (P2-1: whole-buffer jq parse).
tool_result exec '{"execution_id":"ex-i3","state":"succeeded","exit_code":0,"log_tail":"Warning: Permanently added '\''192.168.2.130'\'' (ED25519) to the list of known hosts.\n{\"clean\": true, \"port_residues\": [], \"process_residues\": [], \"git_carriers\": 0}"}'
rc=0; out="$(bash "$pool" pdev1 orphans 2>&1)" || rc=$?
if [ "$rc" = 0 ]; then ok; else fail "orphans known_hosts noise: rc=$rc out=$out"; fi

# --- 14b. list: both global and slot-prefixed shapes -----------------------------
reset_log
tool_result list_leases '{"leases":[{"lease_id":"ls-7","owner":"RUYI-TEST","issue_id":"RUYI-TEST","template_id":"tpl-t","state":"active"}]}'
out="$(bash "$pool" list)"; rc=$?
if [ "$rc" = 0 ] && grep -q 'ls-7' <<<"$out"; then ok; else fail "list global: rc=$rc out=$out"; fi

rc=0; out="$(bash "$pool" pdev1 list 2>&1)" || rc=$?
if [ "$rc" = 0 ] && grep -q 'ls-7' <<<"$out"; then ok; else fail "list slot-prefixed: rc=$rc out=$out"; fi

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

# --- 16. pool-auth file fallback: 600 file used when env unset ------------------
reset_log
unset MULTICA_POOL_TOKEN
mkdir -p "$home/.multica"
printf 'file-token-abc\n' >"$home/.multica/pool-auth"
chmod 600 "$home/.multica/pool-auth"
rc=0; out="$(MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" pauth use --phase dev --template tpl-test-dev --ttl 60 2>&1)" || rc=$?
pa_req="$(reqs acquire_lease | head -1)"
pa_hdr=""
[ -n "$pa_req" ] && pa_hdr="$(sed -n '1p' "$log_dir/req.$pa_req")"
if [ "$rc" = 0 ] && grep -q 'Authorization: Bearer file-token-abc' <<<"$pa_hdr"; then ok; else
  fail "pool-auth fallback: rc=$rc hdr=$pa_hdr out=$out"; fi

# --- 17. pool-auth fallback: wrong mode refused before anything is sent ---------
reset_log
chmod 644 "$home/.multica/pool-auth"
rc=0; out="$(MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" pauth2 use --phase dev --template tpl-test-dev --ttl 60 2>&1)" || rc=$?
if [ "$rc" != 0 ] && grep -q 'must be mode 600' <<<"$out" \
   && [ -z "$(reqs initialize)" ]; then ok; else
  fail "pool-auth 644: rc=$rc out=$out reqs=$(reqs initialize | tr '\n' ' ')"; fi
rm -f "$home/.multica/pool-auth"
export MULTICA_POOL_TOKEN="test-token"

# --- 18. gc: only an explicit server-side expired clears the manifest -----------
gc_home="$tmp_dir/gc-home"; mkdir -p "$gc_home"
reset_log
tool_result get_lease '{"lease_id":"ls-1","instance_id":"ri-1","state":"active"}'
MULTICA_SLOT_POOL_HOME="$gc_home" MULTICA_CALLER_OWNER=RUYI-TEST \
  bash "$pool" pgc use --phase dev --template tpl-test-dev --ttl 60 >/dev/null
rc=0; out="$(MULTICA_SLOT_POOL_HOME="$gc_home" MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" gc 2>&1)" || rc=$?
if [ "$rc" = 0 ] && [ -f "$gc_home/pgc/lease.env" ]; then ok; else
  fail "gc keeps active: rc=$rc out=$out"; fi

reset_log
tool_result get_lease '{"lease_id":"ls-1","instance_id":"ri-1","state":"expired"}'
rc=0; out="$(MULTICA_SLOT_POOL_HOME="$gc_home" MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" gc 2>&1)" || rc=$?
if [ "$rc" = 0 ] && [ ! -f "$gc_home/pgc/lease.env" ] \
   && grep -q 'gc cleared manifest' <<<"$out"; then ok; else
  fail "gc clears expired: rc=$rc out=$out"; fi

mkdir -p "$gc_home/pgc2"
printf 'LEASE_ID=ls-1\nISSUE=RUYI-TEST\nPHASE=dev\nTEMPLATE=tpl-test-dev\n' >"$gc_home/pgc2/lease.env"
tool_result get_lease '{"lease_id":"ls-1","instance_id":"ri-1"}'
rc=0; out="$(MULTICA_SLOT_POOL_HOME="$gc_home" MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" gc 2>&1)" || rc=$?
if [ "$rc" = 0 ] && [ -f "$gc_home/pgc2/lease.env" ]; then ok; else
  fail "gc keeps unknown state: rc=$rc out=$out"; fi

# --- guard exec chain (RUYI-681 scope 1) ---------------------------------------
# The guard verdict is parsed from the guest guard's stdout (log_tail), and the
# in-VM evidence file (.recycle-evidence/*.txt, collected via artifact_globs) is
# archived to the local recycle-evidence dir via list_artifacts/download_artifact.
# Negative assertions pin the fail-closed shape: a guard exec that yields no
# parsable verdict, no evidence artifact, a checksum mismatch or a download
# failure must refuse the release (no release_lease call) — removing any of
# those guards flips the matching case red.
export MULTICA_RECYCLE_EVIDENCE_DIR="$home/.multica/recycle-evidence"

ev_fixture() { # <file> <verdict> — a guard-scan-shaped evidence file
  cat >"$1" <<EOF
recycle-evidence v1
timestamp: 2026-10-11T12:00:00Z
tool: slot-pool
target: /home/agent/workspace
kind: slot-release
topology: standalone
dry_run: false
override: false
verdict: $2
predicates:
  predicate: sole_ref cmd="git -C /home/agent/workspace rev-list --count HEAD --not --branches --remotes" exit=0 count=0
  predicate: unpushed cmd="git -C /home/agent/workspace rev-list --count HEAD --not --remotes" exit=0 count=0
  predicate: stash cmd="git -C /home/agent/workspace stash list" exit=0 count=0
  predicate: dirty_tracked cmd="git -C /home/agent/workspace status --porcelain --untracked-files=no" exit=0 count=0
  predicate: untracked cmd="git -C /home/agent/workspace status --porcelain" exit=0 count=0
EOF
}
# canned_guard_ok <exec-id> <verdict> <evfile> — full pass-through guard chain
canned_guard_ok() {
  local xid=$1 verdict=$2 ev=$3 base sha
  base="$(basename "$ev")"; sha="$(sha256sum "$ev" | awk '{print $1}')"
  tool_result exec "{\"execution_id\":\"$xid\",\"state\":\"succeeded\",\"exit_code\":0,\"log_tail\":\"verdict: $verdict\"}"
  tool_result list_artifacts "{\"artifacts\":[{\"artifact_id\":\"ar-$xid\",\"path\":\".recycle-evidence/$base\",\"size_bytes\":$(stat -c %s "$ev"),\"sha256\":\"$sha\"}]}"
  tool_result download_artifact "{\"artifact_id\":\"ar-$xid\",\"content_base64\":\"$(base64 -w0 "$ev")\"}"
}
new_lease() { # <name> — fresh dev lease so each guard case owns its manifest
  tool_result exec '{"execution_id":"ex-env","state":"succeeded","exit_code":0}'
  MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" "$1" use --phase dev --template tpl-test-dev --ttl 60 >/dev/null
}

# --- 19. verdict parsed from guard stdout + evidence archived locally ----------
reset_log
new_lease pdev1
reset_log
rm -rf "$MULTICA_RECYCLE_EVIDENCE_DIR"
ev19="$tmp_dir/guard-ev19.txt"; ev_fixture "$ev19" evidence
canned_guard_ok ex-g5 evidence "$ev19"
rc=0; out="$(MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" pdev1 lock-release 2>&1)" || rc=$?
dl_req="$(reqs download_artifact | head -1)"; dl_body=""
[ -n "$dl_req" ] && dl_body="$(sed -n '2p' "$log_dir/req.$dl_req")"
if [ "$rc" = 0 ] && [ -n "$(reqs release_lease)" ] && grep -q 'evidence' <<<"$out" \
   && grep -q '"artifact_id": *"ar-ex-g5"' <<<"$dl_body" \
   && cmp -s "$MULTICA_RECYCLE_EVIDENCE_DIR/guard-ev19.txt" "$ev19"; then ok; else
  fail "guard chain: rc=$rc out=$out dl=$dl_body archived=$(ls "$MULTICA_RECYCLE_EVIDENCE_DIR" 2>/dev/null)"; fi

# --- 20. unparsable guard output: fail-closed, refuse release ------------------
reset_log
new_lease pdev1
reset_log
tool_result exec '{"execution_id":"ex-g6","state":"succeeded","exit_code":0,"log_tail":"counts: sole_ref=0 unpushed=0 (no verdict line)"}'
rc=0; out="$(MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" pdev1 lock-release 2>&1)" || rc=$?
if [ "$rc" = 1 ] && [ -z "$(reqs release_lease)" ] && grep -qi 'fail-closed' <<<"$out" \
   && [ -f "$MULTICA_SLOT_POOL_HOME/pdev1/lease.env" ]; then ok; else
  fail "verdict parse fail-closed: rc=$rc out=$out"; fi

# --- 20a. exit_code=0 with blocked verdict: fail-closed, refuse release -------
reset_log
new_lease pdev1
reset_log
tool_result exec '{"execution_id":"ex-g6a","state":"succeeded","exit_code":0,"log_tail":"verdict: blocked"}'
rc=0; out="$(MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" pdev1 lock-release 2>&1)" || rc=$?
if [ "$rc" = 1 ] && [ -z "$(reqs release_lease)" ] \
   && [ -z "$(reqs list_artifacts)" ] \
   && grep -qi 'fail-closed' <<<"$out" \
   && [ -f "$MULTICA_SLOT_POOL_HOME/pdev1/lease.env" ]; then ok; else
  fail "exit 0 blocked fail-closed: rc=$rc out=$out"; fi

# --- 20b. unknown non-empty verdict: fail-closed, refuse release --------------
reset_log
new_lease pdev1
reset_log
tool_result exec '{"execution_id":"ex-g6b","state":"succeeded","exit_code":0,"log_tail":"verdict: suspicious"}'
rc=0; out="$(MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" pdev1 lock-release 2>&1)" || rc=$?
if [ "$rc" = 1 ] && [ -z "$(reqs release_lease)" ] \
   && [ -z "$(reqs list_artifacts)" ] \
   && grep -qi 'unknown guard verdict' <<<"$out" \
   && [ -f "$MULTICA_SLOT_POOL_HOME/pdev1/lease.env" ]; then ok; else
  fail "unknown verdict fail-closed: rc=$rc out=$out"; fi

# --- 20c. guest disabled is not the local kill-switch: fail-closed ------------
reset_log
new_lease pdev1
reset_log
tool_result exec '{"execution_id":"ex-g6c","state":"succeeded","exit_code":0,"log_tail":"verdict: disabled"}'
rc=0; out="$(MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" pdev1 lock-release 2>&1)" || rc=$?
if [ "$rc" = 1 ] && [ -z "$(reqs release_lease)" ] \
   && [ -z "$(reqs list_artifacts)" ] \
   && grep -qi 'unexpected disabled' <<<"$out" \
   && [ -f "$MULTICA_SLOT_POOL_HOME/pdev1/lease.env" ]; then ok; else
  fail "unexpected disabled fail-closed: rc=$rc out=$out"; fi

# --- 20d. exit_code=1 with pass verdict is inconsistent and still blocked -----
reset_log
new_lease pdev1
reset_log
tool_result exec '{"execution_id":"ex-g6d","state":"failed","exit_code":1,"log_tail":"verdict: pass"}'
rc=0; out="$(MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" pdev1 lock-release 2>&1)" || rc=$?
if [ "$rc" = 1 ] && [ -z "$(reqs release_lease)" ] \
   && [ -f "$MULTICA_SLOT_POOL_HOME/pdev1/lease.env" ]; then ok; else
  fail "exit 1 pass mismatch fail-closed: rc=$rc out=$out"; fi

# --- 20e. handoff with unknown verdict: retain manifest and lease ------------
reset_log
new_lease pdev1
reset_log
tool_result exec '{"execution_id":"ex-g6e","state":"succeeded","exit_code":0,"log_tail":"verdict: suspicious"}'
rc=0; out="$(MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" pdev1 handoff --to qa --template tpl-test-qa 2>&1)" || rc=$?
if [ "$rc" = 1 ] && [ -z "$(reqs release_lease)" ] \
   && [ -z "$(reqs acquire_lease)" ] \
   && [ -f "$MULTICA_SLOT_POOL_HOME/pdev1/lease.env" ]; then ok; else
  fail "handoff unknown verdict fail-closed: rc=$rc out=$out"; fi

# --- 21. no evidence artifact: fail-closed, refuse release ---------------------
reset_log
new_lease pdev1
reset_log
tool_result exec '{"execution_id":"ex-g7","state":"succeeded","exit_code":0,"log_tail":"verdict: pass"}'
tool_result list_artifacts '{"artifacts":[]}'
rc=0; out="$(MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" pdev1 lock-release 2>&1)" || rc=$?
if [ "$rc" = 1 ] && [ -z "$(reqs release_lease)" ] && grep -qi 'fail-closed\|evidence' <<<"$out" \
   && [ -f "$MULTICA_SLOT_POOL_HOME/pdev1/lease.env" ]; then ok; else
  fail "no-evidence fail-closed: rc=$rc out=$out"; fi

# --- 22. evidence checksum mismatch: fail-closed, refuse release ---------------
reset_log
new_lease pdev1
reset_log
ev22="$tmp_dir/guard-ev22.txt"; ev_fixture "$ev22" pass
tool_result exec '{"execution_id":"ex-g8","state":"succeeded","exit_code":0,"log_tail":"verdict: pass"}'
tool_result list_artifacts '{"artifacts":[{"artifact_id":"ar-bad","path":".recycle-evidence/guard-ev22.txt","size_bytes":300,"sha256":"0000000000000000000000000000000000000000000000000000000000000000"}]}'
tool_result download_artifact "{\"artifact_id\":\"ar-bad\",\"content_base64\":\"$(base64 -w0 "$ev22")\"}"
rc=0; out="$(MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" pdev1 lock-release 2>&1)" || rc=$?
if [ "$rc" = 1 ] && [ -z "$(reqs release_lease)" ] && grep -qi 'sha256\|fail-closed' <<<"$out" \
   && [ -f "$MULTICA_SLOT_POOL_HOME/pdev1/lease.env" ]; then ok; else
  fail "sha mismatch fail-closed: rc=$rc out=$out"; fi

# --- 23. evidence download failure: fail-closed, refuse release ----------------
reset_log
new_lease pdev1
reset_log
tool_result exec '{"execution_id":"ex-g9","state":"succeeded","exit_code":0,"log_tail":"verdict: pass"}'
tool_result list_artifacts '{"artifacts":[{"artifact_id":"ar-gone","path":".recycle-evidence/guard-gone.txt","size_bytes":300,"sha256":"abc"}]}'
resp download_artifact '{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"artifact missing"}],"isError":true}}'
rc=0; out="$(MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" pdev1 lock-release 2>&1)" || rc=$?
if [ "$rc" = 1 ] && [ -z "$(reqs release_lease)" ] && grep -qi 'fail-closed\|archive' <<<"$out" \
   && [ -f "$MULTICA_SLOT_POOL_HOME/pdev1/lease.env" ]; then ok; else
  fail "download fail-closed: rc=$rc out=$out"; fi

# --- 24. large evidence: download_url GET channel, archived with auth ----------
reset_log
new_lease pdev1
reset_log
ev24="$tmp_dir/guard-ev24.txt"; ev_fixture "$ev24" pass
sha24="$(sha256sum "$ev24" | awk '{print $1}')"
tool_result exec '{"execution_id":"ex-ga","state":"succeeded","exit_code":0,"log_tail":"verdict: pass"}'
tool_result list_artifacts "{\"artifacts\":[{\"artifact_id\":\"ar-url\",\"path\":\".recycle-evidence/guard-ev24.txt\",\"size_bytes\":$(stat -c %s "$ev24"),\"sha256\":\"$sha24\"}]}"
tool_result download_artifact '{"artifact_id":"ar-url","download_url":"http://pool.example:8772/files/artifacts/ex-ga/guard-ev24.txt"}'
cp "$ev24" "$resp_dir/guard-ev24.txt"
rc=0; out="$(MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" pdev1 lock-release 2>&1)" || rc=$?
get_req="$(grep -l 'files/artifacts' "$log_dir"/req.* 2>/dev/null | head -1 || true)"
get_hdr=""
[ -n "$get_req" ] && get_hdr="$(sed -n '1p' "$get_req")"
if [ "$rc" = 0 ] && [ -n "$(reqs release_lease)" ] \
   && cmp -s "$MULTICA_RECYCLE_EVIDENCE_DIR/guard-ev24.txt" "$ev24" \
   && grep -q 'Authorization: Bearer test-token' <<<"$get_hdr"; then ok; else
  fail "download_url chain: rc=$rc out=$out hdr=$get_hdr"; fi
rm -f "$resp_dir/guard-ev24.txt"

# --- 25. evidence schema isomorphic to the local track -------------------------
iso="$tmp_dir/iso"; mkdir -p "$iso/repo" "$iso/ev-guest" "$iso/ev-local"
d="$iso/repo"
git -C "$d" init -q -b main; git -C "$d" config user.email t@t; git -C "$d" config user.name t
echo a >"$d/a"
git -C "$d" add a; git -C "$d" commit -qm init
git -C "$d" update-ref refs/remotes/origin/main HEAD
MULTICA_RECYCLE_EVIDENCE_DIR="$iso/ev-guest" RG_TOOL=iso-guest \
  bash "$root_dir/scripts/slot-image/guest/guard-scan.sh" "$d" slot-release 0 >/dev/null
(
  export MULTICA_RECYCLE_EVIDENCE_DIR="$iso/ev-local"
  . "$root_dir/scripts/lib-recycle-guard.sh"
  RG_TOOL=iso-local
  recycle_guard_scan_worktree "$d" slot-release 0 || true
)
norm_ev() { sed -e 's/^timestamp: .*/timestamp: X/' -e 's/^tool: .*/tool: X/' -e 's/^target: .*/target: X/' "$1"; }
vm_ev="$(ls "$iso/ev-guest"/*.txt 2>/dev/null | head -1)"
lo_ev="$(ls "$iso/ev-local"/*.txt 2>/dev/null | head -1)"
if [ -n "$vm_ev" ] && [ -n "$lo_ev" ] \
   && diff <(norm_ev "$vm_ev") <(norm_ev "$lo_ev") >/dev/null; then ok; else
  fail "schema isomorphism: vm=$vm_ev local=$lo_ev
--- guest ---
$(cat "${vm_ev:-/dev/null}")
--- local ---
$(cat "${lo_ev:-/dev/null}")"; fi

# --- 26. lock-release --dry-run: guard + archive run, lease untouched ----------
reset_log
new_lease pdev1
reset_log
ev26="$tmp_dir/guard-ev26.txt"; ev_fixture "$ev26" pass
canned_guard_ok ex-gd pass "$ev26"
rc=0; out="$(MULTICA_CALLER_OWNER=RUYI-TEST bash "$pool" pdev1 lock-release --dry-run 2>&1)" || rc=$?
ex_req="$(reqs '"name": *"exec"' | head -1)"; ex_body=""
[ -n "$ex_req" ] && ex_body="$(sed -n '2p' "$log_dir/req.$ex_req")"
if [ "$rc" = 0 ] && [ -z "$(reqs release_lease)" ] \
   && [ -f "$MULTICA_SLOT_POOL_HOME/pdev1/lease.env" ] \
   && grep -q 'dry' <<<"$out" && grep -q '"slot-release","1"' <<<"$ex_body"; then ok; else
  fail "dry-run: rc=$rc out=$out ex=$ex_body"; fi

# --- 27. exec --cwd: workspace-relative cwd pinned in the call -----------------
reset_log
tool_result exec '{"execution_id":"ex-c","state":"succeeded","exit_code":0}'
out="$(bash "$pool" pdev1 exec --cwd sub/dir -- echo hi 2>&1)" || rc=$?
ex="$(reqs '"name": *"exec"' | head -1)"; body=""
[ -n "$ex" ] && body="$(sed -n '2p' "$log_dir/req.$ex")"
if [ "$rc" = 0 ] && grep -q '"cwd": *"sub/dir"' <<<"$body"; then ok; else
  fail "exec --cwd: rc=$rc body=$body"; fi

printf '\nslot-pool.test.sh: %d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" = 0 ]
