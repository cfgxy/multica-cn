#!/usr/bin/env bash
# slot-pool.sh — pool backend of the multica slot tool layer (RUYI-676 E3,
# skeleton). Maps the local slot lease semantics onto the VM resource pool
# MCP (docs/03 contract): use→acquire_lease, load→upload_package(+prepare),
# exec→exec, logs→get_exec_logs, lock-release→guard pre-hook + release_lease,
# handoff→guarded release + re-acquire, orphans→in-VM inventory, audit→pool
# ledger sweep, gc→expired-lease sweep. The local slot track
# (scripts/dev-env.sh) is untouched; P2 wires backend selection.
#
# Semantic invariants kept identical to the local track:
#   - Write verbs require MULTICA_CALLER_OWNER=<issue-id> (single writer =
#     one issue per lease; audit fields owner/issue_id ride every acquire).
#   - Release/handoff/gc run the RUYI-594 guard INSIDE the VM first
#     (fail-closed: transport failure = blocked), honour the same one-shot
#     MULTICA_RECYCLE_OVERRIDE and the MULTICA_GC_GUARD_ENABLED kill switch,
#     and file evidence under ~/.multica/recycle-evidence (same v1 format
#     written by scripts/slot-image/guest/guard-scan.sh).
#   - dev phase = wide TTL + activity renew; qa phase = tighter TTL
#     (scripts/slot-pool.json; E1 makes them per-template server-side).
#
# Credentials: MULTICA_POOL_URL (default http://192.168.2.130:8772) and
# MULTICA_POOL_TOKEN (Bearer; also read from $HOME/.multica/pool-auth, never
# committed). Lease manifests live under MULTICA_SLOT_POOL_HOME
# (default ~/.multica/slot-pool/<name>/lease.env, mode 600).
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
POOL_URL="${MULTICA_POOL_URL:-http://192.168.2.130:8772}"
POOL_TOKEN="${MULTICA_POOL_TOKEN:-}"
POOL_HOME="${MULTICA_SLOT_POOL_HOME:-$HOME/.multica/slot-pool}"
POOL_CONFIG="${SLOT_POOL_CONFIG:-$SCRIPT_DIR/slot-pool.json}"
EVIDENCE_DIR="${MULTICA_RECYCLE_EVIDENCE_DIR:-$HOME/.multica/recycle-evidence}"
GUEST_GUARD="/usr/local/lib/multica/guard-scan.sh"
GUEST_INVENTORY="/usr/local/lib/multica/slot-inventory.sh"
GUEST_ENV_REMOTE="/home/agent/.multica-slot.env"
WORKSPACE_ROOT="/home/agent/workspace"
RENEW_AHEAD="${SLOT_POOL_RENEW_AHEAD_SECONDS:-1800}"
HTTP_TIMEOUT="${SLOT_POOL_HTTP_TIMEOUT:-120}"

die() { printf 'slot-pool: %s\n' "$1" >&2; exit 1; }
caller_owner() { printf '%s' "${MULTICA_CALLER_OWNER:-}"; }
require_owner() {
  [ -n "$(caller_owner)" ] || die "MULTICA_CALLER_OWNER is not set. Write commands must name the owning issue, e.g. MULTICA_CALLER_OWNER=RUYI-680 scripts/slot-pool.sh $POOL_SLOT use."
}

json_quote() { jq -rn --arg s "$1" '$s'; }

