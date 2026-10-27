#!/usr/bin/env bash
# slot-pool.sh — pool backend of the multica slot tool layer (RUYI-676 E3,
# skeleton). Maps the local slot lease semantics onto the VM resource pool
# MCP (docs/03 contract): use→acquire_lease, load→upload_package(+prepare),
# exec→exec, logs→get_exec_logs, lock-release→guard pre-hook + release_lease,
# handoff→guarded release + re-acquire, orphans→in-VM inventory, audit→pool
# ledger sweep, gc→expired-lease sweep. The local slot track
# (scripts/dev-env.sh) is untouched; P2 wires backend selection.
#
# RUYI-783 pool contract alignment (client side):
#   - acquire_lease carries the one-shot bootstrap_credential (out-of-band,
#     host-admin delivered; pool stores only its digest) and returns a short
#     execution_capability; every later call rides it in X-Execution-Capability.
#   - phases are pool-side state: DEV<->QA handoff is transition_lease_phase
#     (CAS on expected_generation). The local manifest only mirrors pool state;
#     it is never the handoff mechanism.
#   - deploy phase maps to pool template tpl-multica-dev (scripts/slot-pool.json).
#
# Semantic invariants kept identical to the local track:
#   - Write verbs require MULTICA_CALLER_OWNER=<issue-id> (single writer =
#     one issue per lease; audit fields owner/issue_id ride every acquire).
#   - Release/handoff/gc run the RUYI-594 guard INSIDE the VM first
#     (fail-closed: transport failure = blocked), honour the same one-shot
#     MULTICA_RECYCLE_OVERRIDE and the MULTICA_GC_GUARD_ENABLED kill switch,
#     and file evidence under ~/.multica/recycle-evidence (same v1 format
#     written by scripts/slot-image/guest/guard-scan.sh).
#   - deploy phase = wide TTL + activity renew; qa phase = tighter TTL.
#     These phases describe deployment activity inside one resource-pool
#     lease. They never designate a developer's local build environment.
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

die() { printf 'slot-pool: %s\n' "$1" >&2; exit 1; }

# Token file fallback (RUYI-680 rider): when the env var is unset, read a single
# line from POOL_AUTH_FILE (default ~/.multica/pool-auth). Fail-closed on file
# mode: anything other than 600 is refused (never read, never echoed); the
# resolved value only ever goes out as a Bearer header, never logged.
POOL_AUTH_FILE="${POOL_AUTH_FILE:-$HOME/.multica/pool-auth}"
if [ -z "$POOL_TOKEN" ] && [ -f "$POOL_AUTH_FILE" ]; then
  _pa_mode="$(stat -c %a "$POOL_AUTH_FILE" 2>/dev/null || printf '0')"
  [ "$_pa_mode" = "600" ] ||
    die "pool-auth file $POOL_AUTH_FILE must be mode 600 (got $_pa_mode); fix permissions or set MULTICA_POOL_TOKEN"
  POOL_TOKEN="$(head -n1 "$POOL_AUTH_FILE" | tr -d '[:space:]')"
  unset _pa_mode
fi
POOL_HOME="${MULTICA_SLOT_POOL_HOME:-$HOME/.multica/slot-pool}"
# resolve_bootstrap_file resolves the mode-600 out-of-band bootstrap used to
# re-sign a persisted lease in a fresh process.
resolve_bootstrap_file() {
  local owner f mode
  owner="${1:-${MULTICA_CALLER_OWNER:-}}"
  [ -n "$owner" ] || return 1
  f="$HOME/.multica/pool-bootstrap/$owner"
  [ -f "$f" ] || return 1
  mode="$(stat -c %a "$f" 2>/dev/null || printf '0')"
  [ "$mode" = "600" ] || { printf 'slot-pool: bootstrap file %s must be mode 600 (got %s)\n' "$f" "$mode" >&2; return 1; }
  head -n1 "$f" | tr -d '[:space:]'
}

# resolve_bootstrap preserves the one-shot in-memory input accepted by `use`.
# Any later manifest-driven re-sign deliberately calls resolve_bootstrap_file.
resolve_bootstrap() {
  if [ -n "${SLOT_POOL_BOOTSTRAP_CREDENTIAL:-}" ]; then
    printf '%s' "$SLOT_POOL_BOOTSTRAP_CREDENTIAL"
    return 0
  fi
  resolve_bootstrap_file "$@"
}

