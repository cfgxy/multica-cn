#!/usr/bin/env bash
# QA preflight dependency checklist for Playwright E2E (RUYI-632, acceptance 3).
#
# QA round 4 (2026-10-10) found two unchecked dependencies polluting symptom
# attribution: the slot's REDIS_URL pointed at a port with no listener (API
# silently fell back to the DB) and the effective value of the
# composio_mcp_apps feature flag was never verified (the Agent MCP tab cases
# depend on it). This script turns those into an explicit, paste-able
# checklist that must run BEFORE the suite:
#
#   bash scripts/e2e-preflight.sh dev1
#
# Read-only: HTTP/TCP probes only, no config changes, no writes. Exit codes:
#   0  no FAIL (WARNs possible — read the output and note them in the report)
#   1  at least one FAIL (api/web unreachable — the suite cannot start)
#
# Every WARN names its attribution consequence so the choice to proceed is a
# recorded QA decision, not a silent default.
set -euo pipefail

usage() {
  printf 'usage: scripts/e2e-preflight.sh <slot>\n       slot: dev1 | dev2\n' >&2
  exit 2
}

SLOT="${1:-}"
[ -n "$SLOT" ] || usage

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SLOT_HOME="${MULTICA_SLOTS_HOME:-$HOME/.multica/slots}"
SLOT_MANIFEST="$SLOT_HOME/$SLOT/manifest.env"

fail() { printf 'e2e-preflight: %s\n' "$*" >&2; exit 1; }

[ -f "$SLOT_MANIFEST" ] || fail "no manifest at $SLOT_MANIFEST — is the slot bound and up? (scripts/dev-env.sh $SLOT use)"

manifest_field() { grep -E "^${1}=" "$SLOT_MANIFEST" | head -1 | cut -d= -f2-; }
env_field() { grep -E "^${1}=" "$ENV_FILE" | head -1 | cut -d= -f2-; }

ENV_FILE="$(manifest_field ENV_FILE)"
[ -n "$ENV_FILE" ] && [ -f "$ENV_FILE" ] || fail "slot env file missing: '$ENV_FILE'"

API_PORT="$(manifest_field BACKEND_PORT)"
WEB_PORT="$(manifest_field FRONTEND_PORT)"
CODE_SHA="$(manifest_field CODE_SHA)"
[ -n "$API_PORT" ] && [ -n "$WEB_PORT" ] || fail "manifest missing BACKEND_PORT/FRONTEND_PORT"

RESULTS=()
record() { # $1 = PASS|WARN|FAIL|INFO, $2 = check name, $3 = detail
  RESULTS+=("$1|$2|$3")
  case "$1" in
    PASS) printf '[PASS] %s: %s\n' "$2" "$3" ;;
    WARN) printf '[WARN] %s: %s\n' "$2" "$3" ;;
    FAIL) printf '[FAIL] %s: %s\n' "$2" "$3" ;;
    INFO) printf '[INFO] %s: %s\n' "$2" "$3" ;;
  esac
}

tcp_open() { (exec 3<>"/dev/tcp/$1/$2") 2>/dev/null; }

# 1. API health — must answer and identify its build.
api_health="$(curl -sf --max-time 5 "http://localhost:${API_PORT}/health" 2>/dev/null || true)"
if [ -n "$api_health" ]; then
  api_commit="$(printf '%s' "$api_health" | node -e 'let d="";process.stdin.on("data",c=>d+=c).on("end",()=>{try{console.log(JSON.parse(d).commit??"unknown")}catch{console.log("unknown")}})' 2>/dev/null || echo unknown)"
  record PASS "api health" ":$API_PORT 200, commit $api_commit (slot-bound SHA $CODE_SHA)"
  [ "$api_commit" = "$CODE_SHA" ] || record WARN "api commit" "serving $api_commit but slot is bound to $CODE_SHA — rebuild/rebind before judging symptoms against the target SHA"
else
  record FAIL "api health" "http://localhost:$API_PORT/health unreachable — start the slot: MULTICA_CALLER_OWNER=<issue> scripts/dev-env.sh $SLOT up --components api"
fi