RPC_ID=0
# _rpc <method> <params-json> — JSON-RPC over streamable HTTP. Prints the
# result object (or notification ack). Bodies are jq -cn (single line): the
# request log and assertion tooling key on line-oriented bodies. Request id
# increments per call; notifications carry no id.
_rpc() {
  local method=$1 params=${2:-null} body out
  command -v curl >/dev/null || die "curl is required"
  command -v jq >/dev/null || die "jq is required"
  if [[ "$method" == notifications/* ]]; then
    body="$(jq -cn --arg m "$method" --argjson p "$params" '{jsonrpc:"2.0",method:$m,params:$p}')"
  else
    RPC_ID=$((RPC_ID + 1))
    body="$(jq -cn --arg m "$method" --argjson p "$params" --argjson i "$RPC_ID" \
      '{jsonrpc:"2.0",id:$i,method:$m,params:$p}')"
  fi
  local auth=()
  [ -n "$POOL_TOKEN" ] && auth=(-H "Authorization: Bearer $POOL_TOKEN")
  out="$(curl -sS --max-time "$HTTP_TIMEOUT" -X POST "$POOL_URL/mcp" \
    -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
    "${auth[@]+"${auth[@]}"}" --data-binary "$body" 2>&1)" ||
    die "pool RPC $method transport failed: $out"
  if jq -e '.error' >/dev/null 2>&1 <<<"$out"; then
    die "pool RPC $method error: $(jq -r '.error.message // .error' <<<"$out")"
  fi
  case "$method" in
    notifications/*) printf '%s' "$out" ;;
    *) jq -c '.result // empty' <<<"$out" ;;
  esac
}

# _pool_handshake — initialize + initialized notification (once per run).
_pool_handshake() {
  [ -n "${POOL_HANDSHAKE_DONE:-}" ] && return 0
  _rpc initialize '{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"slot-pool","version":"0.1"}}' >/dev/null
  _rpc notifications/initialized '{}' >/dev/null
  POOL_HANDSHAKE_DONE=1
}

# _tool <tool> <params-json> — tools/call; prints the payload (text parsed as
# JSON when it parses). Tool-level business errors (isError) fail the caller.
_tool() {
  local tool=$1
  local params="${2:-}"
  [ -n "$params" ] || params='{}'
  _pool_handshake
  local result text
  result="$(_rpc tools/call "$(jq -n --arg n "$tool" --argjson a "$params" '{name:$n,arguments:$a}')")"
  [ -n "$result" ] || die "pool tool $tool returned no result"
  if jq -e '.isError == true' >/dev/null 2>&1 <<<"$result"; then
    die "pool tool $tool refused: $(jq -r '.content[0].text // "unknown"' <<<"$result")"
  fi
  text="$(jq -r '.content[0].text // ""' <<<"$result")"
  if jq -e . >/dev/null 2>&1 <<<"$text"; then
    jq -c . <<<"$text"
  else
    printf '%s' "$text"
  fi
}

manifest_path() { printf '%s/%s/lease.env' "$POOL_HOME" "$POOL_SLOT"; }
manifest_field() { sed -n "s/^$1=//p" "$(manifest_path)" | head -1; }
write_manifest() { # <lease_id> <instance_id> <template> <phase> <expires_at>
  local dir
  dir="$(dirname "$(manifest_path)")"
  mkdir -p "$dir"
  {
    printf 'NAME=%s\n' "$POOL_SLOT"
    printf 'LEASE_ID=%s\n' "$1"
    printf 'INSTANCE_ID=%s\n' "$2"
    printf 'TEMPLATE=%s\n' "$3"
    printf 'PHASE=%s\n' "$4"
    printf 'ISSUE=%s\n' "$(caller_owner)"
    printf 'ACQUIRED_AT=%s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
    printf 'EXPIRES_AT=%s\n' "$5"
    printf 'WORKSPACE_ROOT=%s\n' "$WORKSPACE_ROOT"
    printf 'GUEST_ENV=%s\n' "$GUEST_ENV_REMOTE"
  } > "$(manifest_path)"
  chmod 600 "$(manifest_path)"
}
load_manifest() {
  [ -f "$(manifest_path)" ] || die "no lease manifest for $POOL_SLOT (use first)"
  # shellcheck disable=SC1090
  . "$(manifest_path)"
  [ "${LEASE_ID:-}" ] || die "manifest for $POOL_SLOT has no LEASE_ID"
}

config_phase_field() { # <phase> <field>
  jq -r --arg ph "$1" --arg f "$2" '.[$ph][$f] // empty' "$POOL_CONFIG" 2>/dev/null
}

epoch_of() { date -d "$1" '+%s' 2>/dev/null || printf 0; }

# _maybe_renew <expires_at> — activity renewal (dev 长租约 + 活跃续约):
# renew when the server-reported expiry is inside the ahead-of-expiry window.
# Falls back to the manifest value when the caller has no fresh payload.
_maybe_renew() {
  local exp="${1:-$(manifest_field EXPIRES_AT)}" now left
  [ -n "$exp" ] || return 0
  now="$(date -u '+%s')"
  left=$(( $(epoch_of "$exp") - now ))
  if [ "$left" -lt "$RENEW_AHEAD" ]; then
    local out new_exp
    out="$(_tool renew_lease "$(jq -n --arg l "$LEASE_ID" '{lease_id:$l}')" )" || return 0
    new_exp="$(jq -r '.expires_at // empty' <<<"$out" 2>/dev/null)"
    if [ -n "$new_exp" ]; then
      sed -i "s/^EXPIRES_AT=.*/EXPIRES_AT=$new_exp/" "$(manifest_path)"
      printf 'slot-pool: renewed %s until %s\n' "$LEASE_ID" "$new_exp" >&2
    fi
  fi
}

# _write_guest_env — project the registered facts (ports/workspace) into the
# VM so guard/inventory scripts read one env file, same contract as local
# slots. Best effort: acquisition must succeed even while the VM is still
# settling; a failed env write degrades orphans reporting, not the lease.
_write_guest_env() {
  local id=$1
  local api_port web_port desktop_port mcp_port payload
  api_port="$(jq -r '.guest_ports.api // empty' "$POOL_CONFIG" 2>/dev/null)"; api_port="${api_port:-21801}"
  web_port="$(jq -r '.guest_ports.web // empty' "$POOL_CONFIG" 2>/dev/null)"; web_port="${web_port:-13801}"
  desktop_port="$(jq -r '.guest_ports.desktop // empty' "$POOL_CONFIG" 2>/dev/null)"; desktop_port="${desktop_port:-57801}"
  mcp_port="$(jq -r '.guest_ports.mcp // empty' "$POOL_CONFIG" 2>/dev/null)"; mcp_port="${mcp_port:-13001}"
  payload="$(printf 'SLOT_NAME=%s\nAPI_PORT=%s\nWEB_PORT=%s\nDESKTOP_PORT=%s\nMCP_PORT=%s\nWORKSPACE_ROOT=%s\n' \
    "$POOL_SLOT" "$api_port" "$web_port" "$desktop_port" "$mcp_port" "$WORKSPACE_ROOT" | base64 -w0)"
  _tool exec "$(jq -n --arg l "$id" --arg p "$payload" \
    '{lease_id:$l,command:"bash",
      args:["-c","printf %s \"$0\" | base64 -d > /home/agent/.multica-slot.env",$p],
      cwd:"/"}')" >/dev/null
}

# _guard_prehook <kind> — run the RUYI-594 guard inside the VM. Prints the
# verdict line; returns 0 when the caller may proceed (pass/evidence/disabled
# or a one-shot override), 1 when blocked, 2 when skipped (kill switch).
_guard_prehook() {
  local kind=$1 out exit_code verdict
  case "${MULTICA_GC_GUARD_ENABLED:-true}" in
    false|FALSE|0|off|OFF) printf 'verdict: disabled\n'; return 2 ;;
  esac
  local env_json='{"MULTICA_RECYCLE_EVIDENCE_DIR":"/home/agent/.multica/recycle-evidence","RG_TOOL":"slot-pool"}'
  if [ "${MULTICA_RECYCLE_OVERRIDE:-}" = "1" ]; then
    env_json='{"MULTICA_RECYCLE_EVIDENCE_DIR":"/home/agent/.multica/recycle-evidence","RG_TOOL":"slot-pool","MULTICA_RECYCLE_OVERRIDE":"1"}'
  fi
  out="$(_tool exec "$(jq -n \
      --arg l "${LEASE_ID:?}" --arg g "$GUEST_GUARD" --arg w "$WORKSPACE_ROOT" --arg k "$kind" --argjson e "$env_json" \
      '{lease_id:$l,command:$g,args:[$w,$k,"0"],env:$e,cwd:"/",wait:true,timeout_seconds:600,
       artifact_globs:["/home/agent/.multica/recycle-evidence/*.txt"]}')")" || {
    printf 'verdict: blocked (fail-closed: guard exec failed)\n'
    return 1
  }
  exit_code="$(jq -r '.exit_code // empty' <<<"$out" 2>/dev/null)"
  verdict="$(jq -r '.verdict // empty' <<<"$out" 2>/dev/null)"
  case "${exit_code:-}" in
    0) printf 'verdict: %s\n' "${verdict:-pass}"; return 0 ;;
    1) printf 'verdict: %s\n' "${verdict:-blocked}"
       printf '%s\n' "$(jq -r '.log_tail // .logs // ""' <<<"$out" 2>/dev/null)"
       return 1 ;;
    *) printf 'verdict: blocked (fail-closed: guard exit_code=%s state=%s)\n' \
         "${exit_code:-none}" "$(jq -r '.state // "unknown"' <<<"$out" 2>/dev/null)"
       return 1 ;;
  esac
}