# acquire_lease_params encodes the bootstrap credential through jq stdin so it
# never appears in a child process argument vector.
acquire_lease_params() { # <template> <owner> <request-id> <bootstrap> [ttl]
  local template=$1 owner=$2 request_id=$3 bootstrap=$4 ttl=${5:-}
  if [ -n "$ttl" ]; then
    printf '%s' "$bootstrap" | jq -Rsc \
      --arg t "$template" --arg o "$owner" --arg i "$owner" --arg r "$request_id" --argjson ttl "$ttl" \
      '{template_id:$t,owner:$o,issue_id:$i,ttl_minutes:$ttl,request_id:$r,bootstrap_credential:.}'
  else
    printf '%s' "$bootstrap" | jq -Rsc \
      --arg t "$template" --arg o "$owner" --arg i "$owner" --arg r "$request_id" \
      '{template_id:$t,owner:$o,issue_id:$i,bootstrap_credential:.,request_id:$r}'
  fi
}
POOL_CONFIG="${SLOT_POOL_CONFIG:-$SCRIPT_DIR/slot-pool.json}"
EVIDENCE_DIR="${MULTICA_RECYCLE_EVIDENCE_DIR:-$HOME/.multica/recycle-evidence}"
GUEST_GUARD="/usr/local/lib/multica/guard-scan.sh"
GUEST_INVENTORY="/usr/local/lib/multica/slot-inventory.sh"
GUEST_ENV_REMOTE="/home/agent/.multica-slot.env"
WORKSPACE_ROOT="/home/agent/workspace"
RENEW_AHEAD="${SLOT_POOL_RENEW_AHEAD_SECONDS:-1800}"
HTTP_TIMEOUT="${SLOT_POOL_HTTP_TIMEOUT:-120}"

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
  local method=$1 params=${2:-null} body out header_lines="" rpc_header_fd="" curl_rc
  command -v curl >/dev/null || die "curl is required"
  command -v jq >/dev/null || die "jq is required"
  if [[ "$method" == notifications/* ]]; then
    body="$(printf '%s' "$params" | jq -c --arg m "$method" '{jsonrpc:"2.0",method:$m,params:.}')"
  else
    RPC_ID=$((RPC_ID + 1))
    body="$(printf '%s' "$params" | jq -c --arg m "$method" --argjson i "$RPC_ID" \
      '{jsonrpc:"2.0",id:$i,method:$m,params:.}')"
  fi
  # Keep all credentials out of curl's argv: process listings expose literal
  # --data-binary and -H values. curl reads the sensitive headers through an
  # inherited descriptor and the JSON-RPC body from stdin instead.
  [ -n "$POOL_TOKEN" ] && header_lines+="Authorization: Bearer $POOL_TOKEN"$'\n'
  [ -n "${CAPABILITY:-}" ] && header_lines+="X-Execution-Capability: $CAPABILITY"$'\n'
  if [ -n "$header_lines" ]; then
    exec {rpc_header_fd}<<<"$header_lines"
  fi
  out="$(curl -sS --max-time "$HTTP_TIMEOUT" -X POST "$POOL_URL/mcp" \
    -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
    ${rpc_header_fd:+-H "@/proc/self/fd/$rpc_header_fd"} --data-binary @- <<<"$body" 2>&1)"
  curl_rc=$?
  [ -z "$rpc_header_fd" ] || exec {rpc_header_fd}<&-
  [ "$curl_rc" -eq 0 ] ||
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
  result="$(_rpc tools/call "$(printf '%s' "$params" | jq -c --arg n "$tool" '{name:$n,arguments:.}')")"
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
lock_slot() {
  local dir old_umask
  command -v flock >/dev/null || die "flock is required to serialize slot mutations"
  dir="$(dirname "$(manifest_path)")"
  mkdir -p "$dir"
  old_umask="$(umask)"
  umask 077
  exec {SLOT_LOCK_FD}>"$dir/.lease.lock"
  umask "$old_umask"
  chmod 600 "$dir/.lease.lock"
  flock -x "$SLOT_LOCK_FD" || die "could not lock slot $POOL_SLOT"
}
unlock_slot() {
  [ -n "${SLOT_LOCK_FD:-}" ] || return 0
  exec {SLOT_LOCK_FD}>&-
  unset SLOT_LOCK_FD
}
remove_manifest_capability() { # <path>
  sed -i '/^CAPABILITY=/d' "$1"
}
write_manifest() { # <lease_id> <instance_id> <template> <phase> <expires_at> [request_id] [version_sha]
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
    [ $# -ge 6 ] && [ -n "${6:-}" ] && printf 'REQUEST_ID=%s\n' "$6"
    [ $# -ge 7 ] && [ -n "${7:-}" ] && printf 'VERSION_SHA=%s\n' "$7"
  } > "$(manifest_path)"
  chmod 600 "$(manifest_path)"
}
set_manifest_field() { # <KEY> <VALUE> — upsert one manifest field
  local key=$1 val=$2
  [ "$key" != "CAPABILITY" ] || die "CAPABILITY is process-local and cannot be stored in a lease manifest"
  if grep -q "^$key=" "$(manifest_path)"; then
    sed -i "s/^$key=.*/$key=$val/" "$(manifest_path)"
  else
    printf '%s=%s\n' "$key" "$val" >> "$(manifest_path)"
  fi
  chmod 600 "$(manifest_path)"
}
load_manifest() {
  [ -f "$(manifest_path)" ] || die "no lease manifest for $POOL_SLOT (use first)"
  # Older manifests may still contain a stale short-lived capability. Remove it
  # before sourcing so it cannot escape into a new process or a later rewrite.
  remove_manifest_capability "$(manifest_path)"
  # shellcheck disable=SC1090
  . "$(manifest_path)"
  [ "${LEASE_ID:-}" ] || die "manifest for $POOL_SLOT has no LEASE_ID"
  unset CAPABILITY CAPABILITY_ISSUED_AT
  ensure_execution_capability
}

install_execution_capability() { # <capability>
  [ -n "${1:-}" ] || return 1
  CAPABILITY=$1
  CAPABILITY_ISSUED_AT="$(date -u '+%s')"
}

capability_needs_resign() {
  local after now
  after="${SLOT_POOL_CAPABILITY_RENEW_AFTER_SECONDS:-240}"
  case "$after" in ''|*[!0-9]*) return 1 ;; esac
  [ -n "${CAPABILITY:-}" ] && [ -n "${CAPABILITY_ISSUED_AT:-}" ] || return 0
  now="$(date -u '+%s')"
  [ $((now - CAPABILITY_ISSUED_AT)) -ge "$after" ]
}