# 2. Web entry — must answer through the port the suite will drive.
web_status="$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 "http://localhost:${WEB_PORT}" 2>/dev/null || true)"
if [ "$web_status" = "200" ] || [ "$web_status" = "307" ] || [ "$web_status" = "302" ]; then
  record PASS "web entry" ":$WEB_PORT HTTP $web_status"
elif [ "$web_status" = "000" ] || [ -z "$web_status" ]; then
  record FAIL "web entry" ":$WEB_PORT no answer — start the slot: MULTICA_CALLER_OWNER=<issue> scripts/dev-env.sh $SLOT up --components web"
else
  record FAIL "web entry" ":$WEB_PORT answered HTTP $web_status — inspect: scripts/dev-env.sh $SLOT logs web"
fi

# 3. Release-entry proof — when the manifest declares a release build, the
#    served HTML must actually come from it (QA round 4's core gap). Build
#    artifacts live in the slot worktree (manifest DIR), same place
#    e2e-release-entry.sh writes them.
MANIFEST_JSON="$(manifest_field DIR)/e2e-artifacts/manifest.json"
if [ -f "$MANIFEST_JSON" ]; then
  if bash "$SCRIPT_DIR/e2e-release-entry.sh" "$SLOT" verify >/tmp/e2e-preflight-verify.$$ 2>&1; then
    record PASS "release entry" "artifact digest matches manifest ($(node -e 'console.log(JSON.parse(require("fs").readFileSync(process.argv[1],"utf8")).artifact.sha256.slice(0,16))' "$MANIFEST_JSON")…)"
    record INFO "release manifest" "git $(node -e 'console.log(JSON.parse(require("fs").readFileSync(process.argv[1],"utf8")).source.git_sha)' "$MANIFEST_JSON") built $(node -e 'console.log(JSON.parse(require("fs").readFileSync(process.argv[1],"utf8")).build.built_at)' "$MANIFEST_JSON")"
  else
    record WARN "release entry" "artifact digest verification failed: $(tail -3 /tmp/e2e-preflight-verify.$$ | tr '\n' ' ')"
  fi
  rm -f /tmp/e2e-preflight-verify.$$
else
  record WARN "release entry" "no e2e-artifacts manifest in the slot worktree — web is a dev server; E2E results have no artifact traceability (RUYI-632). Build via scripts/e2e-release-entry.sh $SLOT build and restart web with MULTICA_WEB_MODE=release"
fi

# 4. Redis — the API treats an unreachable REDIS_URL as "fall back to the DB",
#    which changes realtime fanout behavior and pollutes symptom attribution.
REDIS_URL="$(env_field REDIS_URL || true)"
if [ -n "$REDIS_URL" ]; then
  redis_host="$(printf '%s' "$REDIS_URL" | sed -E 's#^[a-z]+://##; s#[:/].*$##')"
  redis_port="$(printf '%s' "$REDIS_URL" | sed -nE 's#^[a-z]+://[^/]*:([0-9]+).*#\1#p')"
  redis_port="${redis_port:-6379}"
  if tcp_open "$redis_host" "$redis_port"; then
    record PASS "redis" "$redis_host:$redis_port reachable ($REDIS_URL)"
  else
    record WARN "redis" "REDIS_URL=$REDIS_URL unreachable — API will log a DB fallback; note it in the report (does not block the suite, but realtime-path symptoms cannot be attributed cleanly)"
  fi
else
  record PASS "redis" "REDIS_URL unset — API runs single-node in-memory mode (expected for slots)"
fi

# 5. Database — TestApiClient and the API both need the slot Postgres.
DATABASE_URL="$(env_field DATABASE_URL || true)"
db_host="$(printf '%s' "$DATABASE_URL" | sed -nE 's#^postgres://[^@]*@([^:/]+).*#\1#p')"
db_port="$(printf '%s' "$DATABASE_URL" | sed -nE 's#^postgres://[^@]*@[^:/]+:([0-9]+).*#\1#p')"
if [ -n "$db_host" ] && [ -n "$db_port" ] && tcp_open "$db_host" "$db_port"; then
  record PASS "postgres" "$db_host:$db_port reachable"
else
  record FAIL "postgres" "DATABASE_URL endpoint unreachable — E2E login/seed paths cannot work"
fi