_do_release() { # <reason>
  _tool release_lease "$(jq -n --arg l "$LEASE_ID" '{lease_id:$l}')" >/dev/null ||
    die "release_lease failed for $LEASE_ID"
  rm -f "$(manifest_path)"
  printf 'slot-pool: %s released %s\n' "$1" "$LEASE_ID"
}

# --- verbs -------------------------------------------------------------------

cmd_use() {
  local phase=dev template="" ttl=""
  while [ $# -gt 0 ]; do
    case "$1" in
      --phase) phase=$2; shift 2 ;;
      --template) template=$2; shift 2 ;;
      --ttl) ttl=$2; shift 2 ;;
      *) die "use: unknown flag $1" ;;
    esac
  done
  case "$phase" in dev|qa) ;; *) die "use: phase must be dev or qa" ;; esac
  require_owner
  template="${template:-$(config_phase_field "$phase" template)}"
  ttl="${ttl:-$(config_phase_field "$phase" ttl_minutes)}"
  [ -n "$template" ] || die "use: no template for phase $phase (scripts/slot-pool.json)"
  local out lease_id instance_id expires_at request_id
  request_id="$(caller_owner):$POOL_SLOT:$phase"
  out="$(_tool acquire_lease "$(jq -n \
      --arg t "$template" --arg o "$(caller_owner)" --arg i "$(caller_owner)" \
      --argjson ttl "${ttl:-60}" --arg r "$request_id" \
      '{template_id:$t,owner:$o,issue_id:$i,ttl_minutes:$ttl,request_id:$r}')")"
  lease_id="$(jq -r '.lease_id // empty' <<<"$out")"
  instance_id="$(jq -r '.instance_id // empty' <<<"$out")"
  expires_at="$(jq -r '.expires_at // empty' <<<"$out")"
  [ -n "$lease_id" ] || die "use: acquire returned no lease id"
  write_manifest "$lease_id" "$instance_id" "$template" "$phase" "$expires_at"
  # Subshell: a die inside (VM still settling) must not lose the lease.
  ( _write_guest_env "$lease_id" ) || printf 'slot-pool: warning: guest env write failed (orphans inventory will report it)\n' >&2
  printf 'slot-pool: %s acquired %s (%s/%s, until %s)\n' "$POOL_SLOT" "$lease_id" "$template" "$phase" "$expires_at"
}