# resign_execution_capability replays a recorded acquire request with a local
# bootstrap credential. The pool's request-id idempotency returns the same
# lease with a fresh capability; this function never writes either secret to
# the manifest.
resign_execution_capability() { # <expected-lease-id> <template> <owner> <request-id>
  local expected_lease_id=$1 template=$2 owner=$3 request_id=$4 bootstrap out lease_id capability
  [ -n "$expected_lease_id" ] && [ -n "$template" ] && [ -n "$owner" ] && [ -n "$request_id" ] || return 1
  bootstrap="$(resolve_bootstrap_file "$owner")" || return 1
  CAPABILITY=""
  out="$(_tool acquire_lease "$(acquire_lease_params "$template" "$owner" "$request_id" "$bootstrap")")" || return 1
  lease_id="$(jq -r '.lease_id // empty' <<<"$out")"
  capability="$(jq -r '.execution_capability // empty' <<<"$out")"
  [ "$lease_id" = "$expected_lease_id" ] && [ -n "$capability" ] || return 1
  install_execution_capability "$capability"
}

ensure_execution_capability() {
  [ -n "${REQUEST_ID:-}" ] || die "manifest for $POOL_SLOT has no REQUEST_ID (older manifest; re-run use instead)"
  [ -n "${TEMPLATE:-}" ] && [ -n "${ISSUE:-}" ] || die "manifest for $POOL_SLOT cannot re-sign its execution capability"
  if [ -z "${CAPABILITY:-}" ] || capability_needs_resign; then
    resign_execution_capability "$LEASE_ID" "$TEMPLATE" "$ISSUE" "$REQUEST_ID" ||
      die "lease $LEASE_ID could not be re-signed with its recorded request ID"
  fi
}

config_phase_field() { # <phase> <field>
  jq -r --arg ph "$1" --arg f "$2" '.[$ph][$f] // empty' "$POOL_CONFIG" 2>/dev/null
}

epoch_of() { date -d "$1" '+%s' 2>/dev/null || printf 0; }

# _maybe_renew <expires_at> — activity renewal (deploy 长租约 + 活跃续约):
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

# _guard_verdict <log_tail> — the last "verdict: " line of the guest guard's
# stdout (per-carrier lines then the overall verdict; last wins).
_guard_verdict() { printf '%s\n' "$1" | sed -n 's/^verdict: //p' | tail -1; }

# _archive_guard_evidence <execution_id> — pull the guard's evidence artifacts
# (collected in-VM from .recycle-evidence/ via artifact_globs) into the local
# recycle-evidence dir, byte-identical and sha256-verified. Prints one
# "archived: <path>" line per file; returns 1 on any failure.
_archive_guard_evidence() {
  local execution_id=$1 list n i
  list="$(_tool list_artifacts "$(jq -n --arg l "$LEASE_ID" --arg e "$execution_id" '{lease_id:$l,execution_id:$e}')")"
  n="$(jq '.artifacts | length' <<<"$list" 2>/dev/null)" || n=0
  if [ "${n:-0}" -eq 0 ]; then
    printf 'verdict: blocked (fail-closed: guard produced no evidence artifact)\n' >&2
    return 1
  fi
  mkdir -p "$EVIDENCE_DIR"
  chmod 700 "$EVIDENCE_DIR" 2>/dev/null || true
  for ((i = 0; i < n; i++)); do
    local art_id art_path art_sha art_b64 art_url dest got
    art_id="$(jq -r ".artifacts[$i].artifact_id // empty" <<<"$list")"
    art_path="$(jq -r ".artifacts[$i].path // empty" <<<"$list")"
    art_sha="$(jq -r ".artifacts[$i].sha256 // empty" <<<"$list")"
    if [ -z "$art_id" ] || [ -z "$art_path" ]; then
      printf 'verdict: blocked (fail-closed: evidence artifact entry missing id/path)\n' >&2
      return 1
    fi
    local dl
    dl="$(_tool download_artifact "$(jq -n --arg l "$LEASE_ID" --arg a "$art_id" '{lease_id:$l,artifact_id:$a}')")" || {
      printf 'verdict: blocked (fail-closed: evidence download failed for %s)\n' "$art_id" >&2
      return 1
    }
    dest="$EVIDENCE_DIR/$(basename "$art_path")"
    local suf="" k=0
    while [ -e "$dest$suf" ]; do k=$((k + 1)); suf="_$k"; done
    dest="$dest$suf"
    art_b64="$(jq -r '.content_base64 // empty' <<<"$dl" 2>/dev/null)"
    if [ -n "$art_b64" ]; then
      printf '%s' "$art_b64" | base64 -d >"$dest" || {
        rm -f "$dest"
        printf 'verdict: blocked (fail-closed: evidence base64 decode failed)\n' >&2
        return 1
      }
    else
      art_url="$(jq -r '.download_url // empty' <<<"$dl" 2>/dev/null)"
      if [ -z "$art_url" ]; then
        printf 'verdict: blocked (fail-closed: evidence download returned no content)\n' >&2
        return 1
      fi
      local auth=()
      [ -n "$POOL_TOKEN" ] && auth=(-H "Authorization: Bearer $POOL_TOKEN")
      curl -fsS --max-time "$HTTP_TIMEOUT" "${auth[@]+"${auth[@]}"}" "$art_url" >"$dest" || {
        rm -f "$dest"
        printf 'verdict: blocked (fail-closed: evidence GET failed)\n' >&2
        return 1
      }
    fi
    if [ -n "$art_sha" ]; then
      got="$(sha256sum "$dest" | awk '{print $1}')"
      if [ "$got" != "$art_sha" ]; then
        rm -f "$dest"
        printf 'verdict: blocked (fail-closed: evidence sha256 mismatch for %s)\n' "$(basename "$art_path")" >&2
        return 1
      fi
    fi
    chmod 600 "$dest" 2>/dev/null || true
    printf 'archived: %s\n' "$dest"
  done
}