# 6. Feature flags — /api/config through the web entry (exercises the runtime
#    rewrite too). composio_mcp_apps gates the Agent MCP tab cases.
flags_json="$(curl -sf --max-time 10 "http://localhost:${WEB_PORT}/api/config" 2>/dev/null | node -e 'let d="";process.stdin.on("data",c=>d+=c).on("end",()=>{try{const f=JSON.parse(d).feature_flags||{};console.log(JSON.stringify(f))}catch{console.log("")}})' 2>/dev/null || true)"
if [ -n "$flags_json" ]; then
  record PASS "feature flags (via web /api/config)" "$flags_json"
  composio="$(printf '%s' "$flags_json" | node -e 'let d="";process.stdin.on("data",c=>d+=c).on("end",()=>{const f=JSON.parse(d);console.log(f["composio_mcp_apps"]===true?"true":"false")})' 2>/dev/null || echo false)"
  [ "$composio" = "true" ] || record WARN "composio_mcp_apps" "flag is off — e2e/agent-mcp.spec.ts 'creator sees the MCP Apps tab' cases WILL fail by configuration (enable FF_COMPOSIO_MCP_APPS=true for the api process, or accept and attribute those failures to fixture config)"
else
  record WARN "feature flags" "GET :$WEB_PORT/api/config returned no feature_flags — check the web entry proxy"
fi

# 7. Upload route reachability — POST /api/upload-file unauthenticated must
#    reach the Go layer: auth answers 401 (or the handler 400s on the empty
#    body). The UploadFile handler itself can never answer 404, so a 404 here
#    means the request died at the web rewrite or a wrong API base — exactly
#    how the chat-attachments cases were lost in QA round 4.
for upload_label in "direct api:$API_PORT" "web proxy:$WEB_PORT"; do
  upload_name="${upload_label%%:*}"; upload_port="${upload_label##*:}"
  upload_code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 -X POST "http://localhost:${upload_port}/api/upload-file" 2>/dev/null || true)"
  case "$upload_code" in
    401|400|403)
      record PASS "upload route ($upload_name)" "POST :$upload_port/api/upload-file → $upload_code unauthenticated (route reachable)" ;;
    404)
      if [ "$upload_name" = "web proxy" ]; then
        record FAIL "upload route ($upload_name)" "POST :$upload_port/api/upload-file → 404 — the web runtime rewrite dropped the path; check REMOTE_API_URL/NEXT_PUBLIC_API_URL in the web process env"
      else
        record FAIL "upload route ($upload_name)" "POST :$upload_port/api/upload-file → 404 — the Go router never 404s this path; the api binary predates the route or the port maps elsewhere"
      fi ;;
    *)
      record WARN "upload route ($upload_name)" "POST :$upload_port/api/upload-file → ${upload_code:-no answer} (expected 401/400 unauth); note it before the run" ;;
  esac
done

# 8. Port ownership — both ports must have a listener (belonging to this slot).
for port_label in "api:$API_PORT" "web:$WEB_PORT"; do
  name="${port_label%%:*}"; port="${port_label##*:}"
  listener="$(ss -ltnpH "sport = :$port" 2>/dev/null | head -1 | sed -nE 's/.*pid=([0-9]+).*/\1/p' || true)"
  if [ -n "$listener" ]; then
    record PASS "port $name" ":$port listening (pid $listener)"
  else
    record WARN "port $name" ":$port has no listener — component down or not owned by this slot"
  fi
done

# Summary for the QA report.
printf '\n--- preflight summary ---\n'
fail_count=0; warn_count=0
for line in "${RESULTS[@]}"; do
  status="${line%%|*}"; rest="${line#*|}"
  case "$status" in
    FAIL) printf '[FAIL] %s: %s\n' "${rest%%|*}" "${rest#*|}"; fail_count=$((fail_count+1)) ;;
    WARN) printf '[WARN] %s: %s\n' "${rest%%|*}" "${rest#*|}"; warn_count=$((warn_count+1)) ;;
    INFO) printf '[INFO] %s: %s\n' "${rest%%|*}" "${rest#*|}" ;;
  esac
done
printf '%d FAIL, %d WARN — %s\n' "$fail_count" "$warn_count" "$([ "$fail_count" -eq 0 ] && echo "suite may start; carry WARNs into the QA report" || echo "fix FAILs before starting the suite")"
[ "$fail_count" -eq 0 ]