cmd_status() {
  local as_json=0
  [ "${1:-}" = "--json" ] && as_json=1
  load_manifest
  local out
  out="$(_tool get_lease "$(jq -n --arg l "$LEASE_ID" '{lease_id:$l}')")"
  _maybe_renew "$(jq -r '.expires_at // empty' <<<"$out" 2>/dev/null)"
  if [ "$as_json" = 1 ]; then
    printf '%s\n' "$out"
  else
    printf 'slot-pool: %s %s (%s/%s, issue %s, until %s)\n' \
      "$POOL_SLOT" "$LEASE_ID" "${TEMPLATE:-?}" "${PHASE:-?}" "${ISSUE:-?}" \
      "$(jq -r '.expires_at // "unknown"' <<<"$out")"
  fi
}

cmd_exec() {
  [ "${1:-}" = "--" ] || die "exec: usage: slot-pool.sh <name> exec -- <command> [args...]"
  shift
  local cmd=$1; shift
  load_manifest
  _maybe_renew
  local args_json="[]"
  if [ "$#" -gt 0 ]; then
    args_json="$(printf '%s\n' "$@" | jq -Rsc 'split("\n")[:-1]')"
  fi
  local out exit_code
  out="$(_tool exec "$(jq -n --arg l "$LEASE_ID" --arg c "$cmd" --argjson a "$args_json" \
      '{lease_id:$l,command:$c,args:$a,wait:true}')")" ||
    die "exec failed"
  exit_code="$(jq -r '.exit_code // empty' <<<"$out" 2>/dev/null)"
  jq -r '.log_tail // .logs // empty' <<<"$out" 2>/dev/null
  [ "${exit_code:-1}" = "0" ] || return 1
}