# _guard_prehook <kind> <dry_run:0|1> — run the RUYI-594 guard inside the VM
# and archive its evidence locally. Verdict comes from the guest guard's own
# stdout (verdict: line), never from an exec-result field. The evidence chain
# is part of the guard: a release is only accountable when the scan's evidence
# landed in the local archive, so for an ALLOWING verdict a missing artifact,
# a download failure or a sha256 mismatch is fail-closed exactly like a scan
# failure. Blocked verdicts refuse first and archive best-effort (the lease is
# kept either way). Returns 0 when the caller may proceed, 1 when blocked,
# 2 when skipped (kill switch).
_guard_prehook() {
  local kind=$1 dry_run=${2:-0} out exit_code verdict exec_id
  case "${MULTICA_GC_GUARD_ENABLED:-true}" in
    false|FALSE|0|off|OFF) printf 'verdict: disabled\n'; return 2 ;;
  esac
  local env_json="{\"MULTICA_RECYCLE_EVIDENCE_DIR\":\"$WORKSPACE_ROOT/.recycle-evidence\",\"RG_TOOL\":\"slot-pool\""
  if [ "${MULTICA_RECYCLE_OVERRIDE:-}" = "1" ]; then
    env_json="$env_json,\"MULTICA_RECYCLE_OVERRIDE\":\"1\""
  fi
  env_json="$env_json}"
  out="$(_tool exec "$(jq -n \
      --arg l "${LEASE_ID:?}" --arg g "$GUEST_GUARD" --arg w "$WORKSPACE_ROOT" --arg k "$kind" --arg d "$dry_run" --argjson e "$env_json" \
      '{lease_id:$l,command:$g,args:[$w,$k,$d],env:$e,cwd:"/",wait:true,timeout_seconds:600,
       artifact_globs:[".recycle-evidence/*.txt"]}')")" || {
    printf 'verdict: blocked (fail-closed: guard exec failed)\n'
    return 1
  }
  exit_code="$(jq -r '.exit_code // empty' <<<"$out" 2>/dev/null)"
  exec_id="$(jq -r '.execution_id // empty' <<<"$out" 2>/dev/null)"
  verdict="$(_guard_verdict "$(jq -r '.log_tail // ""' <<<"$out" 2>/dev/null)")"
  case "$exit_code" in
    0) ;;
    1)
      # Blocked: refuse first, then archive best-effort so the operator has the
      # refusal evidence at hand without another pool roundtrip.
      [ -n "$verdict" ] || verdict="blocked"
      printf 'verdict: %s\n' "$verdict"
      printf '%s\n' "$(jq -r '.log_tail // ""' <<<"$out" 2>/dev/null)"
      if [ -n "$exec_id" ]; then
        _archive_guard_evidence "$exec_id" ||
          printf 'slot-pool: warning: evidence archive failed (lease kept; artifacts remain in the pool retention window)\n' >&2
      fi
      return 1 ;;
    "")
      printf 'verdict: blocked (fail-closed: guard exec returned no exit code)\n'
      return 1 ;;
    *)
      printf 'verdict: blocked (fail-closed: guard exit_code=%s state=%s)\n' \
        "$exit_code" "$(jq -r '.state // "unknown"' <<<"$out" 2>/dev/null)"
      return 1 ;;
  esac
  if [ -z "$verdict" ]; then
    printf 'verdict: blocked (fail-closed: guard stdout has no verdict line)\n'
    return 1
  fi
  case "$verdict" in
    pass|evidence) ;;
    blocked)
      printf 'verdict: blocked (fail-closed: guard exit_code=0 with blocked verdict)\n'
      return 1 ;;
    disabled)
      printf 'verdict: blocked (fail-closed: unexpected disabled verdict)\n'
      return 1 ;;
    *)
      printf 'verdict: blocked (fail-closed: unknown guard verdict)\n'
      return 1 ;;
  esac
  local archived
  archived="$(_archive_guard_evidence "$exec_id")" || return 1
  printf '%s\n' "$archived"
  printf 'verdict: %s\n' "$verdict"
}

_do_release() { # <reason>
  _tool release_lease "$(jq -n --arg l "$LEASE_ID" '{lease_id:$l}')" >/dev/null ||
    die "release_lease failed for $LEASE_ID"
  rm -f "$(manifest_path)"
  printf 'slot-pool: %s released %s\n' "$1" "$LEASE_ID"
}