cmd_logs() {
  local exec_id=${1:-} lines=${2:-200}
  [ -n "$exec_id" ] || die "logs: usage: slot-pool.sh <name> logs <execution_id> [lines]"
  load_manifest
  _tool get_exec_logs "$(jq -n --arg e "$exec_id" --argjson n "$lines" '{execution_id:$e,tail_lines:$n}')" ||
    die "get_exec_logs failed"
}

cmd_load() {
  local file="" name="" prepare=0
  while [ $# -gt 0 ]; do
    case "$1" in
      --file) file=$2; shift 2 ;;
      --name) name=$2; shift 2 ;;
      --prepare) prepare=1; shift ;;
      *) die "load: unknown flag $1" ;;
    esac
  done
  require_owner
  [ -n "$file" ] && [ -f "$file" ] || die "load: --file <local-package> required"
  name="${name:-$(basename "$file")}"
  case "$name" in *[!A-Za-z0-9._-]*) die "load: package name must match [A-Za-z0-9._-]+" ;; esac
  load_manifest
  local sha size b64 out pkg_id url
  sha="$(sha256sum "$file" | awk '{print $1}')"
  size="$(stat -c %s "$file")"
  local params
  params="$(jq -n --arg l "$LEASE_ID" --arg n "$name" --arg sha "$sha" --argjson s "$size" \
    '{lease_id:$l,name:$n,size_bytes:$s,sha256:$sha}')"
  if [ "$size" -le "$(_inline_limit)" ]; then
    b64="$(base64 -w0 "$file")"
    params="$(jq -n --argjson p "$params" --arg b "$b64" '$p + {content_base64:$b}')"
  fi
  out="$(_tool upload_package "$params")" || die "upload_package failed"
  pkg_id="$(jq -r '.package_id // empty' <<<"$out")"
  [ -n "$pkg_id" ] || die "load: no package_id returned"
  url="$(jq -r '.upload.url // empty' <<<"$out")"
  if [ -n "$url" ]; then
    local auth=()
    [ -n "$POOL_TOKEN" ] && auth=(-H "Authorization: Bearer $POOL_TOKEN")
    curl -fsS --max-time "$HTTP_TIMEOUT" -X PUT "$url" \
      -H "X-Sha256: $sha" "${auth[@]+"${auth[@]}"}" --data-binary "@$file" \
      >/dev/null || die "PUT upload failed for $name"
  fi
  {
    printf 'ARTIFACT_PACKAGE=%s\n' "$name"
    printf 'ARTIFACT_SHA256=%s\n' "$sha"
    printf 'ARTIFACT_SIZE=%s\n' "$size"
    printf 'ARTIFACT_PACKAGE_ID=%s\n' "$pkg_id"
  } >> "$(manifest_path)"
  if [ "$prepare" = 1 ]; then
    _tool prepare_workspace "$(jq -n --arg l "$LEASE_ID" --arg p "$pkg_id" '{lease_id:$l,package_id:$p}')" >/dev/null ||
      die "prepare_workspace failed"
    printf 'slot-pool: loaded %s (sha256 %s) and prepared workspace\n' "$name" "$sha"
  else
    printf 'slot-pool: staged %s (sha256 %s, package %s)\n' "$name" "$sha" "$pkg_id"
  fi
}

_inline_limit() { jq -r '.inline_limit_bytes // 16777216' "$POOL_CONFIG" 2>/dev/null || printf 16777216; }

cmd_lock_status() {
  if [ -f "$(manifest_path)" ]; then
    # shellcheck disable=SC1090
    . "$(manifest_path)"
    printf 'slot-pool: %s held by issue %s (phase %s, template %s, until %s)\n' \
      "$POOL_SLOT" "${ISSUE:-unknown}" "${PHASE:-unknown}" "${TEMPLATE:-unknown}" "${EXPIRES_AT:-unknown}"
  else
    printf 'slot-pool: %s free (no lease)\n' "$POOL_SLOT"
  fi
}