# --- verbs -------------------------------------------------------------------

cmd_use() {
  local phase=deploy template="" ttl="" version_sha="" request_id_override=""
  while [ $# -gt 0 ]; do
    case "$1" in
      --phase) phase=$2; shift 2 ;;
      --template) template=$2; shift 2 ;;
      --ttl) ttl=$2; shift 2 ;;
      --version) version_sha=$2; shift 2 ;;
      --request-id) request_id_override=$2; shift 2 ;;
      *) die "use: unknown flag $1" ;;
    esac
  done
  # dev is accepted only for existing callers. It is an alias for deploy, not
  # permission to move source development or builds into the resource pool.
  if [ "$phase" = "dev" ]; then
    printf 'slot-pool: --phase dev is deprecated; treating it as deploy (the pool is deployment-only)\n' >&2
    phase=deploy
  fi
  case "$phase" in deploy|qa) ;; *) die "use: phase must be deploy or qa" ;; esac
  require_owner
  lock_slot
  template="${template:-$(config_phase_field "$phase" template)}"
  ttl="${ttl:-$(config_phase_field "$phase" ttl_minutes)}"
  [ -n "$template" ] || die "use: no template for phase $phase (scripts/slot-pool.json)"
  local bootstrap
  bootstrap="$(resolve_bootstrap)" ||
    die "use: bootstrap credential required (set SLOT_POOL_BOOTSTRAP_CREDENTIAL, or drop a mode-600 credential at ~/.multica/pool-bootstrap/$(caller_owner); the value is sent once and never logged)"
  local out lease_id instance_id expires_at state capability
  local request_id="$(caller_owner):$POOL_SLOT:$phase"
  # Reusing a recorded request_id replays the same lease (idempotent pool
  # contract): the owner can bind the manifest to a lease acquired out-of-band.
  [ -n "$request_id_override" ] && request_id="$request_id_override"
  out="$(_tool acquire_lease "$(acquire_lease_params "$template" "$(caller_owner)" "$request_id" "$bootstrap" "${ttl:-60}")")"
  lease_id="$(jq -r '.lease_id // empty' <<<"$out")"
  instance_id="$(jq -r '.instance_id // empty' <<<"$out")"
  expires_at="$(jq -r '.expires_at // empty' <<<"$out")"
  [ -n "$lease_id" ] || die "use: acquire returned no lease id"
  # A pending lease is already capability-gated by the pool. Install the
  # acquire response before the first get_lease poll; it never leaves memory.
  capability="$(jq -r '.execution_capability // empty' <<<"$out")"
  [ -n "$capability" ] || die "use: acquire returned no execution capability"
  install_execution_capability "$capability"
  # New-instance provisioning is async (RUYI-597): poll get_lease until the
  # lease settles. The reused-instance path returns state=active directly.
  state="$(jq -r '.state // "active"' <<<"$out")"
  if [ "$state" = "pending" ]; then
    out="$(poll_pending_lease "$lease_id" "$template" "$(caller_owner)" "$request_id")" || die "use: provisioning of $lease_id did not complete"
    state="$(jq -r '.state // empty' <<<"$out")"
    expires_at="$(jq -r '.expires_at // empty' <<<"$out")"
    # A pool may re-sign on activation. Keep the acquire capability when the
    # settled payload omits one, otherwise carry the newer in-memory value on.
    local settled_capability
    settled_capability="$(jq -r '.execution_capability // empty' <<<"$out")"
    if [ -n "$settled_capability" ]; then
      capability="$settled_capability"
      install_execution_capability "$capability"
    fi
  fi
  case "$state" in
    active) ;;
    failed) die "use: lease $lease_id provisioning failed: $(jq -r '.last_error // "unknown"' <<<"$out" 2>/dev/null)" ;;
    *) die "use: lease $lease_id in unexpected state '${state:-unknown}'" ;;
  esac
  write_manifest "$lease_id" "$instance_id" "$template" "$phase" "$expires_at" "$request_id" "$version_sha"
  # Subshell: a die inside (VM still settling) must not lose the lease.
  ( _write_guest_env "$lease_id" ) || printf 'slot-pool: warning: guest env write failed (orphans inventory will report it)\n' >&2
  printf 'slot-pool: %s acquired %s (%s/%s, until %s)\n' "$POOL_SLOT" "$lease_id" "$template" "$phase" "$expires_at"
}

# get_lease_json <lease_id> — capability-aware get_lease (manifest must be
# loaded so _rpc rides the current execution capability).
get_lease_json() {
  local l=$1
  _tool get_lease "$(jq -n --arg l "$l" '{lease_id:$l}')"
}

# get_lease_json_recoverable deliberately does not call _tool: _tool exits the
# current shell on an isError response, while pending provisioning must catch a
# rejected short-lived capability and renew it with the bootstrap credential.
get_lease_json_recoverable() {
  local l=$1 result text
  (_pool_handshake) || return 1
  result="$(_rpc tools/call "$(jq -n --arg l "$l" '{name:"get_lease",arguments:{lease_id:$l}}')")" || return 1
  [ -n "$result" ] || return 1
  jq -e '.isError == true' >/dev/null 2>&1 <<<"$result" && return 1
  text="$(jq -r '.content[0].text // empty' <<<"$result")"
  jq -ec . <<<"$text" 2>/dev/null
}