cmd_lock_release() {
  require_owner
  load_manifest
  local verdict rc=0
  verdict="$(_guard_prehook slot-release)" || rc=$?
  if [ "$rc" = 1 ]; then
    printf 'slot-pool: release refused (%s) — guard blocked %s; preserve the work, then re-run once with MULTICA_RECYCLE_OVERRIDE=1\n' "$verdict" "$LEASE_ID" >&2
    return 1
  fi
  _do_release "lock-release ($verdict)"
}

cmd_handoff() {
  local to="" template=""
  while [ $# -gt 0 ]; do
    case "$1" in
      --to) to=$2; shift 2 ;;
      --template) template=$2; shift 2 ;;
      *) die "handoff: unknown flag $1" ;;
    esac
  done
  case "$to" in dev|qa) ;; *) die "handoff: --to dev|qa required" ;; esac
  require_owner
  load_manifest
  local rc=0 verdict
  verdict="$(_guard_prehook "handoff-$to")" || rc=$?
  if [ "$rc" = 1 ]; then
    printf 'slot-pool: handoff refused (%s) — guard blocked %s\n' "$verdict" "$LEASE_ID" >&2
    return 1
  fi
  _do_release "handoff"
  cmd_use --phase "$to" ${template:+--template "$template"}
}

cmd_orphans() {
  local as_json=0
  [ "${1:-}" = "--json" ] && as_json=1
  load_manifest
  local out exit_code logs clean
  out="$(_tool exec "$(jq -n --arg l "$LEASE_ID" --arg g "$GUEST_INVENTORY" --arg e "$GUEST_ENV_REMOTE" \
      '{lease_id:$l,command:$g,args:["--env",$e,"--json"],cwd:"/",wait:true,timeout_seconds:300}')")" ||
    die "orphans: inventory exec failed (guest scripts present in image? multica-slot-base pending bake)"
  exit_code="$(jq -r '.exit_code // empty' <<<"$out" 2>/dev/null)"
  logs="$(jq -r '.log_tail // .logs // ""' <<<"$out" 2>/dev/null)"
  clean="$(jq -r '.clean // empty' <<<"$logs" 2>/dev/null)"
  if [ "$as_json" = 1 ]; then
    printf '%s\n' "$logs"
  else
    printf '%s\n' "$logs"
  fi
  [ "${exit_code:-1}" = "0" ] && [ "$clean" = "true" ] || return 1
}

cmd_list() {
  local as_json=0
  [ "${1:-}" = "--json" ] && as_json=1
  local out
  out="$(_tool list_leases '{"state":"active"}')"
  if [ "$as_json" = 1 ]; then printf '%s\n' "$out"; else
    jq -r '.leases[]? | "\(.lease_id) \(.owner // "?") \(.issue_id // "?") \(.template_id // "?") \(.state)"' <<<"$out" 2>/dev/null
  fi
}