# poll_pending_lease <lease_id> — wait for async provisioning to settle
# (docs/03 polling semantics: 2-5 s interval, bounded by the provision timeout).
# Prints the settled lease payload; rc=0 only for state=active.
poll_pending_lease() {
  local lease_id=$1 template=$2 owner=$3 request_id=$4 timeout start now
  timeout="${SLOT_POOL_PROVISION_TIMEOUT_SECONDS:-600}"
  start="$(date -u '+%s')"
  while :; do
    local out
    if capability_needs_resign; then
      resign_execution_capability "$lease_id" "$template" "$owner" "$request_id" || {
        printf 'slot-pool: could not re-sign pending lease %s\n' "$lease_id" >&2
        return 1
      }
    fi
    out="$(get_lease_json_recoverable "$lease_id")" || {
      # An authorization failure commonly means the short-lived header expired.
      # Replaying this exact idempotency key must return the same lease before
      # the pending poll is allowed to continue.
      resign_execution_capability "$lease_id" "$template" "$owner" "$request_id" || {
        printf 'slot-pool: get_lease failed and pending lease %s could not be re-signed\n' "$lease_id" >&2
        return 1
      }
      out="$(get_lease_json_recoverable "$lease_id")" || {
        printf 'slot-pool: get_lease failed while polling %s after re-signing\n' "$lease_id" >&2
        return 1
      }
    }
    local state
    state="$(jq -r '.state // empty' <<<"$out")"
    case "$state" in
      active)
        printf '%s' "$out"
        return 0 ;;
      failed)
        printf '%s' "$out"
        printf 'slot-pool: lease %s provisioning failed: %s\n' "$lease_id" "$(jq -r '.last_error // "unknown"' <<<"$out")" >&2
        return 1 ;;
      pending|"")
        ;;
      *)
        printf 'slot-pool: lease %s in unexpected state %s\n' "$lease_id" "$state" >&2
        return 1 ;;
    esac
    now="$(date -u '+%s')"
    if [ $((now - start)) -ge "$timeout" ]; then
      printf 'slot-pool: provisioning of %s still pending after %ss\n' "$lease_id" "$timeout" >&2
      return 1
    fi
    sleep 5
  done
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
  local cwd=""
  while [ "$#" -gt 0 ] && [ "${1:-}" != "--" ]; do
    case "$1" in
      --cwd) cwd=$2; shift 2 ;;
      *) die "exec: usage: slot-pool.sh <name> exec [--cwd <dir>] -- <command> [args...]" ;;
    esac
  done
  [ "${1:-}" = "--" ] || die "exec: usage: slot-pool.sh <name> exec [--cwd <dir>] -- <command> [args...]"
  shift
  local cmd=$1; shift
  load_manifest
  _maybe_renew
  local args_json="[]"
  if [ "$#" -gt 0 ]; then
    args_json="$(printf '%s\n' "$@" | jq -Rsc 'split("\n")[:-1]')"
  fi
  local extra="{}"
  [ -n "$cwd" ] && extra="$(jq -n --arg c "$cwd" '{cwd:$c}')"
  local out exit_code
  out="$(_tool exec "$(jq -n --arg l "$LEASE_ID" --arg c "$cmd" --argjson a "$args_json" --argjson x "$extra" \
      '{lease_id:$l,command:$c,args:$a,wait:true} + $x')")" ||
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
    case "$url" in
      *$'\n'*|*$'\r'*|*'"'*|*'\\'*) die "PUT upload URL contains unsafe configuration characters" ;;
    esac
    local upload_config_fd="" upload_rc upload_config
    upload_config="url = \"$url\""$'\n'
    upload_config+="request = \"PUT\""$'\n'
    upload_config+="header = \"X-Sha256: $sha\""$'\n'
    [ -z "$POOL_TOKEN" ] || upload_config+="header = \"Authorization: Bearer $POOL_TOKEN\""$'\n'
    exec {upload_config_fd}<<<"$upload_config"
    if curl -fsS --max-time "$HTTP_TIMEOUT" --config "/proc/self/fd/$upload_config_fd" \
      --data-binary "@$file" >/dev/null; then
      upload_rc=0
    else
      upload_rc=$?
    fi
    exec {upload_config_fd}<&-
    [ "$upload_rc" -eq 0 ] || die "PUT upload failed for $name"
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
    remove_manifest_capability "$(manifest_path)"
    # shellcheck disable=SC1090
    . "$(manifest_path)"
    printf 'slot-pool: %s held by issue %s (phase %s, template %s, until %s)\n' \
      "$POOL_SLOT" "${ISSUE:-unknown}" "${PHASE:-unknown}" "${TEMPLATE:-unknown}" "${EXPIRES_AT:-unknown}"
  else
    printf 'slot-pool: %s free (no lease)\n' "$POOL_SLOT"
  fi
}

cmd_lock_release() {
  local dry_run=0
  [ "${1:-}" = "--dry-run" ] && dry_run=1
  require_owner
  load_manifest
  local verdict rc=0
  verdict="$(_guard_prehook slot-release "$dry_run")" || rc=$?
  if [ "$rc" = 1 ]; then
    printf 'slot-pool: release refused (%s) — guard blocked %s; preserve the work, then re-run once with MULTICA_RECYCLE_OVERRIDE=1\n' "$verdict" "$LEASE_ID" >&2
    return 1
  fi
  if [ "$dry_run" = 1 ]; then
    printf 'slot-pool: dry-run complete (%s); lease %s untouched\n' "$verdict" "$LEASE_ID"
    return 0
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
  if [ "$to" = "dev" ]; then
    printf 'slot-pool: --to dev is deprecated; treating it as deploy (the pool is deployment-only)\n' >&2
    to=deploy
  fi
  case "$to" in deploy|qa) ;; *) die "handoff: --to deploy|qa required" ;; esac
  require_owner
  lock_slot
  load_manifest
  local rc=0 verdict
  verdict="$(_guard_prehook "handoff-$to" 0)" || rc=$?
  if [ "$rc" = 1 ]; then
    printf 'slot-pool: handoff refused (%s) — guard blocked %s\n' "$verdict" "$LEASE_ID" >&2
    return 1
  fi
  # A phase change belongs to the same Issue lease. Releasing and acquiring a
  # second lease here used to create a capacity race and broke deployment→QA
  # continuity. Template changes require a new deployment lease instead.
  if [ -n "$template" ] && [ "$template" != "$TEMPLATE" ]; then
    die "handoff: template changes require release followed by a new use; phase handoff preserves the current lease"
  fi
  # The handoff itself is the pool-side atomic CAS (RUYI-783 contract):
  # transition_lease_phase fences the generation, drains in-flight executions,
  # isolates the phase data namespace and re-signs the capability. The local
  # manifest only mirrors the pool state afterwards - it is not the mechanism.
  local target_phase gen resp err transition newcap
  case "$to" in
    qa) target_phase="QA" ;;
    deploy) target_phase="DEV" ;;
  esac
  gen="$(get_lease_json "$LEASE_ID" | jq -r '.generation // empty')"
  [ -n "$gen" ] || die "handoff: pool lease $LEASE_ID reports no generation (pool server older than RUYI-783? transition unavailable)"
  local retries=0
  while :; do
    resp="$(transition_raw "$LEASE_ID" "$target_phase" "$gen")"
    if ! jq -e '.isError == true' >/dev/null 2>&1 <<<"$resp"; then
      break
    fi
    err="$(jq -r '.content[0].text // "unknown"' <<<"$resp")"
    if [ "$retries" -eq 0 ] && printf '%s' "$err" | grep -q "阶段代际冲突"; then
      # Stale generation CAS miss: re-read and retry exactly once.
      retries=1
      gen="$(get_lease_json "$LEASE_ID" | jq -r '.generation // empty')"
      continue
    fi
    die "handoff: pool refused the phase transition: $err"
  done
  transition="$(jq -er --arg lease "$LEASE_ID" --arg phase "$target_phase" '
    select(.isError != true)
    | .content[0].text
    | fromjson?
    | select(type == "object")
    | select(.lease.lease_id == $lease)
    | select(.lease.phase == $phase)
    | select(.lease.state == "active")
    | select(.lease.generation | type == "number")
    | select(.execution_capability | type == "string" and length > 0)
  ' <<<"$resp" 2>/dev/null)" ||
    die "handoff: pool transition returned no valid active lease result"
  newcap="$(jq -r '.execution_capability' <<<"$transition")"
  gen="$(jq -r '.lease.generation' <<<"$transition")"
  set_manifest_field PHASE "$to"
  install_execution_capability "$newcap"
  printf 'slot-pool: handoff %s transitioned lease %s to %s (generation %s)\n' "$POOL_SLOT" "$LEASE_ID" "$target_phase" "$gen"
}

# transition_raw <lease_id> <target_phase> <generation> — one transition_lease_phase
# call without _tool's die-on-error: callers inspect isError themselves (the CAS
# retry path needs the pool's refusal text, and must not die on the first miss).
transition_raw() {
  _rpc tools/call "$(jq -cn --arg l "$1" --arg p "$2" --argjson g "$3" \
    '{name:"transition_lease_phase",arguments:{lease_id:$l,target_phase:$p,expected_generation:$g}}')"
}