cmd_gc() {
  local dry=0
  [ "${1:-}" = "--dry-run" ] && dry=1
  require_owner
  # Sweep OUR expired leases whose manifests still exist; the pool reaper is
  # the server-side backstop, this is the polite counterpart. A get_lease
  # transport failure must NOT look like an expiry — skip and say so.
  local dir m lease_id released=0 lease_out state
  for dir in "$POOL_HOME"/*/; do
    [ -d "$dir" ] || continue
    m="$dir/lease.env"
    [ -f "$m" ] || continue
    # shellcheck disable=SC1090
    . "$m"
    [ "${ISSUE:-}" = "$(caller_owner)" ] || continue
    lease_id="${LEASE_ID:-}"
    [ -n "$lease_id" ] || continue
    lease_out="$(_tool get_lease "$(jq -n --arg l "$lease_id" '{lease_id:$l}')" 2>/dev/null)" || {
      printf 'slot-pool: gc: %s unreachable (state unknown), keeping manifest\n' "$lease_id" >&2
      continue
    }
    state="$(jq -r '.state // empty' <<<"$lease_out" 2>/dev/null)"
    case "$state" in
      expired|"")
        if [ "$dry" = 1 ]; then
          printf 'slot-pool: gc would clear %s (%s, state=%s)\n' "$dir" "$lease_id" "${state:-unknown}"
        else
          rm -f "$m"
          printf 'slot-pool: gc cleared manifest %s (%s)\n' "$dir" "$lease_id"
        fi
        released=$((released + 1)) ;;
    esac
  done
  [ "$released" -gt 0 ] || printf 'slot-pool: gc nothing to collect\n'
}

cmd_audit() {
  local as_json=0
  [ "${1:-}" = "--json" ] && as_json=1
  local out leases findings=0 known
  out="$(_tool list_leases '{"state":"active"}')"
  leases="$(jq -c '.leases // []' <<<"$out" 2>/dev/null || printf '[]')"
  known=" $(for m in "$POOL_HOME"/*/lease.env; do [ -f "$m" ] && sed -n 's/^LEASE_ID=//p' "$m"; done | tr '\n' ' ')"
  local rows
  rows="$(jq -c '.leases[]?' <<<"$out" 2>/dev/null)"
  local payload="[]"
  if [ -n "$rows" ]; then
    payload="$(printf '%s\n' "$rows" | jq -c --arg known "$known" \
      'select(.lease_id as $id | $known | contains(" \($id) ") | not)')"
    if [ -n "$payload" ]; then
      payload="$(printf '%s\n' "$payload" | jq -s .)"
      findings="$(jq 'length' <<<"$payload")"
    else
      payload="[]"
    fi
  fi
  if [ "$as_json" = 1 ]; then
    jq -n --argjson f "$findings" --argjson u "$payload" '{findings:$f,unregistered:$u}'
  else
    if [ "$findings" -gt 0 ]; then
      printf '%s\n' "$payload" | jq -r '.[] | "unregistered lease: \(.lease_id) owner=\(.owner // "?") issue=\(.issue_id // "?") template=\(.template_id // "?")"'
    else
      printf 'audit: clean (no unregistered active leases)\n'
    fi
  fi
  [ "$findings" -eq 0 ]
}

usage() {
  cat <<'EOF'
Pool-backed slot leases (RUYI-676 E3 skeleton; MCP contract docs/03).

  slot-pool.sh <name> use    [--phase dev|qa] [--template <id>] [--ttl <min>]
  slot-pool.sh <name> load   --file <pkg> [--name <nm>] [--prepare]
  slot-pool.sh <name> status [--json]
  slot-pool.sh <name> exec   -- <command> [args...]
  slot-pool.sh <name> logs   <execution_id> [lines]
  slot-pool.sh <name> handoff --to dev|qa [--template <id>]
  slot-pool.sh <name> lock-status
  slot-pool.sh <name> lock-release      # guard pre-hook inside the VM first
  slot-pool.sh <name> orphans [--json]  # in-VM inventory must be clean
  slot-pool.sh list [--json]
  slot-pool.sh gc     [--dry-run]
  slot-pool.sh audit  [--json]          # unregistered active leases = exit 1

Env: MULTICA_POOL_URL, MULTICA_POOL_TOKEN (or ~/.multica/pool-auth),
MULTICA_SLOT_POOL_HOME, MULTICA_CALLER_OWNER (write verbs),
MULTICA_RECYCLE_OVERRIDE (one-shot), MULTICA_GC_GUARD_ENABLED=false (kill switch).
Config: scripts/slot-pool.json (phase→template/TTL, guest ports).
EOF
}

main() {
  local first="${1:-}"
  case "$first" in
    ""|-h|--help|help) usage; exit 0 ;;
    list|gc|audit)
      shift
      "cmd_${first}" "$@"
      exit $?
      ;;
  esac
  local action="${2:-}"
  [ $# -ge 2 ] || { usage >&2; exit 2; }
  POOL_SLOT=$first
  shift 2
  case "$action" in
    use) cmd_use "$@" ;;
    load) cmd_load "$@" ;;
    status) cmd_status "$@" ;;
    exec) cmd_exec "$@" ;;
    logs) cmd_logs "$@" ;;
    handoff) cmd_handoff "$@" ;;
    lock-status) cmd_lock_status ;;
    lock-release) cmd_lock_release ;;
    orphans) cmd_orphans "$@" ;;
    -h|--help|help) usage ;;
    *) usage >&2; printf '\nslot-pool: unknown action "%s"\n' "$action" >&2; exit 2 ;;
  esac
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  main "$@"
fi