cmd_refresh() {
  # Re-sign the short-lived execution capability for the lease this slot holds:
  # re-send the same one-shot bootstrap credential with the recorded request_id
  # (idempotent: the pool re-signs the same active lease, never creates a new one).
  require_owner
  load_manifest
  local out
  # load_manifest already re-signed from the out-of-band bootstrap and the
  # recorded idempotency key; the read below verifies the renewed header works.
  out="$(get_lease_json "$LEASE_ID")"
  case "$(jq -r '.state // empty' <<<"$out")" in
    active) ;;
    *) die "refresh: lease $LEASE_ID is not active (state $(jq -r '.state // empty' <<<"$out"))" ;;
  esac
  printf 'slot-pool: %s re-signed the execution capability for lease %s (phase %s)\n' "$POOL_SLOT" "$LEASE_ID" "${PHASE:-unknown}"
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
  # The exec channel may fold ssh transport noise (known_hosts notices) into
  # log_tail ahead of the inventory JSON; whole-buffer jq would choke on the
  # prefix. Scan line-wise and keep the last parseable inventory object.
  local inv=""
  while IFS= read -r line; do
    case "$line" in
      '{'*) jq -e '.clean != null' >/dev/null 2>&1 <<<"$line" && inv="$line" ;;
    esac
  done <<<"$logs"
  clean="$(jq -r '.clean // empty' <<<"$inv" 2>/dev/null)"
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
  local dir m slot lease_id template issue request_id released=0 lease_out state
  local pool_slot_before_gc=${POOL_SLOT:-}
  for dir in "$POOL_HOME"/*/; do
    [ -d "$dir" ] || continue
    slot="${dir%/}"
    POOL_SLOT="${slot##*/}"
    m="$(manifest_path)"
    [ -f "$m" ] || continue
    lock_slot
    [ -f "$m" ] || { unlock_slot; continue; }
    remove_manifest_capability "$m"
    unset LEASE_ID TEMPLATE ISSUE REQUEST_ID CAPABILITY CAPABILITY_ISSUED_AT
    # shellcheck disable=SC1090
    . "$m"
    [ "${ISSUE:-}" = "$(caller_owner)" ] || { unlock_slot; continue; }
    lease_id="${LEASE_ID:-}"
    [ -n "$lease_id" ] || { unlock_slot; continue; }
    template="${TEMPLATE:-}"
    issue="${ISSUE:-}"
    request_id="${REQUEST_ID:-}"
    if ! resign_execution_capability "$lease_id" "$template" "$issue" "$request_id"; then
      printf 'slot-pool: gc: %s could not be re-signed, keeping manifest\n' "$lease_id" >&2
      unlock_slot
      continue
    fi
    lease_out="$(_tool get_lease "$(jq -n --arg l "$lease_id" '{lease_id:$l}')" 2>/dev/null)" || {
      printf 'slot-pool: gc: %s unreachable (state unknown), keeping manifest\n' "$lease_id" >&2
      unlock_slot
      continue
    }
    state="$(jq -r '.state // empty' <<<"$lease_out" 2>/dev/null)"
    # Only an explicit server-side "expired" clears the manifest; empty or
    # unexpected states are treated as unknown and kept (never GC on ambiguity).
    case "$state" in
      expired)
        if [ "$dry" = 1 ]; then
          printf 'slot-pool: gc would clear %s (%s, state=%s)\n' "$dir" "$lease_id" "$state"
        else
          rm -f "$m"
          printf 'slot-pool: gc cleared manifest %s (%s)\n' "$dir" "$lease_id"
        fi
        released=$((released + 1)) ;;
    esac
    unlock_slot
  done
  POOL_SLOT=$pool_slot_before_gc
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
Pool-backed slot leases (RUYI-676 E3; MCP contract docs/03).

  slot-pool.sh <name> use    [--phase deploy|qa] [--template <id>] [--ttl <min>] [--version <source-sha>] [--request-id <id>]
  slot-pool.sh <name> load   --file <pkg> [--name <nm>] [--prepare]
  slot-pool.sh <name> status [--json]
  slot-pool.sh <name> exec   [--cwd <dir>] -- <command> [args...]
  slot-pool.sh <name> logs   <execution_id> [lines]
  slot-pool.sh <name> handoff --to deploy|qa   # pool-side transition_lease_phase CAS; manifest mirrors pool state
  slot-pool.sh <name> refresh                   # re-sign the short-lived execution capability
  slot-pool.sh <name> lock-status
  slot-pool.sh <name> lock-release [--dry-run]
                              # guard runs inside the VM; the release is only
                              # granted when the scan verdict is grantable AND
                              # its evidence landed in the local archive
                              # (fail-closed end to end). --dry-run runs guard
                              # + archive and keeps the lease.
  slot-pool.sh <name> orphans [--json]  # in-VM inventory must be clean
  slot-pool.sh list [--json]
  slot-pool.sh gc     [--dry-run]
  slot-pool.sh audit  [--json]          # unregistered active leases = exit 1

Env: MULTICA_POOL_URL, MULTICA_POOL_TOKEN (or ~/.multica/pool-auth, mode 600),
SLOT_POOL_BOOTSTRAP_CREDENTIAL (one-shot bootstrap credential, in-memory; never
logged) or ~/.multica/pool-bootstrap/<owner> (mode 600; out-of-band host-admin
delivery - the pool stores only its digest, the plaintext never enters logs
or the repository), MULTICA_SLOT_POOL_HOME, MULTICA_CALLER_OWNER (write verbs),
SLOT_POOL_PROVISION_TIMEOUT_SECONDS (async provisioning poll bound, default 600),
MULTICA_RECYCLE_OVERRIDE (one-shot), MULTICA_GC_GUARD_ENABLED=false (kill switch),
MULTICA_RECYCLE_EVIDENCE_DIR (local archive, default ~/.multica/recycle-evidence).
Config: scripts/slot-pool.json (deployment phase→template/TTL, guest ports).
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
    refresh) cmd_refresh "$@" ;;
    lock-status) cmd_lock_status ;;
    lock-release) cmd_lock_release "$@" ;;
    orphans) cmd_orphans "$@" ;;
    list) cmd_list "$@" ;;
    -h|--help|help) usage ;;
    *) usage >&2; printf '\nslot-pool: unknown action "%s"\n' "$action" >&2; exit 2 ;;
  esac
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  main "$@"
fi
