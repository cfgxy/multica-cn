#!/usr/bin/env bash
# Local development environments as fixed, lease-protected slots.
#
#   scripts/dev-env.sh dev1 up                 # start the slot (api + web)
#   scripts/dev-env.sh dev1 up C=api,web       # pick the components
#   scripts/dev-env.sh dev1 use <sha>          # load a revision into the slot
#   scripts/dev-env.sh dev1 handoff --to qa    # dev -> QA verification handover
#   scripts/dev-env.sh dev1 status             # what is running, and whose
#   scripts/dev-env.sh list                    # every slot on this machine
#   scripts/dev-env.sh audit                   # bypass sweep: stray multica_% dbs + servers
#   scripts/dev-env.sh dev1 down               # stop the processes, keep the data
#   scripts/dev-env.sh dev1 orphans            # leftover procs/ports/worktrees
#   scripts/dev-env.sh dev1 destroy            # stop, then drop slot db + account
#
# The slot model (RUYI-333) replaces the dynamic name+offset allocator: every
# slot's ports, database, account, directories and RESOURCE BUDGET are FIXED
# facts recorded in scripts/slots.json, so two environments can never drift
# into sharing a database again (the RUYI-300 exposure window). There are
# exactly two general-purpose slots, dev1 and dev2; QA runs no separate
# infrastructure — it borrows the issue's DEV slot via the lease phase
# lifecycle dev -> qa -> release. Four invariants the verbs below keep:
#
#  1. A slot's env file is GENERATED from slots.json, never copied from a
#     checkout's .env: endpoint localhost:5432, slot database `multica_<slot>`,
#     slot account `<slot>_app`. `up` re-verifies all three from the file on
#     disk before anything starts — a tampered file is refused, not fixed.
#  2. The shared instance's main database `multica` and the `multica` role are
#     hard-coded out of reach: no verb constructs a DROP/ALTER for them, the
#     names are checked against the slot pattern before any SQL is issued, and
#     the opt-in `main-db status` entry executes SELECT-only statements.
#  3. Write verbs require the caller to name an issue (MULTICA_CALLER_OWNER)
#     and honour the slot lease at ~/.multica/slots/<slot>/.slot-lock — a slot
#     held by an in-progress issue cannot be taken over, only released when
#     the issue leaves in_progress. The lease carries a PHASE (dev|qa); the
#     handoff verb moves between phases atomically under the slot lock, one
#     role holds the slot at any moment.
#  4. Every slot process runs inside the slot's resource budget: pinned CPU
#     set, per-component memory caps (GOMEMLIMIT / NODE_OPTIONS) and the
#     per-slot watchdog (scripts/slot-watchdog.sh) that kills any component
#     whose RSS outgrows its quota — a runaway next-server can never again
#     take the host down (2026-10-02 OOM).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Recycle guard (RUYI-594): scan-before-recycle + evidence for destroy/gc.
# shellcheck source=scripts/lib-recycle-guard.sh
source "$REPO_ROOT/scripts/lib-recycle-guard.sh"

SLOTS_FILE="$REPO_ROOT/scripts/slots.json"
SLOT_HOME="${MULTICA_SLOTS_HOME:-$HOME/.multica/slots}"
LOCK_DIR="$SLOT_HOME/.lock.d"
DEV_PROFILES_HOME="${MULTICA_DEV_PROFILES_HOME:-$HOME/.multica/profiles}"
DEV_DESKTOP_APP_DATA="${MULTICA_DEV_DESKTOP_APP_DATA:-}"

DEV_EMAIL="${MULTICA_DEV_EMAIL:-dev@localhost}"
DEV_CODE_DEFAULT=888888
WORKSPACE_NAME="${MULTICA_DEV_WORKSPACE_NAME:-Dev}"
WORKSPACE_SLUG="${MULTICA_DEV_WORKSPACE_SLUG:-dev}"
QA_TTL_HOURS="${MULTICA_DEV_QA_TTL_HOURS:-}"

DEV_TMPDIR="${MULTICA_DEV_TMPDIR:-$HOME/.multica/dev-tmp}"

ALL_COMPONENTS="api web mcp daemon desktop"
DEFAULT_COMPONENTS="api web mcp"

# The platform's main database and role on the shared instance. Slot tools
# never construct statements naming these; the constants back the assertions
# in scripts/dev-env.test.sh.
MAIN_DATABASE_NAME="multica"
MAIN_ROLE_NAME="multica"

# An agent runs with TMPDIR=/tmp/multica-task-<id>, deleted when the run ends.
# Anything the Go toolchain builds there goes with it, so a binary started from
# such a build stops being re-executable the moment its creator finishes.
#
# The agent runtime also exports MULTICA_* values pointing at PRODUCTION; the
# runtime's own MULTICA_CALLER_OWNER is consumed here (lease bookkeeping) and
# stripped from children so a local daemon cannot mistake a task-scoped owner
# for its own.
CLEAN_ENV=(env
  -u MULTICA_SERVER_URL -u MULTICA_TOKEN -u MULTICA_WORKSPACE_ID
  -u MULTICA_DAEMON_PORT -u MULTICA_AGENT_ID -u MULTICA_AGENT_NAME
  -u MULTICA_TASK_ID -u MULTICA_TASK_SLOT -u MULTICA_CALLER_OWNER
  -u MULTICA_TASK_CONFIG_ROOT -u MULTICA_TASK_WORKSPACES_ROOT
  -u MULTICA_WORKSPACES_ROOT)

# ---------------------------------------------------------------- output ----

if [ -t 1 ]; then
  C_BOLD=$'\033[1m'; C_GREEN=$'\033[32m'; C_RED=$'\033[31m'; C_DIM=$'\033[2m'; C_OFF=$'\033[0m'
else
  C_BOLD=""; C_GREEN=""; C_RED=""; C_DIM=""; C_OFF=""
fi

step() { printf '\n%s==> %s%s\n' "$C_BOLD" "$1" "$C_OFF"; }
info() { printf '    %s\n' "$1"; }
ok()   { printf '    %s✓%s %s\n' "$C_GREEN" "$C_OFF" "$1"; }
warn() { printf '    %s!%s %s\n' "$C_RED" "$C_OFF" "$1"; }
die()  { printf '\n%s✗ %s%s\n' "$C_RED" "$1" "$C_OFF" >&2; exit 1; }

json_escape() {
  printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' -e 's/	/\\t/g'
}

now_iso() { date -u '+%Y-%m-%dT%H:%M:%SZ'; }
now_epoch() { date -u '+%s'; }

expires_at_after_hours() {
  node -e '
    const hours = Number(process.argv[1]);
    process.stdout.write(new Date(Date.now() + hours * 3600_000).toISOString().replace(/\.\d{3}Z$/, "Z"));
  ' "$1"
}

random_hex() {
  node -e 'process.stdout.write(require("node:crypto").randomBytes(Number(process.argv[1])).toString("hex"))' "$1"
}

# ---------------------------------------------------------------- locking ---

# flock is not on a stock macOS, so the lock is an atomic mkdir. The holder's
# pid is recorded so a lock left behind by a killed process is recoverable
# instead of wedging every later command.
acquire_lock() {
  local waited=0
  mkdir -p "$SLOT_HOME"
  while ! mkdir "$LOCK_DIR" 2>/dev/null; do
    local holder=""
    holder="$(cat "$LOCK_DIR/pid" 2>/dev/null || true)"
    if [ -n "$holder" ] && ! kill -0 "$holder" 2>/dev/null; then
      rm -rf "$LOCK_DIR"
      continue
    fi
    waited=$((waited + 1))
    [ "$waited" -lt 100 ] || die "Timed out waiting for the slot allocation lock at $LOCK_DIR. If nothing else is running, remove it."
    sleep 0.1
  done
  printf '%s\n' "$$" > "$LOCK_DIR/pid"
}

release_lock() { rm -rf "$LOCK_DIR"; }

# ------------------------------------------------------------ slots.json ----

slot_field() {
  node -e '
    const fs = require("fs");
    const facts = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
    const slot = facts.slots.find(s => s.name === process.argv[2]);
    if (!slot) process.exit(2);
    const key = process.argv[3];
    const value = slot[key];
    if (value === undefined) process.exit(1);
    process.stdout.write(String(value));
  ' "$SLOTS_FILE" "$1" "$2"
}

shared_field() {
  node -e '
    const fs = require("fs");
    const facts = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
    const value = process.argv[2].split(".").reduce((acc, key) => (acc == null ? acc : acc[key]), facts);
    if (value === undefined) process.exit(1);
    if (Array.isArray(value)) {
      process.stdout.write(value.join("\n"));
    } else {
      process.stdout.write(String(value));
    }
  ' "$SLOTS_FILE" "$1"
}

expand_home() {
  case "$1" in
    "~"/*) printf '%s' "$HOME${1#\~}" ;;
    "~") printf '%s' "$HOME" ;;
    *) printf '%s' "$1" ;;
  esac
}

# Canonical rendering of the slot facts. The committed slots.json must equal
# this byte for byte; a hand-edited file is restored (the tool owns the file),
# which is what keeps a tampered fact source from steering SQL or ports.
render_slots_json() {
  printf '{\n'
  printf '  "shared_postgres": {\n'
  printf '    "endpoint": "localhost:5432",\n'
  printf '    "allowed_endpoints": ["localhost:5432", "127.0.0.1:5432", "[::1]:5432"],\n'
  printf '    "admin_container": "multica-postgres-1",\n'
  printf '    "admin_role": "multica",\n'
  printf '    "protected_databases": ["multica"],\n'
  printf '    "protected_roles": ["multica"]\n'
  printf '  },\n'
  printf '  "ttl_hours_qa": 24,\n'
  printf '  "resource_budget": {\n'
  printf '    "slot_cpus": 4,\n'
  printf '    "components": {\n'
  printf '      "api": {"memory_mb": 768},\n'
  printf '      "web": {"memory_mb": 8192},\n'
  printf '      "daemon": {"memory_mb": 256},\n'
  printf '      "desktop": {"memory_mb": 256},\n'
  printf '      "mcp": {"memory_mb": 256}\n'
  printf '    },\n'
  printf '    "shared_postgres": {"memory": "2g", "cpus": 4}\n'
  printf '  },\n'
  printf '  "slots": [\n'
  local first=1 n name row frontend renderer mcp cpuset
  for n in 1 2; do
    name="dev$n"
    [ "$first" = 1 ] || printf ',\n'
    first=0
    row=$((21800 + n)); frontend=$((13800 + n)); renderer=$((57800 + n)); mcp=$((13000 + n))
    cpuset="$(( (n - 1) * 4 ))-$(( n * 4 - 1 ))"
    printf '    {"name": "%s", "index": %s, "cpuset": "%s", "backend_port": %s, "frontend_port": %s, "desktop_renderer_port": %s, "mcp_port": %s, "database": "multica_%s", "account": "%s_app", "profile": "%s", "worktree_root": "~/.multica/slots/%s/worktrees", "workspaces_root": "~/.multica/slots/%s/workspaces", "desktop_app_suffix": "%s"}' \
      "$name" "$n" "$cpuset" "$row" "$frontend" "$renderer" "$mcp" "$name" "$name" "$name" "$name" "$name" "$name"
  done
  printf '\n  ]\n}\n'
}

ensure_slots_file() {
  local expected actual
  expected="$(render_slots_json)"
  if [ -f "$SLOTS_FILE" ]; then
    actual="$(cat "$SLOTS_FILE")"
    if [ "$actual" = "$expected" ]; then
      return 0
    fi
    warn "scripts/slots.json was hand-edited; restoring the canonical fact source."
  fi
  printf '%s\n' "$expected" > "$SLOTS_FILE"
}

# ------------------------------------------------------------- slot facts ---

valid_slot_name() {
  case "$1" in
    dev[12]) return 0 ;;
  esac
  return 1
}

require_slot() {
  local slot="${1:-}"
  valid_slot_name "$slot" || die "Unknown slot '$slot'. Valid slots: dev1, dev2 (fact source: scripts/slots.json)."
  SLOT="$slot"
  SLOT_INDEX="$(slot_field "$slot" index)"
  SLOT_CPUSET="$(slot_field "$slot" cpuset)"
  SLOT_DB="$(slot_field "$slot" database)"
  SLOT_ACCOUNT="$(slot_field "$slot" account)"
  SLOT_BACKEND_PORT="$(slot_field "$slot" backend_port)"
  SLOT_FRONTEND_PORT="$(slot_field "$slot" frontend_port)"
  SLOT_RENDERER_PORT="$(slot_field "$slot" desktop_renderer_port)"
  SLOT_MCP_PORT="$(slot_field "$slot" mcp_port)"
  SLOT_PROFILE="$(slot_field "$slot" profile)"
  # ~/-style roots from slots.json follow MULTICA_SLOTS_HOME: everything a slot
  # owns lives under one root, so an overridden home moves the worktrees and
  # workspaces too (and the orphan sweeps in qa-clean.sh keep covering them).
  SLOT_WORKTREE_ROOT="$(expand_home "$(slot_field "$slot" worktree_root)")"
  SLOT_WORKTREE_ROOT="${SLOT_WORKTREE_ROOT/#$HOME\/.multica\/slots/$SLOT_HOME}"
  SLOT_WORKSPACES_ROOT="$(expand_home "$(slot_field "$slot" workspaces_root)")"
  SLOT_WORKSPACES_ROOT="${SLOT_WORKSPACES_ROOT/#$HOME\/.multica\/slots/$SLOT_HOME}"
  SLOT_DESKTOP_SUFFIX="$(slot_field "$slot" desktop_app_suffix)"
  SLOT_DIR="$SLOT_HOME/$slot"
  SLOT_MANIFEST="$SLOT_DIR/manifest.env"
  SLOT_LOCK_FILE="$SLOT_DIR/.slot-lock"
  SLOT_ENV_FILE="$SLOT_DIR/env"
  load_shared_facts
}

load_shared_facts() {
  SLOT_PG_ENDPOINT="$(shared_field shared_postgres.endpoint)"
  SLOT_PG_CONTAINER="$(shared_field shared_postgres.admin_container)"
  SLOT_PG_ADMIN_ROLE="$(shared_field shared_postgres.admin_role)"
}

budget_field() {
  node -e '
    const fs = require("fs");
    const facts = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
    const value = ("resource_budget." + process.argv[2]).split(".").reduce((acc, key) => (acc == null ? acc : acc[key]), facts);
    if (value === undefined) process.exit(1);
    process.stdout.write(String(value));
  ' "$SLOTS_FILE" "$1"
}

# Per-component memory caps, emitted as KEY=VALUE pairs for the launch env.
# GOMEMLIMIT bounds the Go heap, GOMAXPROCS matches the slot CPU budget, and
# V8's old-space cap bounds next-server's heap — the process that grew to
# 11.5 GB RSS and OOM-killed the host on 2026-10-02. These are runtime
# self-limits; the slot watchdog enforces the hard behavioural ceiling.
component_resource_env() {
  local mb
  case "$1" in
    api)
      mb="$(budget_field components.api.memory_mb)" || return 0
      printf 'GOMEMLIMIT=%sMiB\n' "$mb"
      printf 'GOMAXPROCS=%s\n' "$(budget_field slot_cpus)"
      ;;
    web)
      mb="$(budget_field components.web.memory_mb)" || return 0
      printf 'NODE_OPTIONS=--max-old-space-size=%s\n' "$mb"
      ;;
    mcp)
      mb="$(budget_field components.mcp.memory_mb)" || return 0
      printf 'NODE_OPTIONS=--max-old-space-size=%s\n' "$mb"
      ;;
    daemon)
      mb="$(budget_field components.daemon.memory_mb)" || return 0
      printf 'GOMEMLIMIT=%sMiB\n' "$mb"
      ;;
    desktop)
      # Electron ignores GOMEMLIMIT/NODE_OPTIONS for its renderers; the CPU
      # pin and the watchdog are its budget enforcement.
      return 0
      ;;
  esac
}

slot_dir_exists() { [ -d "$SLOT_DIR" ]; }

# --------------------------------------------------------------- registry ---

write_manifest_value() {
  printf '%s=' "$1"
  printf '%q' "$2"
  printf '\n'
}

# A manifest only carries the keys that existed when it was written; defaults
# must stay safe for the destructive verbs: an empty DB_NAME never satisfies
# env_file_agrees_on_database, an empty DESKTOP_USER_DATA_DIR never satisfies
# the expected-path guard in destroy.
load_manifest() {
  [ -f "$SLOT_MANIFEST" ] || return 1
  # shellcheck disable=SC1090
  . "$SLOT_MANIFEST"
  [ "${NAME:-}" = "$SLOT" ] || die "Manifest $SLOT_MANIFEST declares NAME=${NAME:-}; expected $SLOT."
  : "${CREATED_AT:=}" "${ISSUE:=}" "${CODE_SOURCE:=}" "${CODE_SHA:=}" \
    "${OWNER:=unknown}" "${TTL_HOURS:=0}" "${EXPIRES_AT:=}" "${PHASE:=dev}" \
    "${DESKTOP_USER_DATA_DIR:=}" "${DESKTOP_ENV_FILE:=$DIR/apps/desktop/.env.development.local}"
  return 0
}

save_manifest() {
  {
    write_manifest_value NAME "$SLOT"
    write_manifest_value PHASE "${PHASE:-dev}"
    write_manifest_value ISSUE "$MANIFEST_ISSUE"
    write_manifest_value DIR "$DIR"
    write_manifest_value CODE_SOURCE "$CODE_SOURCE"
    write_manifest_value CODE_SHA "$CODE_SHA"
    write_manifest_value CREATED_AT "$CREATED_AT"
    write_manifest_value OWNER "$OWNER"
    write_manifest_value TTL_HOURS "$TTL_HOURS"
    write_manifest_value EXPIRES_AT "$EXPIRES_AT"
    write_manifest_value ENV_FILE "$SLOT_ENV_FILE"
    write_manifest_value BACKEND_PORT "$SLOT_BACKEND_PORT"
    write_manifest_value FRONTEND_PORT "$SLOT_FRONTEND_PORT"
    write_manifest_value DESKTOP_RENDERER_PORT "$SLOT_RENDERER_PORT"
    write_manifest_value DB_NAME "$SLOT_DB"
    write_manifest_value DB_ACCOUNT "$SLOT_ACCOUNT"
    write_manifest_value PROFILE "$SLOT_PROFILE"
    write_manifest_value WORKSPACES_ROOT "$SLOT_WORKSPACES_ROOT"
    write_manifest_value DESKTOP_APP_SUFFIX "$SLOT_DESKTOP_SUFFIX"
    write_manifest_value DESKTOP_USER_DATA_DIR "$(desktop_user_data_dir "$SLOT_DESKTOP_SUFFIX")"
    write_manifest_value DESKTOP_ENV_FILE "$DESKTOP_ENV_FILE"
  } > "$SLOT_MANIFEST"
  chmod 600 "$SLOT_MANIFEST"
}

# ------------------------------------------------------------------ lease ---

caller_owner() { printf '%s' "${MULTICA_CALLER_OWNER:-}"; }

# Write verbs are lease-gated fail-closed: without an issue identity nothing
# may allocate, start, stop or destroy a slot, and a slot held by another
# issue is off limits until lock-recover proves that issue left in_progress.
require_lease() {
  local owner
  owner="$(caller_owner)"
  [ -n "$owner" ] || die "MULTICA_CALLER_OWNER is not set. Write commands must name the owning issue, e.g. MULTICA_CALLER_OWNER=RUYI-333 scripts/dev-env.sh $SLOT up."

  local current="" phase=""
  if [ -f "$SLOT_LOCK_FILE" ]; then
    # shellcheck disable=SC1090
    . "$SLOT_LOCK_FILE"
    current="${OWNER_ISSUE:-}"
    phase="${PHASE:-dev}"
  elif [ -f "$SLOT_MANIFEST" ]; then
    # shellcheck disable=SC1090
    . "$SLOT_MANIFEST"
    current="${ISSUE:-}"
    # No live lease: fall back to the manifest's last known phase. gc writes
    # its own lease while collecting, so a guard-blocked destroy (RUYI-594)
    # leaves a `gc`-owned lock behind — the retry on the next cycle must see
    # the slot's real qa phase, not silently become a dev slot gc skips
    # forever.
    phase="${PHASE:-}"
  fi

  if [ -n "$current" ] && [ "$current" != "$owner" ]; then
    # gc collecting an expired slot runs destroy under the dead lease; that is
    # the one sanctioned bypass — everything else goes through lock-recover.
    if [ "${MULTICA_SLOT_GC_INTERNAL:-}" != "1" ]; then
      die "Slot $SLOT is held by issue $current (caller: $owner). Inspect with '$0 $SLOT lock-status'; take over with '$0 $SLOT lock-recover' once that issue is no longer in_progress."
    fi
  fi

  MANIFEST_ISSUE="$owner"
  OWNER="${OWNER_ROLE:-agent}"
  write_lease "$owner" "${phase:-${MULTICA_SLOT_PHASE:-dev}}"
}

write_lease() {
  local owner=$1 phase="${2:-dev}"
  mkdir -p "$SLOT_DIR"
  {
    write_manifest_value OWNER_ISSUE "$owner"
    write_manifest_value OWNER_ROLE "${OWNER_ROLE:-agent}"
    write_manifest_value PHASE "$phase"
    write_manifest_value ACQUIRED_AT "$(now_iso)"
  } > "$SLOT_LOCK_FILE"
}

# The slot's lifecycle phase: dev (development holds the slot) or qa (QA
# verification holds it). The lease file is authoritative; the manifest
# mirrors it for status/gc so a slot whose lease file was lost still reports
# its last known phase.
lease_phase() {
  local phase=""
  if [ -f "$SLOT_LOCK_FILE" ]; then
    # shellcheck disable=SC1090
    . "$SLOT_LOCK_FILE"
    phase="${PHASE:-}"
  fi
  if [ -z "$phase" ] && [ -f "$SLOT_MANIFEST" ]; then
    phase="$(sed -n 's/^PHASE=//p' "$SLOT_MANIFEST" | head -1)"
  fi
  printf '%s' "${phase:-dev}"
}

lease_field() {
  [ -f "$SLOT_LOCK_FILE" ] || return 1
  (
    # shellcheck disable=SC1090
    . "$SLOT_LOCK_FILE"
    case "$1" in
      OWNER_ISSUE) printf '%s' "${OWNER_ISSUE:-}" ;;
      OWNER_ROLE) printf '%s' "${OWNER_ROLE:-}" ;;
      PHASE) printf '%s' "${PHASE:-}" ;;
      ACQUIRED_AT) printf '%s' "${ACQUIRED_AT:-}" ;;
    esac
  )
}

# The takeover judgement reads the lease issue's status from the live platform:
# an in_progress issue still owns its slot, anything else (done, cancelled, or
# an issue the CLI cannot see) may be taken over by the next claimant.
issue_status() {
  command -v multica >/dev/null 2>&1 || return 1
  multica issue get "$1" --output json 2>/dev/null | node -e '
    let payload;
    try { payload = JSON.parse(require("fs").readFileSync(0, "utf8")); } catch { process.exit(1); }
    if (!payload.status) process.exit(1);
    process.stdout.write(payload.status);
  '
}

effective_lease_issue() {
  local issue
  issue="$(lease_field OWNER_ISSUE || true)"
  if [ -z "$issue" ] && [ -f "$SLOT_MANIFEST" ]; then
    issue="$(manifest_env_field ISSUE)"
  fi
  printf '%s' "$issue"
}

cmd_lock_status() {
  local issue role at phase
  issue="$(effective_lease_issue)"
  if [ -z "$issue" ]; then
    printf '%s: free (no lease, not registered)\n' "$SLOT"
    return 0
  fi
  role="$(lease_field OWNER_ROLE || printf 'unknown')"
  at="$(lease_field ACQUIRED_AT || printf 'unknown')"
  phase="$(lease_phase)"
  printf '%s: held by issue %s (phase %s, role %s, since %s)\n' "$SLOT" "$issue" "$phase" "$role" "$at"
}

# Atomic role handover on the lease: dev -> qa when development hands the slot
# to QA verification, qa -> dev to hand it back. The whole slot is held by one
# role at any moment; the transition runs under the slot allocation lock so
# two concurrent handoffs cannot interleave. Releasing (lock-release) ends the
# lifecycle from either phase.
cmd_handoff() {
  local to="" note=""
  while [ $# -gt 0 ]; do
    case "$1" in
      --to) to="${2:-}"; shift 2 ;;
      --note) note="${2:-}"; shift 2 ;;
      *) die "Unknown flag for handoff: $1" ;;
    esac
  done
  case "$to" in
    dev|qa) ;;
    *) die "handoff --to must be dev or qa (got '${to:-}')." ;;
  esac
  local owner
  owner="$(caller_owner)"
  [ -n "$owner" ] || die "MULTICA_CALLER_OWNER is not set. Handing a slot off must name the calling issue, e.g. MULTICA_CALLER_OWNER=RUYI-333 scripts/dev-env.sh $SLOT handoff --to qa."
  local current="" phase="dev"
  if [ -f "$SLOT_LOCK_FILE" ]; then
    # shellcheck disable=SC1090
    . "$SLOT_LOCK_FILE"
    current="${OWNER_ISSUE:-}"
    phase="${PHASE:-dev}"
  elif [ -f "$SLOT_MANIFEST" ]; then
    # shellcheck disable=SC1090
    . "$SLOT_MANIFEST"
    current="${ISSUE:-}"
  fi
  [ -n "$current" ] || die "Slot $SLOT is free. Acquire it with '$0 $SLOT use' before handing it off."
  if [ "$current" != "$owner" ]; then
    die "Slot $SLOT is held by issue $current (caller: $owner). Only the holding issue may hand the slot off."
  fi
  [ "$phase" != "$to" ] || die "Slot $SLOT is already in phase $to."

  acquire_lock
  write_lease "$owner" "$to"
  printf '%s %s %s->%s%s\n' "$(now_iso)" "$owner" "$phase" "$to" "$( [ -n "$note" ] && printf ' %s' "$note" )" \
    >> "$SLOT_DIR/lease-history.log"
  release_lock

  if [ -f "$SLOT_MANIFEST" ]; then
    load_manifest
    PHASE="$to"
    MANIFEST_ISSUE="$owner"
    if [ "$to" = qa ]; then
      TTL_HOURS="$(qa_ttl)"
      EXPIRES_AT="$(expires_at_after_hours "$TTL_HOURS")"
    else
      TTL_HOURS=0
      EXPIRES_AT=""
    fi
    save_manifest
  fi

  case "$to" in
    qa) ok "slot $SLOT handed to QA verification (phase qa, expires $EXPIRES_AT). Release it with '$0 $SLOT lock-release' when verification closes." ;;
    dev) ok "slot $SLOT handed back to development (phase dev)." ;;
  esac
}

cmd_lock_release() {
  local force=0
  while [ $# -gt 0 ]; do
    case "$1" in
      --force) force=1; shift ;;
      *) die "Unknown flag for lock-release: $1" ;;
    esac
  done
  local issue
  issue="$(effective_lease_issue)"
  [ -n "$issue" ] || die "Slot $SLOT has no lease to release."
  local owner
  owner="$(caller_owner)"
  [ -n "$owner" ] || die "MULTICA_CALLER_OWNER is not set. Releasing a lease must name the calling issue."
  if [ "$issue" != "$owner" ] && [ "$force" != 1 ]; then
    die "Slot $SLOT is held by $issue, not $owner. Re-run with --force after confirming the issue is closed."
  fi
  if [ "$force" != 1 ]; then
    local status
    if status="$(issue_status "$issue")"; then
      # A qa-phase holder closes its verification window the moment the check
      # ends — that is the whole point of the phase (dual-server test windows
      # hold BOTH slots for one issue, RUYI-431); waiting for issue closure
      # would pin the second slot for the issue's whole remaining lifetime.
      # The dev phase keeps the closure-only rule.
      if [ "$status" = "in_progress" ] && [ "$(lease_phase)" != "qa" ]; then
        die "Issue $issue is still in_progress; its slot cannot be released. Re-run with --force only after the issue is closed."
      fi
    else
      die "Cannot verify the status of issue $issue (multica CLI unavailable or issue unknown); refusing to release. Re-run with --force to override."
    fi
  fi
  rm -f "$SLOT_LOCK_FILE"
  # Closing the window clears the tenancy, not just the lease file: the
  # manifest's ISSUE/PHASE/TTL fields are the fallback every gate, takeover
  # and display reads once the lock file is gone, so leaving the old holder
  # behind kept released slots "held by issue" for everyone else (RUYI-431).
  if [ -f "$SLOT_MANIFEST" ]; then
    load_manifest || true
    MANIFEST_ISSUE=""
    PHASE="dev"
    TTL_HOURS=0
    EXPIRES_AT=""
    save_manifest
  fi
  ok "released lease on $SLOT (was: $issue)"
}

cmd_lock_recover() {
  local owner
  owner="$(caller_owner)"
  [ -n "$owner" ] || die "MULTICA_CALLER_OWNER is not set. Recovering a lease must name the calling issue."
  local issue
  issue="$(effective_lease_issue)"
  if [ -z "$issue" ] || [ "$issue" = "$owner" ]; then
    OWNER_ROLE="agent"
    write_lease "$owner" "$(lease_phase)"
    # Adopt the manifest too, or recovering a released slot would hold the
    # lease file while the manifest still reads free — the mirror image of
    # the residue this verb exists to clean up (RUYI-431).
    MANIFEST_ISSUE="$owner"
    if [ -f "$SLOT_MANIFEST" ]; then
      load_manifest || true
      save_manifest
    fi
    ok "lease on $SLOT is now held by $owner"
    return 0
  fi
  local status
  if ! status="$(issue_status "$issue")"; then
    die "Cannot verify the status of $issue (multica CLI unavailable or issue unknown); takeover refused."
  fi
  [ "$status" != "in_progress" ] || die "Issue $issue is still in_progress; takeover refused. It releases via lock-release at issue closure."
  OWNER_ROLE="agent"
  write_lease "$owner" dev
  MANIFEST_ISSUE="$owner"
  if [ -f "$SLOT_MANIFEST" ]; then
    load_manifest || true
    PHASE="dev"
    TTL_HOURS=0
    EXPIRES_AT=""
    save_manifest
  fi
  ok "took over $SLOT from $issue (status: $status) for $owner"
}

# ------------------------------------------------------------------ ports ---

# -sTCP:LISTEN matters: an unfiltered lsof also returns CLIENTS of the port, and
# the daemon holds a long-lived connection to the backend. Killing what the
# unfiltered lookup returns takes the daemon down with the server (#6573).
#
# The trailing `|| true` is load-bearing on macOS's bash 3.2: `x="$(fn)"`
# inside a function aborts the script under `set -e` when fn's last command
# fails, and "no process is listening" is the normal answer here, not an
# error. ss answers before lsof: Docker's overlay/netns mounts make lsof warn
# and return an INCOMPLETE fd set, missing listeners `ss` reports fine.
port_listener_pid() {
  local pid
  pid="$(ss -lntp "sport = :$1" 2>/dev/null | sed -n 's/.*pid=\([0-9]\{1,\}\).*/\1/p' | head -1 || true)"
  [ -n "$pid" ] || pid="$(lsof -nP -iTCP:"$1" -sTCP:LISTEN -t 2>/dev/null | head -1 || true)"
  printf '%s' "$pid"
}

port_free() { [ -z "$(port_listener_pid "$1")" ]; }

describe_port_owner() {
  local pid
  pid="$(port_listener_pid "$1")"
  [ -n "$pid" ] || { printf 'free'; return; }
  printf 'pid %s (%s), up %s' "$pid" \
    "$(ps -p "$pid" -o comm= 2>/dev/null | sed 's/^ *//' || echo unknown)" \
    "$(ps -p "$pid" -o etime= 2>/dev/null | sed 's/^ *//' || echo unknown)"
}

desktop_app_data_root() {
  if [ -n "$DEV_DESKTOP_APP_DATA" ]; then
    printf '%s' "$DEV_DESKTOP_APP_DATA"
    return 0
  fi
  case "$(uname -s)" in
    Darwin) printf '%s/Library/Application Support' "$HOME" ;;
    MINGW*|MSYS*|CYGWIN*) printf '%s' "${APPDATA:-$HOME/AppData/Roaming}" ;;
    *) printf '%s' "${XDG_CONFIG_HOME:-$HOME/.config}" ;;
  esac
}

desktop_user_data_dir() {
  printf '%s/Multica Canary %s' "$(desktop_app_data_root)" "$1"
}

# --------------------------------------------------------------- env file ---

# The slot env file is generated wholesale from slots.json — no field is ever
# inherited from a checkout's .env/.env.worktree, which is how the shared main
# database used to leak into per-issue environments (RUYI-300).
generate_slot_env() {
  local password="$1"
  {
    printf '# Managed by scripts/dev-env.sh for slot %s. Do not edit; regenerate via use/up.\n' "$SLOT"
    printf 'POSTGRES_DB=%s\n' "$SLOT_DB"
    printf 'POSTGRES_USER=%s\n' "$SLOT_ACCOUNT"
    printf 'POSTGRES_PASSWORD=%s\n' "$password"
    printf 'POSTGRES_PORT=5432\n'
    printf 'DATABASE_URL=postgres://%s:%s@%s/%s?sslmode=disable\n' "$SLOT_ACCOUNT" "$password" "$SLOT_PG_ENDPOINT" "$SLOT_DB"
    printf 'PORT=%s\n' "$SLOT_BACKEND_PORT"
    printf 'FRONTEND_PORT=%s\n' "$SLOT_FRONTEND_PORT"
    printf 'FRONTEND_ORIGIN=http://localhost:%s\n' "$SLOT_FRONTEND_PORT"
    printf 'MULTICA_SERVER_URL=ws://localhost:%s/ws\n' "$SLOT_BACKEND_PORT"
    printf 'MULTICA_PUBLIC_URL=http://localhost:%s\n' "$SLOT_BACKEND_PORT"
    printf 'MULTICA_APP_URL=http://localhost:%s\n' "$SLOT_FRONTEND_PORT"
    printf 'NEXT_PUBLIC_API_URL=http://localhost:%s\n' "$SLOT_BACKEND_PORT"
    printf 'NEXT_PUBLIC_WS_URL=ws://localhost:%s/ws\n' "$SLOT_BACKEND_PORT"
    printf 'MULTICA_DEV_VERIFICATION_CODE=%s\n' "$DEV_CODE_DEFAULT"
    printf 'MULTICA_DEV_EMAIL=%s\n' "$DEV_EMAIL"
    printf 'WORKSPACE_NAME=%s\n' "$WORKSPACE_NAME"
    printf 'WORKSPACE_SLUG=%s\n' "$WORKSPACE_SLUG"
    printf 'MCP_URL=http://localhost:%s\n' "$SLOT_MCP_PORT"
    printf 'MULTICA_MCP_PORT=%s\n' "$SLOT_MCP_PORT"
    # E2E fixture dependency (RUYI-632): e2e/agent-mcp.spec.ts asserts the
    # creator-only MCP Apps tab, which the composio_mcp_apps flag gates off by
    # default. Slots exist to run the E2E/dev surface, so the flag ships on.
    printf 'FF_COMPOSIO_MCP_APPS=true\n'
  } > "$SLOT_ENV_FILE"
  chmod 600 "$SLOT_ENV_FILE"
}

# `use` and `up` both funnel through here. A missing env file is generated
# fresh; a file written before MCP joined the slot facts (RUYI-428) is
# upgraded in place by appending the missing MCP lines — never regenerated,
# so the stored POSTGRES_PASSWORD, the one the shared instance's slot role was
# provisioned with, survives the upgrade untouched.
ensure_slot_env() {
  local password="$1" appended=""
  if [ ! -f "$SLOT_ENV_FILE" ]; then
    generate_slot_env "$password"
    ok "generated slot env $SLOT_ENV_FILE (${SLOT_ACCOUNT}@${SLOT_PG_ENDPOINT}/${SLOT_DB})"
    return 0
  fi
  if ! grep -q '^MCP_URL=' "$SLOT_ENV_FILE"; then
    printf 'MCP_URL=http://localhost:%s\n' "$SLOT_MCP_PORT" >> "$SLOT_ENV_FILE"
    appended="MCP_URL"
  fi
  if ! grep -q '^MULTICA_MCP_PORT=' "$SLOT_ENV_FILE"; then
    printf 'MULTICA_MCP_PORT=%s\n' "$SLOT_MCP_PORT" >> "$SLOT_ENV_FILE"
    appended="${appended:+$appended }MULTICA_MCP_PORT"
  fi
  if ! grep -q '^FF_COMPOSIO_MCP_APPS=' "$SLOT_ENV_FILE"; then
    printf 'FF_COMPOSIO_MCP_APPS=true\n' >> "$SLOT_ENV_FILE"
    appended="${appended:+$appended }FF_COMPOSIO_MCP_APPS"
  fi
  [ -z "$appended" ] || ok "upgraded slot env $SLOT_ENV_FILE (added $appended)"
}

env_file_field() {
  local key=$1
  [ -f "$SLOT_ENV_FILE" ] || return 1
  sed -n "s/^${key}=//p" "$SLOT_ENV_FILE" | head -n 1
}

database_name_from_url() {
  node -e '
    const url = new URL(process.argv[1]);
    if (url.protocol !== "postgres:" && url.protocol !== "postgresql:") process.exit(1);
    process.stdout.write(decodeURIComponent(url.pathname.replace(/^\//, "")));
  ' "$1" 2>/dev/null
}

database_endpoint_from_url() {
  node -e '
    const url = new URL(process.argv[1]);
    if (url.protocol !== "postgres:" && url.protocol !== "postgresql:") process.exit(1);
    process.stdout.write(`${url.hostname}:${url.port || "5432"}`);
  ' "$1" 2>/dev/null
}

database_user_from_url() {
  node -e '
    const url = new URL(process.argv[1]);
    if (url.protocol !== "postgres:" && url.protocol !== "postgresql:") process.exit(1);
    process.stdout.write(decodeURIComponent(url.username));
  ' "$1" 2>/dev/null
}

# Preflight, fail-closed: the env file the components are about to run from
# must name the shared instance endpoint, this slot's database and this slot's
# account — all three, read back from disk. Anything else (the main database,
# the superuser role, a remote host) is a structural no-go and refuses the
# start before a single process is launched.
preflight_env_identity() {
  local url endpoint db user allowed
  [ -f "$SLOT_ENV_FILE" ] || die "Slot env file $SLOT_ENV_FILE is missing; run '$0 $SLOT use' or '$0 $SLOT up' to generate it."
  url="$(env_file_field DATABASE_URL)"
  [ -n "$url" ] || die "Preflight failed: $SLOT_ENV_FILE has no DATABASE_URL."
  endpoint="$(database_endpoint_from_url "$url")" || die "Preflight failed: DATABASE_URL in $SLOT_ENV_FILE is not a PostgreSQL URL."
  db="$(database_name_from_url "$url")"
  user="$(database_user_from_url "$url")"

  while IFS= read -r allowed; do
    [ -n "$allowed" ] || continue
    [ "$endpoint" = "$allowed" ] && break
    allowed=""
  done <<EOF
$(shared_field shared_postgres.allowed_endpoints)
EOF
  [ -n "$allowed" ] || die "Preflight failed: DATABASE_URL endpoint $endpoint is not the shared slot instance ($SLOT_PG_ENDPOINT). Slots never talk to other hosts — refusing to start."

  [ "$db" = "$SLOT_DB" ] || die "Preflight failed: DATABASE_URL names database '$db', but slot $SLOT owns '$SLOT_DB'. Refusing to start against another database."
  [ "$user" = "$SLOT_ACCOUNT" ] || die "Preflight failed: DATABASE_URL connects as user '$user', but slot $SLOT owns account '$SLOT_ACCOUNT'. The main/superuser role is not a slot identity. Refusing to start."

  local declared_db declared_user
  declared_db="$(env_file_field POSTGRES_DB)"
  declared_user="$(env_file_field POSTGRES_USER)"
  [ "$declared_db" = "$SLOT_DB" ] || die "Preflight failed: POSTGRES_DB=$declared_db in $SLOT_ENV_FILE disagrees with slot database $SLOT_DB."
  [ "$declared_user" = "$SLOT_ACCOUNT" ] || die "Preflight failed: POSTGRES_USER=$declared_user in $SLOT_ENV_FILE disagrees with slot account $SLOT_ACCOUNT."
}

# The manifest records the same facts; if it disagrees with the fact source the
# slot's identity is ambiguous and every write path stays closed until destroy
# + use rebuild it.
preflight_manifest_consistency() {
  [ -f "$SLOT_MANIFEST" ] || return 0
  load_manifest || return 0
  [ "${DB_NAME:-}" = "$SLOT_DB" ] && [ "${DB_ACCOUNT:-}" = "$SLOT_ACCOUNT" ] \
    && [ "${BACKEND_PORT:-}" = "$SLOT_BACKEND_PORT" ] \
    && [ "${FRONTEND_PORT:-}" = "$SLOT_FRONTEND_PORT" ] \
    && [ "${DESKTOP_RENDERER_PORT:-}" = "$SLOT_RENDERER_PORT" ] \
    || die "Manifest $SLOT_MANIFEST disagrees with scripts/slots.json (database/account/ports). Run '$0 $SLOT destroy --yes' and '$0 $SLOT use' to rebuild the slot on the current facts."
}

# --------------------------------------------------------------- database ---

# Slot database administration runs inside the shared instance's container:
# the compose project fixes its name, and no host psql is required. Every
# statement below is built from slots.json facts — the guards ahead of each
# DROP/CREATE re-check the slot pattern so a corrupted fact source cannot
# steer SQL at the main database.
slot_name_guards() {
  [ "$SLOT_DB" = "$MAIN_DATABASE_NAME" ] && die "Guard: slot database may never be the main database '$MAIN_DATABASE_NAME'."
  [ "$SLOT_ACCOUNT" = "$MAIN_ROLE_NAME" ] && die "Guard: slot account may never be the main role '$MAIN_ROLE_NAME'."
  case "$SLOT_DB" in multica_dev[12]) ;; *) die "Guard: slot database '$SLOT_DB' does not match the slot pattern multica_dev[1-2]." ;; esac
  case "$SLOT_ACCOUNT" in dev[12]_app) ;; *) die "Guard: slot account '$SLOT_ACCOUNT' does not match the slot pattern dev[1-2]_app." ;; esac
}

db_admin_psql() {
  local target_db=$1
  shift
  docker exec -i "$SLOT_PG_CONTAINER" psql -U "$SLOT_PG_ADMIN_ROLE" -d "$target_db" "$@"
}

container_running() {
  [ "$(docker inspect -f '{{.State.Running}}' "$SLOT_PG_CONTAINER" 2>/dev/null || true)" = "true" ]
}

# The shared instance is the slot mechanism's infrastructure layer. `up` may
# bring it up via the repo's compose file when nothing at all is listening;
# it may never touch an instance it did not start.
ensure_shared_instance() {
  if container_running && db_admin_psql postgres -tAc 'SELECT 1' >/dev/null 2>&1; then
    return 0
  fi
  if ! container_running && ! port_free 5432; then
    die "Port 5432 is owned by $(describe_port_owner 5432), but container $SLOT_PG_CONTAINER is not running. Stop that process or start the shared instance deliberately; refusing to provision against an unknown server."
  fi
  info "Shared PostgreSQL container is not running; starting it via docker compose."
  docker compose -f "$REPO_ROOT/docker-compose.yml" up -d postgres
  local waited=0
  until db_admin_psql postgres -tAc 'SELECT 1' >/dev/null 2>&1; do
    waited=$((waited + 1))
    [ "$waited" -lt 30 ] || die "Shared PostgreSQL did not become ready within 30s."
    sleep 1
  done
}

# Idempotent provisioning of the slot role + database. The password in the
# slot env file is authoritative: an existing role is re-pointed at it, so a
# lost env file heals on the next up instead of stranding the slot.
provision_slot_database() {
  slot_name_guards
  local password
  password="$(env_file_field POSTGRES_PASSWORD)"
  [ -n "$password" ] || die "Slot env file has no POSTGRES_PASSWORD; regenerate with '$0 $SLOT use'."

  # CREATEDB (not CREATEROLE, not superuser): server integration tests create
  # throwaway databases at runtime (taskusagebackfill's migration-history
  # tests), so a slot account must be able to mint scratch databases of its
  # own. It still cannot open a session in, or alter, any database it does not
  # own. destroy collects anything the account left behind before dropping it.
  if [ "$(db_admin_psql postgres -tAc "SELECT 1 FROM pg_roles WHERE rolname='${SLOT_ACCOUNT}'")" = "1" ]; then
    db_admin_psql postgres -v ON_ERROR_STOP=1 -c "ALTER ROLE \"${SLOT_ACCOUNT}\" WITH LOGIN CREATEDB PASSWORD '${password}'" >/dev/null
    info "Reused slot account ${SLOT_ACCOUNT} (password re-synced from the slot env file)."
  else
    db_admin_psql postgres -v ON_ERROR_STOP=1 -c "CREATE ROLE \"${SLOT_ACCOUNT}\" WITH LOGIN CREATEDB PASSWORD '${password}'" >/dev/null
    info "Created slot account ${SLOT_ACCOUNT}."
  fi

  if [ "$(db_admin_psql postgres -tAc "SELECT 1 FROM pg_database WHERE datname='${SLOT_DB}'")" != "1" ]; then
    db_admin_psql postgres -v ON_ERROR_STOP=1 -c "CREATE DATABASE \"${SLOT_DB}\" OWNER \"${SLOT_ACCOUNT}\"" >/dev/null
    info "Created slot database ${SLOT_DB}."
  fi

  # Connection-level isolation between slots: PUBLIC loses CONNECT on this
  # slot's database, so another slot's account cannot even open a session
  # here. (The main database's ACL is intentionally untouched — that change
  # belongs to the shared-face stage and needs its own sign-off.)
  db_admin_psql "$SLOT_DB" -v ON_ERROR_STOP=1 -c "REVOKE CONNECT ON DATABASE \"${SLOT_DB}\" FROM PUBLIC" >/dev/null
  info "ACL: CONNECT on ${SLOT_DB} restricted to non-PUBLIC grantees (the owner)."
}

migrate_database() {
  # `migrate up` connects with the slot account's DATABASE_URL and pings
  # before doing anything, so a successful run is the proof that the
  # application can reach the database this script just provisioned.
  if ! (cd "$DIR/server" && go run ./cmd/migrate up) > "$LOG_DIR/migrate.log" 2>&1; then
    tail -5 "$LOG_DIR/migrate.log" | sed 's/^/    /' >&2 || true
    die "Migrations failed. Full log: $LOG_DIR/migrate.log"
  fi
}

database_state() {
  command -v docker >/dev/null 2>&1 || { printf 'unknown'; return; }
  container_running || { printf 'no-server'; return; }
  if [ "$(db_admin_psql postgres -tAc "SELECT 1 FROM pg_database WHERE datname='${SLOT_DB}'" 2>/dev/null)" = "1" ]; then
    printf 'present'
  else
    printf 'missing'
  fi
}

# Two sources before a drop (the RUYI-66 discipline, slot-shaped): the
# registry manifest AND the env file the application actually ran from must
# both name the slot database. The main database can never agree — it is not
# a slot database, and slot_name_guards refuses before any SQL is built.
env_file_agrees_on_database() {
  local declared url_db
  [ -f "$SLOT_ENV_FILE" ] || return 1
  declared="$(env_file_field POSTGRES_DB)"
  [ -n "$declared" ] || return 1
  url_db="$(database_name_from_url "$(env_file_field DATABASE_URL)")"
  [ -n "$url_db" ] || return 1
  [ "$declared" = "$SLOT_DB" ] && [ "$url_db" = "$SLOT_DB" ]
}

# -------------------------------------------------------------- components ---

component_selected() {
  case " $COMPONENTS " in *" $1 "*) return 0 ;; *) return 1 ;; esac
}

pid_file()  { printf '%s/%s.pid' "$SLOT_DIR" "$1"; }
listener_pid_file() { printf '%s/%s.listener.pid' "$SLOT_DIR" "$1"; }
log_file()  { printf '%s/%s.log' "$LOG_DIR" "$1"; }

component_pid() {
  local file
  file="$(pid_file "$1")"
  [ -f "$file" ] || return 1
  local pid
  pid="$(cat "$file")"
  kill -0 "$pid" 2>/dev/null || return 1
  printf '%s' "$pid"
}

# set -m puts the launcher in its own process group, so stopping can signal the
# whole tree (make → go run → server) with one kill, and the child's own
# `trap 'kill 0'` can never reach back into this shell.
#
# Every slot process is wrapped in the slot's resource budget: pinned to the
# slot's CPU set (affinity is inherited by the whole child tree) and tagged
# with MULTICA_SLOT/MULTICA_ISSUE so processes can be traced back to their
# slot/issue for the orphan sweep even after the manifest is gone.
launch_detached() {
  local name=$1
  shift
  local -a pin=()
  if [ -n "${SLOT_CPUSET:-}" ]; then
    if command -v taskset >/dev/null 2>&1 && taskset -c "$SLOT_CPUSET" true 2>/dev/null; then
      pin=(taskset -c "$SLOT_CPUSET")
    else
      warn "cpuset $SLOT_CPUSET is not usable on this host (offline CPUs?); starting $name without CPU pinning"
    fi
  fi
  (
    set -m
    nohup "${CLEAN_ENV[@]}" "${pin[@]}" env MULTICA_SLOT="$SLOT" MULTICA_ISSUE="${MANIFEST_ISSUE:-}" "$@" > "$(log_file "$name")" 2>&1 < /dev/null &
    printf '%s\n' "$!" > "$(pid_file "$name")"
  )
}

# Per-component resource env as command-prefix arguments.
resource_env_args() { # $1 = component -> fills RE_ARGS array
  local kv
  RE_ARGS=()
  while IFS= read -r kv; do
    [ -n "$kv" ] && RE_ARGS+=("$kv")
  done < <(component_resource_env "$1")
}

health_json() { curl -sf --max-time 3 "http://localhost:${SLOT_BACKEND_PORT}/health" 2>/dev/null; }

json_field() {
  node -e '
    let payload;
    try { payload = JSON.parse(process.argv[1]); } catch { process.exit(1); }
    const value = process.argv[2].split(".").reduce((acc, key) => (acc == null ? acc : acc[key]), payload);
    if (value === undefined || value === null || value === "") process.exit(1);
    process.stdout.write(String(value));
  ' "$1" "$2" 2>/dev/null
}

api_started_after() {
  local started_at epoch
  started_at="$(json_field "$1" started_at || true)"
  [ -n "$started_at" ] || return 1
  epoch="$(node -e 'process.stdout.write(String(Math.floor(Date.parse(process.argv[1]) / 1000)))' "$started_at" 2>/dev/null || echo 0)"
  [ "$epoch" -ge $(($2 - 5)) ]
}

checkout_commit() {
  git -C "${DIR:-$REPO_ROOT}" rev-parse --short HEAD 2>/dev/null || printf 'unknown'
}

process_group_id() {
  ps -p "$1" -o pgid= 2>/dev/null | tr -d ' ' || true
}

# Process group equality cannot prove ownership of a web listener: turbo starts
# each task in a NEW process group, so the chain is
#
#   make (launcher, pgid=launcher) → pnpm → turbo
#     → pnpm run dev (pgid=itself) → next → next-server (the listener)
#
# and the listener's pgid is turbo's task group, never the launcher's pid.
# Ancestry is what actually proves it, and it stays just as strict: a process
# that merely reused the port has no path up to this slot's launcher.
pid_has_ancestor() {
  local pid=$1 ancestor=$2 hops=0
  [ -n "$pid" ] && [ -n "$ancestor" ] || return 1
  while [ -n "$pid" ] && [ "$pid" != 0 ] && [ "$pid" != 1 ] && [ "$hops" -lt 32 ]; do
    [ "$pid" = "$ancestor" ] && return 0
    pid="$(ps -p "$pid" -o ppid= 2>/dev/null | tr -d ' ' || true)"
    hops=$((hops + 1))
  done
  return 1
}

listener_belongs_to_component() {
  local component=$1 port=$2 launcher listener recorded
  launcher="$(component_pid "$component" || true)"
  listener="$(port_listener_pid "$port")"
  [ -n "$launcher" ] && [ -n "$listener" ] || return 1
  recorded="$(cat "$(listener_pid_file "$component")" 2>/dev/null || true)"
  [ -n "$recorded" ] && [ "$listener" = "$recorded" ] && return 0
  pid_has_ancestor "$listener" "$launcher"
}

# Written once ownership has been proven, so `down` can identify the listener
# again without re-deriving ancestry from a tree that has since changed shape,
# and so the watchdog can accept an orphaned (re-parented) server it can no
# longer prove by ancestry alone. The watchdog samples the listener's CURRENT
# pid and process group live every tick — this file is a fallback identity
# proof, never the sampling target (RUYI-333 QA round 2: a snapshot goes
# stale the moment next-server re-groups after startup).
record_listener_pid() {
  local component=$1 port=$2 listener
  listener="$(port_listener_pid "$port")"
  [ -n "$listener" ] || return 0
  printf '%s\n' "$listener" > "$(listener_pid_file "$component")"
}

health_belongs_to_api() {
  local health=$1 health_pid listener
  health_pid="$(json_field "$health" pid || true)"
  listener="$(port_listener_pid "$SLOT_BACKEND_PORT")"
  [ -n "$health_pid" ] && [ "$health_pid" = "$listener" ] \
    && listener_belongs_to_component api "$SLOT_BACKEND_PORT"
}

api_identity_matches() {
  local health=$1 expected_commit=$2 launched_at=${3:-0} reported_commit started_at
  health_belongs_to_api "$health" || return 1
  reported_commit="$(json_field "$health" commit || true)"
  started_at="$(json_field "$health" started_at || true)"
  [ -n "$started_at" ] && [ "$reported_commit" = "$expected_commit" ] || return 1
  [ "$launched_at" = 0 ] || api_started_after "$health" "$launched_at"
}

start_api() {
  local launched_at health waited=0 expected_commit
  expected_commit="$(checkout_commit)"
  if health="$(health_json)" && [ -n "$health" ] && component_pid api >/dev/null; then
    if api_identity_matches "$health" "$expected_commit"; then
      ok "api already running on :$SLOT_BACKEND_PORT (pid $(json_field "$health" pid), commit $expected_commit)"
      return 0
    fi
    if health_belongs_to_api "$health"; then
      warn "api on :$SLOT_BACKEND_PORT is ours but not commit $expected_commit; restarting it."
      stop_component api
    else
      die "Port $SLOT_BACKEND_PORT answers /health, but its pid/commit does not match this slot. Refusing to reuse or kill it."
    fi
  fi
  if ! port_free "$SLOT_BACKEND_PORT"; then
    die "Port $SLOT_BACKEND_PORT is busy: $(describe_port_owner "$SLOT_BACKEND_PORT").
Stop the other process first — a leftover instance answers /health with 200 and you would test it instead of your build."
  fi

  launched_at="$(now_epoch)"
  resource_env_args api
  launch_detached api env "${RE_ARGS[@]}" make -C "$DIR" -s api-dev ENV_FILE="$SLOT_ENV_FILE"
  info "api launching (pid $(cat "$(pid_file api)")), log: $(log_file api)"

  while [ "$waited" -lt 300 ]; do
    health="$(health_json || true)"
    if [ -n "$health" ]; then
      # A 200 is not enough: pid, process group, commit and launch time all have
      # to identify the process this slot just started.
      if ! api_identity_matches "$health" "$expected_commit" "$launched_at"; then
        stop_component api
        die "Something else is serving :$SLOT_BACKEND_PORT, or the launched api did not report pid/commit/started_at for commit $expected_commit."
      fi
      record_listener_pid api "$SLOT_BACKEND_PORT"
      ok "api healthy at http://localhost:$SLOT_BACKEND_PORT (pid $(json_field "$health" pid), commit $expected_commit)"
      return 0
    fi
    component_pid api >/dev/null || { tail -20 "$(log_file api)" | sed 's/^/    /' >&2; die "api exited during startup. Log: $(log_file api)"; }
    sleep 2
    waited=$((waited + 2))
  done
  die "api never became healthy. Log: $(log_file api)"
}

start_web() {
  local waited=0 listener
  if curl -sf --max-time 15 "http://localhost:${SLOT_FRONTEND_PORT}" >/dev/null 2>&1 \
    && listener_belongs_to_component web "$SLOT_FRONTEND_PORT"; then
    ok "web already running on :$SLOT_FRONTEND_PORT"
    return 0
  fi
  if ! port_free "$SLOT_FRONTEND_PORT"; then
    die "Port $SLOT_FRONTEND_PORT is busy: $(describe_port_owner "$SLOT_FRONTEND_PORT"). Stop the other process first."
  fi

  resource_env_args web
  # MULTICA_WEB_MODE=release serves the prebuilt production bundle instead of
  # the dev server — the verifiable E2E entry (RUYI-632). The build must
  # exist before up; building is e2e-release-entry.sh's job, not the slot's.
  local web_target="web-dev"
  if [ "${MULTICA_WEB_MODE:-dev}" = "release" ]; then
    web_target="web-release"
    if [ ! -f "$DIR/apps/web/.next/BUILD_ID" ]; then
      die "MULTICA_WEB_MODE=release needs a production build first: bash scripts/e2e-release-entry.sh $SLOT build (runbook: e2e/README.md)"
    fi
    info "web mode: release (BUILD_ID $(cat "$DIR/apps/web/.next/BUILD_ID"))"
  fi
  launch_detached web env "${RE_ARGS[@]}" make -C "$DIR" -s "$web_target" ENV_FILE="$SLOT_ENV_FILE"
  info "web launching (pid $(cat "$(pid_file web)")), log: $(log_file web)"

  while [ "$waited" -lt 300 ]; do
    if curl -sf --max-time 15 "http://localhost:${SLOT_FRONTEND_PORT}" >/dev/null 2>&1; then
      listener="$(port_listener_pid "$SLOT_FRONTEND_PORT")"
      if ! listener_belongs_to_component web "$SLOT_FRONTEND_PORT"; then
        stop_component web
        die "Web on :$SLOT_FRONTEND_PORT is not owned by the process group this slot launched."
      fi
      record_listener_pid web "$SLOT_FRONTEND_PORT"
      ok "web serving http://localhost:$SLOT_FRONTEND_PORT (pid ${listener:-?})"
      return 0
    fi
    component_pid web >/dev/null || { tail -20 "$(log_file web)" | sed 's/^/    /' >&2; die "web exited during startup. Log: $(log_file web)"; }
    sleep 2
    waited=$((waited + 2))
  done
  die "web never came up. Log: $(log_file web)"
}

# The MCP Node process (apps/mcp) behind the dev web's /api/mcp rewrite
# (RUYI-428). Stateless: every request carries its own PAT, so startup needs
# no token — only the backend REST origin, which the mcp-dev Makefile target
# passes as --server-url (overriding the daemon-shaped ws://.../ws
# MULTICA_SERVER_URL in the slot env) and the public site root for 401
# resource_metadata (MULTICA_APP_URL, already in the slot env). Loopback-only,
# on the slot's fixed mcp_port from slots.json.
start_mcp() {
  local waited=0 listener
  if curl -sf --max-time 3 "http://localhost:${SLOT_MCP_PORT}/healthz" >/dev/null 2>&1 \
    && listener_belongs_to_component mcp "$SLOT_MCP_PORT"; then
    ok "mcp already running on :$SLOT_MCP_PORT"
    return 0
  fi
  if ! port_free "$SLOT_MCP_PORT"; then
    die "Port $SLOT_MCP_PORT is busy: $(describe_port_owner "$SLOT_MCP_PORT"). Stop the other process first."
  fi

  (cd "$DIR" && "${CLEAN_ENV[@]}" pnpm --filter @multica/mcp build) > "$(log_file mcp-build)" 2>&1 \
    || { tail -20 "$(log_file mcp-build)" | sed 's/^/    /' >&2; die "mcp build failed. Log: $(log_file mcp-build)"; }
  resource_env_args mcp
  launch_detached mcp env "${RE_ARGS[@]}" make -C "$DIR" -s mcp-dev ENV_FILE="$SLOT_ENV_FILE"
  info "mcp launching (pid $(cat "$(pid_file mcp)")), log: $(log_file mcp)"

  while [ "$waited" -lt 60 ]; do
    if curl -sf --max-time 3 "http://localhost:${SLOT_MCP_PORT}/healthz" >/dev/null 2>&1; then
      listener="$(port_listener_pid "$SLOT_MCP_PORT")"
      if ! listener_belongs_to_component mcp "$SLOT_MCP_PORT"; then
        stop_component mcp
        die "MCP on :$SLOT_MCP_PORT is not owned by the process group this slot launched."
      fi
      record_listener_pid mcp "$SLOT_MCP_PORT"
      ok "mcp serving http://localhost:$SLOT_MCP_PORT (pid ${listener:-?})"
      return 0
    fi
    component_pid mcp >/dev/null || { tail -20 "$(log_file mcp)" | sed 's/^/    /' >&2; die "mcp exited during startup. Log: $(log_file mcp)"; }
    sleep 2
    waited=$((waited + 2))
  done
  die "mcp never came up. Log: $(log_file mcp)"
}

# send-code once, verify-code once. Repeated verify attempts lock the code out
# and start returning 400 even when it is correct, so retrying is self-defeating.
write_profile_config() {
  local config=$1 pat=$2 ws=$3
  mkdir -p "$PROFILE_DIR"
  cat > "$config" <<EOF
{
  "server_url": "http://localhost:${SLOT_BACKEND_PORT}",
  "app_url": "http://localhost:${SLOT_FRONTEND_PORT}",
  "token": "$(json_escape "$pat")",
  "workspace_id": "$(json_escape "$ws")",
  "workspaces_root": "$(json_escape "$SLOT_WORKSPACES_ROOT")"
}
EOF
  chmod 600 "$config"
}

ensure_credentials() {
  local server="http://localhost:${SLOT_BACKEND_PORT}" config="$PROFILE_DIR/config.json"
  local code="${MULTICA_DEV_VERIFICATION_CODE:-$DEV_CODE_DEFAULT}"
  local verify jwt pat ws

  if [ -f "$config" ]; then
    pat="$(json_field "$(cat "$config")" token || true)"
    ws="$(json_field "$(cat "$config")" workspace_id || true)"
    if [ -n "$pat" ] && curl -sf --max-time 5 "$server/api/me" -H "Authorization: Bearer $pat" >/dev/null 2>&1; then
      WORKSPACE_ID="$ws"
      write_profile_config "$config" "$pat" "$ws"
      ok "CLI profile $SLOT_PROFILE already authenticated"
      return 0
    fi
  fi

  curl -sf -X POST "$server/auth/send-code" -H 'Content-Type: application/json' \
    -d "{\"email\":\"${DEV_EMAIL}\"}" >/dev/null \
    || die "send-code failed. Is MULTICA_DEV_VERIFICATION_CODE set and APP_ENV non-production?"

  verify="$(curl -sS -X POST "$server/auth/verify-code" -H 'Content-Type: application/json' \
    -d "{\"email\":\"${DEV_EMAIL}\",\"code\":\"${code}\"}")"
  jwt="$(json_field "$verify" token || true)"
  [ -n "$jwt" ] || die "verify-code failed: $verify
Do not retry immediately — repeated attempts lock the code. Wait ~40s and re-run."

  local pat_response
  pat_response="$(curl -sS -X POST "$server/api/tokens" -H "Authorization: Bearer $jwt" \
    -H 'Content-Type: application/json' -d '{"name":"dev-env","expires_in_days":365}')"
  pat="$(json_field "$pat_response" token || true)"
  [ -n "$pat" ] || die "Personal access token creation failed: $pat_response"

  local ws_response
  ws_response="$(curl -sS -X POST "$server/api/workspaces" -H "Authorization: Bearer $pat" \
    -H 'Content-Type: application/json' \
    -d "{\"name\":\"${WORKSPACE_NAME}\",\"slug\":\"${WORKSPACE_SLUG}\"}")"
  ws="$(json_field "$ws_response" id || true)"
  if [ -z "$ws" ]; then
    ws="$(node -e '
      let list;
      try { list = JSON.parse(process.argv[1]); } catch { process.exit(1); }
      const match = (Array.isArray(list) ? list : []).find(w => w.slug === process.argv[2]);
      if (!match) process.exit(1);
      process.stdout.write(match.id);
    ' "$(curl -sS "$server/api/workspaces" -H "Authorization: Bearer $pat")" "$WORKSPACE_SLUG" 2>/dev/null || true)"
  fi
  [ -n "$ws" ] || die "Workspace creation failed: $ws_response"

  # A fresh user has onboarded_at = NULL and a browser login is bounced to
  # /onboarding, so the URL this script prints would not land in the app.
  curl -sS -X POST "$server/api/me/onboarding/complete" \
    -H "Authorization: Bearer $pat" -H "X-Workspace-ID: $ws" \
    -H 'Content-Type: application/json' -d '{"exit":"existing"}' >/dev/null 2>&1 || true

  write_profile_config "$config" "$pat" "$ws"
  WORKSPACE_ID="$ws"
  ok "Logged in as $DEV_EMAIL and wrote profile $SLOT_PROFILE"
}

# The CLI refuses `daemon start` anywhere under a daemon-task marker, so a task
# cannot spawn a second daemon that competes for its own work. Checking the
# marker here turns that refusal into one actionable line, before this
# component has spent a login and a CLI build on an outcome it cannot reach.
daemon_task_marker() {
  local dir="${DIR:-$REPO_ROOT}" marker
  while :; do
    marker="$dir/.multica/daemon_task_context.json"
    if [ -f "$marker" ] && grep -q 'multica-daemon-task' "$marker" 2>/dev/null; then
      printf '%s' "$marker"
      return 0
    fi
    [ "$dir" != "/" ] || break
    dir="$(dirname "$dir")"
  done
  return 0
}

start_daemon() {
  local status state
  ensure_credentials

  # Built, never `go run`: the daemon records its own executable path at startup
  # and re-execs it as the execution-environment helper for every task. Under
  # `go run` the toolchain deletes that binary when the launcher exits, so the
  # daemon registers, heartbeats, and then fails every task with
  # "fork/exec .../go-build.../exe/multica: no such file or directory".
  info "Building the multica CLI (a go run daemon would fail every task later)."
  (cd "$DIR/server" && go build -o bin/multica ./cmd/multica) || die "Failed to build the multica CLI."
  MULTICA_BIN="$DIR/server/bin/multica"

  resource_env_args daemon
  # RUYI-606: the slot daemon gets its own supervisor run store. The shared
  # default (~/.multica/supervisor-runs) is where production workers live;
  # enumerating them at startup is the RUYI-592 cross-daemon kill blind spot.
  "${CLEAN_ENV[@]}" MULTICA_WORKSPACES_ROOT="$SLOT_WORKSPACES_ROOT" \
    MULTICA_SUPERVISOR_RUNS_DIR="$SLOT_DIR/supervisor-runs" \
    "${RE_ARGS[@]}" "$MULTICA_BIN" daemon start --profile "$SLOT_PROFILE" 2>&1 | sed 's/^/    /' || true

  status="$("${CLEAN_ENV[@]}" MULTICA_WORKSPACES_ROOT="$SLOT_WORKSPACES_ROOT" \
    "$MULTICA_BIN" daemon status --profile "$SLOT_PROFILE" --output json 2>/dev/null || true)"
  state="$(json_field "$status" status || echo unknown)"
  # `daemon status` reports "stopped" plus port_conflict when the daemon
  # answering this profile's health port belongs to another profile, so a
  # collision can never be read here as a healthy daemon.
  if [ "$state" != running ]; then
    if [ -n "$(json_field "$status" port_conflict.profile || true)" ]; then
      die "The health port for $SLOT_PROFILE is served by profile $(json_field "$status" port_conflict.profile)."
    fi
    die "Daemon is '$state' after start. Log: $PROFILE_DIR/daemon.log"
  fi
  ok "daemon running for profile $SLOT_PROFILE (pid $(json_field "$status" pid || echo '?'))"
}

start_desktop() {
  local waited=0 listener stable_listener
  DESKTOP_ENV_FILE="$DIR/apps/desktop/.env.development.local"
  if component_pid desktop >/dev/null \
    && curl -sf --max-time 10 "http://localhost:${SLOT_RENDERER_PORT}" >/dev/null 2>&1 \
    && listener_belongs_to_component desktop "$SLOT_RENDERER_PORT" \
    && desktop_env_matches; then
      ok "desktop already running (pid $(component_pid desktop), renderer :$SLOT_RENDERER_PORT)"
      return 0
  fi
  if component_pid desktop >/dev/null; then
    warn "desktop launcher exists but its renderer/backend identity is stale; restarting it."
    stop_component desktop
  fi
  if ! port_free "$SLOT_RENDERER_PORT"; then
    die "Desktop renderer port $SLOT_RENDERER_PORT is busy: $(describe_port_owner "$SLOT_RENDERER_PORT")."
  fi

  # The marker makes destroy remove only a file this tool owns. Explicit
  # renderer/app values bind Desktop to the slot facts rather than
  # independently hashing the checkout path again.
  cat > "$DESKTOP_ENV_FILE" <<EOF
# Managed by scripts/dev-env.sh for slot ${SLOT}.
VITE_API_URL=http://localhost:${SLOT_BACKEND_PORT}
VITE_WS_URL=ws://localhost:${SLOT_BACKEND_PORT}/ws
EOF
  launch_detached desktop env \
    DESKTOP_RENDERER_PORT="$SLOT_RENDERER_PORT" DESKTOP_APP_SUFFIX="$SLOT_DESKTOP_SUFFIX" \
    make -C "$DIR" -s desktop-dev ENV_FILE="$SLOT_ENV_FILE"

  while [ "$waited" -lt 300 ]; do
    if curl -sf --max-time 10 "http://localhost:${SLOT_RENDERER_PORT}" >/dev/null 2>&1; then
      listener="$(port_listener_pid "$SLOT_RENDERER_PORT")"
      if ! component_pid desktop >/dev/null || [ -z "$listener" ] || ! desktop_env_matches; then
        stop_component desktop
        die "Desktop renderer on :$SLOT_RENDERER_PORT does not belong to this slot."
      fi
      # Electron can bring Vite up and then crash during renderer bootstrap.
      # Require a short stable window before the slot claims readiness.
      sleep 5
      stable_listener="$(port_listener_pid "$SLOT_RENDERER_PORT")"
      if ! component_pid desktop >/dev/null || [ "$stable_listener" != "$listener" ] \
        || ! curl -sf --max-time 10 "http://localhost:${SLOT_RENDERER_PORT}" >/dev/null 2>&1; then
        tail -20 "$(log_file desktop)" | sed 's/^/    /' >&2 || true
        stop_component desktop
        die "desktop exited during renderer bootstrap. Log: $(log_file desktop)"
      fi
      printf '%s\n' "$listener" > "$(listener_pid_file desktop)"
      ok "desktop ready (launcher $(component_pid desktop), renderer pid ${listener:-?}, backend :$SLOT_BACKEND_PORT)"
      return 0
    fi
    component_pid desktop >/dev/null \
      || { tail -20 "$(log_file desktop)" | sed 's/^/    /' >&2; die "desktop exited during startup. Log: $(log_file desktop)"; }
    sleep 2
    waited=$((waited + 2))
  done
  stop_component desktop
  die "desktop renderer never became ready on :$SLOT_RENDERER_PORT. Log: $(log_file desktop)"
}

desktop_env_matches() {
  [ -f "$DESKTOP_ENV_FILE" ] \
    && grep -Fqx "# Managed by scripts/dev-env.sh for slot ${SLOT}." "$DESKTOP_ENV_FILE" \
    && grep -Fqx "VITE_API_URL=http://localhost:${SLOT_BACKEND_PORT}" "$DESKTOP_ENV_FILE" \
    && grep -Fqx "VITE_WS_URL=ws://localhost:${SLOT_BACKEND_PORT}/ws" "$DESKTOP_ENV_FILE"
}

stop_component() {
  local name=$1 pid launcher="" status state recorded_listener=""
  case "$name" in
    daemon)
      if [ -x "${MULTICA_BIN:-}" ]; then
        if "${CLEAN_ENV[@]}" MULTICA_WORKSPACES_ROOT="$SLOT_WORKSPACES_ROOT" \
          "$MULTICA_BIN" daemon stop --profile "$SLOT_PROFILE" >/dev/null 2>&1; then
          ok "daemon stopped"
        else
          status="$("${CLEAN_ENV[@]}" MULTICA_WORKSPACES_ROOT="$SLOT_WORKSPACES_ROOT" \
            "$MULTICA_BIN" daemon status --profile "$SLOT_PROFILE" --output json 2>/dev/null || true)"
          state="$(json_field "$status" status || echo stopped)"
          if [ "$state" = running ]; then
            warn "daemon for profile $SLOT_PROFILE is still running"
            return 1
          fi
          info "daemon was not running"
        fi
      else
        pid="$(cat "$PROFILE_DIR/daemon.pid" 2>/dev/null || true)"
        if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
          warn "cannot stop daemon pid $pid because no usable multica binary was found"
          return 1
        fi
        info "daemon skipped (no usable binary and no live profile pid)"
      fi
      return 0
      ;;
  esac

  local port=""
  case "$name" in
    api) port="$SLOT_BACKEND_PORT" ;;
    web) port="$SLOT_FRONTEND_PORT" ;;
    desktop) port="$SLOT_RENDERER_PORT" ;;
    mcp) port="$SLOT_MCP_PORT" ;;
  esac

  recorded_listener="$(cat "$(listener_pid_file "$name")" 2>/dev/null || true)"
  pid="$(component_pid "$name" || true)"

  # Ownership has to be established while the launcher is still alive: ancestry
  # is the only proof of it, and the intermediate processes that carry that proof
  # are gone the moment the launcher's process group dies.
  if [ -z "$recorded_listener" ] && [ -n "$pid" ] && [ -n "$port" ]; then
    local found
    found="$(port_listener_pid "$port")"
    if [ -n "$found" ] && pid_has_ancestor "$found" "$pid"; then
      recorded_listener="$found"
    fi
  fi

  if [ -n "$pid" ]; then
    launcher="$pid"
    # Negative pid targets the process group, so make → go run → server all go
    # down together instead of leaving the real listener orphaned.
    kill -TERM -"$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null || true
    sleep 1
    if kill -0 "$pid" 2>/dev/null; then
      kill -KILL -"$pid" 2>/dev/null || kill -KILL "$pid" 2>/dev/null || true
      sleep 1
    fi
    if kill -0 "$pid" 2>/dev/null; then
      warn "$name launcher pid $pid is still running"
      return 1
    fi
    rm -f "$(pid_file "$name")"
    ok "$name stopped (pid $pid)"
  else
    rm -f "$(pid_file "$name")"
    info "$name was not running"
  fi

  # A process group kill misses the listener whenever it does not share the
  # launcher's group — which for web is always, because turbo puts each task in
  # its own group. Only kill the listener when it was proven to belong to this
  # slot; a stale manifest must never kill an unrelated process that later
  # reused the port.
  if [ -n "$port" ]; then
    local listener listener_pgid
    listener="$(port_listener_pid "$port")"
    if [ -n "$listener" ]; then
      if { [ -n "$recorded_listener" ] && [ "$listener" = "$recorded_listener" ]; } \
        || { [ -n "$launcher" ] && pid_has_ancestor "$listener" "$launcher"; }; then
        # The listener's own group holds the rest of turbo's task subtree (pnpm,
        # next). Signalling the group is what keeps those from being orphaned;
        # it is only safe because this branch has already proven the listener is
        # ours, and the group it leads was created for this task alone.
        listener_pgid="$(process_group_id "$listener")"
        [ -n "$listener_pgid" ] && [ "$listener_pgid" != "$launcher" ] \
          && kill -TERM -"$listener_pgid" 2>/dev/null || true
        kill -TERM "$listener" 2>/dev/null || true
        sleep 1
        if kill -0 "$listener" 2>/dev/null; then
          [ -n "$listener_pgid" ] && [ "$listener_pgid" != "$launcher" ] \
            && kill -KILL -"$listener_pgid" 2>/dev/null || true
          kill -KILL "$listener" 2>/dev/null || true
          sleep 1
        fi
        if kill -0 "$listener" 2>/dev/null; then
          warn "$name listener pid $listener is still running"
          return 1
        fi
        info "released :$port (pid $listener)"
      else
        warn "left :$port alone: listener pid $listener is not owned by this slot"
      fi
    fi
  fi
  rm -f "$(listener_pid_file "$name")"
}

# ------------------------------------------------------------- watchdog ---

# The per-slot resource watchdog (scripts/slot-watchdog.sh) samples every
# component's process-tree RSS against the budgets in slots.json and kills a
# component that outgrows its quota — the guardrail that keeps a runaway
# next-server from repeating the 2026-10-02 host OOM. It is started by `up`,
# tagged with MULTICA_SLOT (so the orphan sweep finds it), and stopped by
# down/destroy via the stop file plus a process-group kill.
start_watchdog() {
  local pid
  if pid="$(component_pid watchdog 2>/dev/null)"; then
    ok "resource watchdog already running (pid $pid)"
    return 0
  fi
  rm -f "$SLOT_DIR/watchdog.stop"
  launch_detached watchdog bash "$REPO_ROOT/scripts/slot-watchdog.sh" \
    "$SLOT" "$SLOT_DIR" "$SLOTS_FILE"
  sleep 0.3
  if pid="$(component_pid watchdog 2>/dev/null)"; then
    ok "resource watchdog running (pid $pid, budget: api $(budget_field components.api.memory_mb)MB / web $(budget_field components.web.memory_mb)MB / others $(budget_field components.daemon.memory_mb)MB)"
  else
    warn "resource watchdog did not stay up; log: $(log_file watchdog)"
    return 1
  fi
}

stop_watchdog() {
  local pid
  [ -f "$SLOT_DIR/watchdog.stop" ] || : > "$SLOT_DIR/watchdog.stop" 2>/dev/null || true
  pid="$(component_pid watchdog || true)"
  if [ -n "$pid" ]; then
    kill -TERM -"$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null || true
    sleep 0.5
    kill -0 "$pid" 2>/dev/null && { kill -KILL -"$pid" 2>/dev/null || kill -KILL "$pid" 2>/dev/null || true; }
    rm -f "$(pid_file watchdog)"
    ok "resource watchdog stopped (pid $pid)"
  else
    rm -f "$(pid_file watchdog)"
  fi
}

# ------------------------------------------------------------------ status ---

component_state() {
  case "$1" in
    api)
      local health
      health="$(health_json || true)"
      if [ -n "$health" ] && api_identity_matches "$health" "$(checkout_commit)"; then
        printf 'running|http://localhost:%s|pid %s commit %s started %s' "$SLOT_BACKEND_PORT" \
          "$(json_field "$health" pid || echo '?')" \
          "$(json_field "$health" commit || echo '?')" \
          "$(json_field "$health" started_at || echo '?')"
      elif [ -n "$health" ]; then
        printf 'mismatch|http://localhost:%s|health responder is not this checkout/process' "$SLOT_BACKEND_PORT"
      else
        printf 'stopped|http://localhost:%s|' "$SLOT_BACKEND_PORT"
      fi
      ;;
    web)
      if curl -sf --max-time 10 "http://localhost:${SLOT_FRONTEND_PORT}" >/dev/null 2>&1 \
        && listener_belongs_to_component web "$SLOT_FRONTEND_PORT"; then
        printf 'running|http://localhost:%s|pid %s' "$SLOT_FRONTEND_PORT" "$(port_listener_pid "$SLOT_FRONTEND_PORT")"
      elif [ -n "$(port_listener_pid "$SLOT_FRONTEND_PORT")" ]; then
        printf 'mismatch|http://localhost:%s|listener is not owned by this slot' "$SLOT_FRONTEND_PORT"
      else
        printf 'stopped|http://localhost:%s|' "$SLOT_FRONTEND_PORT"
      fi
      ;;
    mcp)
      if curl -sf --max-time 3 "http://localhost:${SLOT_MCP_PORT}/healthz" >/dev/null 2>&1 \
        && listener_belongs_to_component mcp "$SLOT_MCP_PORT"; then
        printf 'running|http://localhost:%s|pid %s' "$SLOT_MCP_PORT" "$(port_listener_pid "$SLOT_MCP_PORT")"
      elif [ -n "$(port_listener_pid "$SLOT_MCP_PORT")" ]; then
        printf 'mismatch|http://localhost:%s|listener is not owned by this slot' "$SLOT_MCP_PORT"
      else
        printf 'stopped|http://localhost:%s|' "$SLOT_MCP_PORT"
      fi
      ;;
    daemon)
      local status state
      if [ -x "${MULTICA_BIN:-}" ]; then
        status="$("${CLEAN_ENV[@]}" MULTICA_WORKSPACES_ROOT="$SLOT_WORKSPACES_ROOT" \
          "$MULTICA_BIN" daemon status --profile "$SLOT_PROFILE" --output json 2>/dev/null || true)"
        state="$(json_field "$status" status || echo stopped)"
        printf '%s|%s|pid %s' "$state" "$SLOT_PROFILE" "$(json_field "$status" pid || echo '-')"
      else
        printf 'stopped|%s|not built' "$SLOT_PROFILE"
      fi
      ;;
    desktop)
      local pid
      pid="$(component_pid desktop || true)"
      if [ -n "$pid" ] \
        && curl -sf --max-time 10 "http://localhost:${SLOT_RENDERER_PORT}" >/dev/null 2>&1 \
        && listener_belongs_to_component desktop "$SLOT_RENDERER_PORT" \
        && desktop_env_matches; then
        printf 'running|http://localhost:%s|launcher %s renderer %s' \
          "$SLOT_RENDERER_PORT" "$pid" "$(port_listener_pid "$SLOT_RENDERER_PORT")"
      elif [ -n "$pid" ] || [ -n "$(port_listener_pid "$SLOT_RENDERER_PORT")" ]; then
        printf 'mismatch|http://localhost:%s|renderer/backend identity does not match' "$SLOT_RENDERER_PORT"
      else
        printf 'stopped|http://localhost:%s|' "$SLOT_RENDERER_PORT"
      fi
      ;;
  esac
}

masked_database_url() {
  sed -E 's#(postgres://[^:/@]+):[^@]*@#\1:***@#' <<<"${DATABASE_URL:-}"
}

print_status_human() {
  local comp state url detail row
  printf '\n%s%s%s  %s%s%s\n' "$C_BOLD" "$SLOT" "$C_OFF" "$C_DIM" "$DIR" "$C_OFF"
  printf '  %-9s %-9s %-32s %s\n' COMPONENT STATE ADDRESS DETAIL
  for comp in $ALL_COMPONENTS; do
    row="$(component_state "$comp")"
    state="${row%%|*}"; row="${row#*|}"
    url="${row%%|*}"; detail="${row#*|}"
    printf '  %-9s %-9s %-32s %s\n' "$comp" "$state" "${url:--}" "${detail:--}"
  done
  printf '  %-9s %-9s %-32s %s\n' database "$(database_state)" "$SLOT_DB" "$(masked_database_url)"
  printf '\n  issue %s · owner %s · phase %s · created %s · code %s%s\n' \
    "${ISSUE:-?}" "$OWNER" "$(lease_phase)" "$CREATED_AT" "$CODE_SHA" "$( [ "${TTL_HOURS:-0}" != 0 ] && printf ' · expires %s' "$EXPIRES_AT" )"
  printf '  lease %s\n' "$(effective_lease_issue)"
  printf '  logs  %s\n' "$LOG_DIR"
}

print_status_json() {
  local comp row state url detail first=1
  printf '{"slot":"%s","phase":"%s","dir":"%s","issue":"%s","owner":"%s","created_at":"%s","code_sha":"%s","ttl_hours":%s,"expires_at":"%s",' \
    "$(json_escape "$SLOT")" "$(json_escape "$(lease_phase)")" "$(json_escape "${DIR:-}")" \
    "$(json_escape "${ISSUE:-}")" "$(json_escape "$OWNER")" "$(json_escape "$CREATED_AT")" \
    "$(json_escape "${CODE_SHA:-}")" "${TTL_HOURS:-0}" "$(json_escape "${EXPIRES_AT:-}")"
  printf '"lease_issue":"%s","backend_port":%s,"frontend_port":%s,"desktop_renderer_port":%s,"database":"%s","database_state":"%s","account":"%s","profile":"%s","env_file":"%s","logs":"%s","components":{' \
    "$(json_escape "$(effective_lease_issue)")" "$SLOT_BACKEND_PORT" "$SLOT_FRONTEND_PORT" "$SLOT_RENDERER_PORT" \
    "$(json_escape "$SLOT_DB")" "$(database_state)" "$(json_escape "$SLOT_ACCOUNT")" "$(json_escape "$SLOT_PROFILE")" \
    "$(json_escape "$SLOT_ENV_FILE")" "$(json_escape "$LOG_DIR")"
  for comp in $ALL_COMPONENTS; do
    row="$(component_state "$comp")"
    state="${row%%|*}"; row="${row#*|}"
    url="${row%%|*}"; detail="${row#*|}"
    [ "$first" = 1 ] || printf ','
    first=0
    printf '"%s":{"state":"%s","address":"%s","detail":"%s"}' \
      "$comp" "$(json_escape "$state")" "$(json_escape "$url")" "$(json_escape "$detail")"
  done
  printf '}}\n'
}

print_handoff() {
  local entrypoint
  if component_selected web; then
    entrypoint="Open        http://localhost:${SLOT_FRONTEND_PORT}/${WORKSPACE_SLUG}/issues"
  elif component_selected desktop; then
    entrypoint="Desktop     renderer http://localhost:${SLOT_RENDERER_PORT} → backend :${SLOT_BACKEND_PORT}"
  else
    entrypoint="API only    http://localhost:${SLOT_BACKEND_PORT}"
  fi
  cat <<EOF

${C_GREEN}✓ Slot ${SLOT} ready.${C_OFF}

  ${entrypoint}
  Sign in     ${DEV_EMAIL}  ·  code ${DEV_CODE_DEFAULT}
  Backend     http://localhost:${SLOT_BACKEND_PORT}   (GET /health reports pid + commit + started_at)
  MCP         http://localhost:${SLOT_MCP_PORT}  (web /api/mcp → here; loopback only)
  Database    ${SLOT_DB} @ ${SLOT_PG_ENDPOINT} (account ${SLOT_ACCOUNT})
  Commit      $(git -C "$DIR" rev-parse --short HEAD 2>/dev/null || echo unknown)
  Slot        ${SLOT}$( [ "${TTL_HOURS:-0}" != 0 ] && printf ' (expires %s)' "$EXPIRES_AT" )

  Inspect     scripts/dev-env.sh ${SLOT} status
  Stop        scripts/dev-env.sh ${SLOT} down       (keeps the database, restarts in seconds)
  Delete      scripts/dev-env.sh ${SLOT} destroy    (drops the slot database and account)
EOF
}

# ------------------------------------------------------------------- verbs ---

resolve_slot_for_read() {
  [ -f "$SLOT_MANIFEST" ] || die "Slot $SLOT is not registered. Run '$0 $SLOT use' or '$0 $SLOT up' first."
  load_manifest || die "Slot $SLOT has a corrupted manifest at $SLOT_MANIFEST."
  [ -n "${DIR:-}" ] || die "Slot $SLOT manifest has no DIR."
  bind_paths
}

bind_paths() {
  LOG_DIR="$SLOT_DIR/logs"
  PROFILE_DIR="$DEV_PROFILES_HOME/$SLOT_PROFILE"
  MULTICA_BIN="$DIR/server/bin/multica"
  if [ ! -x "$MULTICA_BIN" ] && [ -x "$REPO_ROOT/server/bin/multica" ]; then
    MULTICA_BIN="$REPO_ROOT/server/bin/multica"
  fi
  DESKTOP_USER_DATA_DIR="${DESKTOP_USER_DATA_DIR:-$(desktop_user_data_dir "$SLOT_DESKTOP_SUFFIX")}"
  DESKTOP_ENV_FILE="${DESKTOP_ENV_FILE:-$DIR/apps/desktop/.env.development.local}"
  EXPIRES_AT="${EXPIRES_AT:-}"
  mkdir -p "$LOG_DIR"
}

manifest_env_field() {
  [ -f "$SLOT_MANIFEST" ] || return 0
  local issue
  issue="$(sed -n 's/^ISSUE=//p' "$SLOT_MANIFEST" | head -1)"
  # write_manifest_value %q-quotes; a cleared tenancy lands as '' — read it
  # back as empty, or a released slot would still look held (RUYI-431).
  case "$issue" in
    "''" | '""') issue="" ;;
  esac
  printf '%s' "$issue"
}

issue_key_slug() {
  local slug
  slug="$(printf '%s' "${1:-local}" | tr '[:upper:]' '[:lower:]' | sed 's/[^a-z0-9._-]/-/g; s/-\{2,\}/-/g; s/^[-.]*//; s/[-.]*$//')"
  printf '%s' "${slug:-local}"
}

qa_ttl() {
  if [ -n "$QA_TTL_HOURS" ]; then
    printf '%s' "$QA_TTL_HOURS"
  else
    shared_field ttl_hours_qa
  fi
}

cmd_use() {
  local sha="" want_phase=""
  while [ $# -gt 0 ]; do
    case "$1" in
      --phase)
        want_phase="${2:-}"
        case "$want_phase" in dev|qa) ;; *) die "use --phase must be dev or qa (got '$want_phase')." ;; esac
        shift 2
        ;;
      -*) die "Unknown flag for use: $1" ;;
      *)
        if [ -n "$sha" ]; then die "Unexpected argument for use: $1"; fi
        sha="$1"; shift
        ;;
    esac
  done
  # The lease phase is owned by the handoff verb: --phase applies to a fresh
  # acquire (the slot may be claimed straight into the qa phase), while a held
  # phase only moves via handoff.
  if [ -n "$want_phase" ]; then
    MULTICA_SLOT_PHASE="$want_phase" require_lease
  else
    require_lease
  fi
  local phase
  phase="$(lease_phase)"
  if [ -n "$want_phase" ] && [ "$want_phase" != "$phase" ] \
    && [ "$(lease_field OWNER_ISSUE || true)" = "${MULTICA_CALLER_OWNER:-}" ]; then
    die "Slot $SLOT is already held in phase $phase. Move to $want_phase with '$0 $SLOT handoff --to $want_phase'."
  fi
  PHASE="$phase"

  local now created=""
  now="$(now_iso)"
  if [ -f "$SLOT_MANIFEST" ]; then
    load_manifest || true
    created="${CREATED_AT:-$now}"
  fi
  CREATED_AT="${created:-$now}"
  OWNER="${OWNER_ROLE:-agent}"
  TTL_HOURS=0
  EXPIRES_AT=""
  if [ "$phase" = qa ]; then
    TTL_HOURS="$(qa_ttl)"
    EXPIRES_AT="$(expires_at_after_hours "$TTL_HOURS")"
  fi

  if [ -n "$sha" ]; then
    git -C "$REPO_ROOT" cat-file -e "${sha}^{commit}" 2>/dev/null \
      || die "Commit $sha does not exist in $REPO_ROOT. Run 'use' from a checkout that carries the revision."
    local key wt
    key="$(issue_key_slug "$MANIFEST_ISSUE")"
    mkdir -p "$SLOT_WORKTREE_ROOT"
    wt="$SLOT_WORKTREE_ROOT/$key"
    if [ -e "$wt/.git" ]; then
      git -C "$wt" checkout --detach "$sha" >/dev/null 2>&1 \
        || die "Failed to move the existing worktree at $wt to $sha. Resolve it by hand (it may hold uncommitted changes)."
    else
      [ -e "$wt" ] && die "$wt exists but is not a git worktree; remove it by hand."
      git -C "$REPO_ROOT" worktree add --detach "$wt" "$sha" >/dev/null \
        || die "git worktree add failed for $wt at $sha."
    fi
    DIR="$wt"
    CODE_SOURCE="worktree"
    CODE_SHA="$(git -C "$wt" rev-parse HEAD)"
  else
    DIR="$REPO_ROOT"
    CODE_SOURCE="bind"
    CODE_SHA="$(git -C "$REPO_ROOT" rev-parse HEAD 2>/dev/null || printf 'unknown')"
  fi

  DESKTOP_ENV_FILE="$DIR/apps/desktop/.env.development.local"
  mkdir -p "$SLOT_DIR"
  ensure_slot_env "$(random_hex 16)"
  save_manifest
  bind_paths
  ok "slot $SLOT now runs $CODE_SOURCE $CODE_SHA from $DIR"
}

ensure_code_binding() {
  if [ -f "$SLOT_MANIFEST" ]; then
    load_manifest || true
  fi
  if [ -z "${DIR:-}" ]; then
    info "Slot has no code binding yet; binding $REPO_ROOT at HEAD (like 'use' without a sha)."
    cmd_use
    load_manifest || true
  fi
  [ -d "$DIR" ] || die "Slot $SLOT points at $DIR, which does not exist. Re-run '$0 $SLOT use [sha]' or destroy the slot."
}

cmd_up() {
  local requested="$DEFAULT_COMPONENTS" comp
  while [ $# -gt 0 ]; do
    case "$1" in
      --components|-c) requested="$(printf '%s' "$2" | tr ',' ' ')"; shift 2 ;;
      --all) requested="$ALL_COMPONENTS"; shift ;;
      *) die "Unknown flag for up: $1" ;;
    esac
  done

  for comp in $requested; do
    case " $ALL_COMPONENTS " in *" $comp "*) ;; *) die "Unknown component '$comp'. Valid: $ALL_COMPONENTS" ;; esac
  done
  # web, daemon, desktop and mcp are all clients of the backend; selecting one
  # without api would produce an environment that cannot serve a single request.
  case " $requested " in *" api "*) ;; *) requested="api $requested" ;; esac
  COMPONENTS="$requested"

  require_lease
  # No resident cleanup service is required: every slot start is a safe
  # opportunity to collect expired qa slots.
  cmd_gc --auto

  step "Prerequisites"
  local missing=() tool needed="node go git curl docker"
  # pnpm is only required by the components that actually build JavaScript, so
  # `up C=api` works on a checkout that has never run an install.
  if component_selected web || component_selected desktop || component_selected mcp; then needed="$needed pnpm"; fi
  for tool in $needed; do
    command -v "$tool" >/dev/null 2>&1 || missing+=("$tool")
  done
  [ ${#missing[@]} -eq 0 ] || die "Missing prerequisites: ${missing[*]}"
  mkdir -p "$DEV_TMPDIR"
  if [ "${TMPDIR:-}" != "$DEV_TMPDIR" ]; then
    info "TMPDIR pinned to $DEV_TMPDIR (was ${TMPDIR:-<unset>}) so builds outlive the run that made them."
    export TMPDIR="$DEV_TMPDIR" TMP="$DEV_TMPDIR" TEMP="$DEV_TMPDIR"
  fi
  ok "node, go, git, curl, docker found"

  if component_selected daemon; then
    local marker
    marker="$(daemon_task_marker)"
    [ -z "$marker" ] || die "The daemon component cannot start under a daemon-managed task.
This checkout sits below $marker, and the CLI refuses 'daemon start' there so a
task cannot spawn a second daemon competing for its own work.
Start the rest with 'up --components api,web', or run 'up --components daemon' from your own shell."
  fi

  step "Code"
  ensure_code_binding
  # A slot held in the qa phase slides its expiry forward on every start.
  if [ "$(lease_phase)" = qa ]; then
    TTL_HOURS="$(qa_ttl)"
    EXPIRES_AT="$(expires_at_after_hours "$TTL_HOURS")"
    if [ -f "$SLOT_MANIFEST" ]; then
      load_manifest || true
      save_manifest
    fi
  fi

  step "Environment"
  mkdir -p "$SLOT_DIR"
  ensure_slot_env "$(random_hex 16)"
  preflight_manifest_consistency
  preflight_env_identity
  save_manifest
  ok "preflight passed: $SLOT_ACCOUNT@$SLOT_PG_ENDPOINT/$SLOT_DB"

  step "Database"
  ensure_shared_instance
  provision_slot_database

  step "Migrations"
  bind_paths
  # The migrate child reads DATABASE_URL from its environment.
  export DATABASE_URL="$(env_file_field DATABASE_URL)"
  migrate_database
  ok "$SLOT_DB reachable through the slot account and migrated"

  if [ ! -d "$DIR/node_modules" ] && { component_selected web || component_selected desktop || component_selected mcp; }; then
    step "Dependencies"
    (cd "$DIR" && pnpm install) || die "pnpm install failed."
  fi

  step "Components: $COMPONENTS"
  component_selected api && start_api
  component_selected web && start_web
  component_selected mcp && start_mcp
  component_selected daemon && start_daemon
  component_selected desktop && start_desktop

  start_watchdog || true

  print_handoff
}

cmd_down() {
  local requested="$ALL_COMPONENTS"
  while [ $# -gt 0 ]; do
    case "$1" in
      --components|-c) requested="$(printf '%s' "$2" | tr ',' ' ')"; shift 2 ;;
      -*) die "Unknown flag for down: $1" ;;
      *) die "Unexpected argument for down: $1 (the slot is the first argument: dev-env.sh $SLOT down)" ;;
    esac
  done
  require_lease
  resolve_slot_for_read
  export DATABASE_URL="$(env_file_field DATABASE_URL)" POSTGRES_DB="$SLOT_DB"

  step "Stopping $SLOT: $requested"
  local comp
  for comp in $requested; do
    case " $ALL_COMPONENTS " in *" $comp "*) ;; *) die "Unknown component '$comp'. Valid: $ALL_COMPONENTS" ;; esac
    stop_component "$comp"
  done
  stop_watchdog
  printf '\n%s✓ %s stopped.%s Database, profile and code binding kept — up restarts in seconds.%s\n' "$C_GREEN" "$SLOT" "$C_OFF" "$C_OFF"
}

# A slot worktree may hold a QA session's uncommitted work or a checked-out
# branch; neither may vanish behind destroy's back. Detached + clean trees hold
# no unique commits (they live in the source repository), so they are the only
# removable shape — and the recycle guard (RUYI-594) proves it per tree before
# destroy proceeds: sole-reference commits block, stash blocks or is noted by
# topology, and git that cannot be read blocks too (fail-closed — an
# unreadable tree used to scan as "empty output"). Every scan, pass or not,
# lands an evidence file under ~/.multica/recycle-evidence/ answering "what
# did the guard see" after the fact.
slot_worktrees_removable() {
  local wt
  [ -d "$SLOT_WORKTREE_ROOT" ] || return 0
  for wt in "$SLOT_WORKTREE_ROOT"/*/; do
    [ -d "$wt" ] || continue
    [ -e "$wt/.git" ] || continue
    if [ -n "$(git -C "$wt" symbolic-ref -q HEAD 2>/dev/null || true)" ]; then
      warn "worktree $wt is on a branch; destroy refuses to remove it."
      return 1
    fi
    if [ -n "$(git -C "$wt" status --porcelain 2>/dev/null | head -n 1)" ]; then
      warn "worktree $wt has uncommitted changes; destroy refuses to remove it."
      return 1
    fi
    RG_TOOL="dev-env destroy"
    if recycle_guard_scan_worktree "$wt" slot-worktree 0; then
      if [ -n "$RG_NOTES" ]; then
        local note
        while IFS= read -r note; do info "worktree $wt: $note"; done <<< "$RG_NOTES"
      fi
      if [ -n "$RG_EVIDENCE_FILE" ]; then
        info "recycle evidence: $RG_EVIDENCE_FILE"
      fi
    else
      warn "recycle guard blocks removing worktree $wt:"
      local reason
      while IFS= read -r reason; do warn "  $reason"; done <<< "$RG_REASONS"
      warn "  preserve the work (push, branch, or bundle), then re-run destroy once with MULTICA_RECYCLE_OVERRIDE=1"
      if [ -n "$RG_EVIDENCE_FILE" ]; then
        warn "  evidence: $RG_EVIDENCE_FILE"
      fi
      return 1
    fi
  done
  return 0
}

remove_slot_worktrees() {
  local wt
  for wt in "$SLOT_WORKTREE_ROOT"/*/; do
    [ -d "$wt" ] || continue
    [ -e "$wt/.git" ] || continue
    git -C "$wt" worktree remove --force "$wt" >/dev/null 2>&1 || rm -rf "$wt"
    ok "removed worktree $wt"
  done
  rmdir "$SLOT_WORKTREE_ROOT" 2>/dev/null || true
}

cmd_destroy() {
  local assume_yes=0 reply failures=0
  while [ $# -gt 0 ]; do
    case "$1" in
      --yes|-y) assume_yes=1; shift ;;
      -*) die "Unknown flag for destroy: $1" ;;
      *) die "Unexpected argument for destroy: $1 (the slot is the first argument: dev-env.sh $SLOT destroy)" ;;
    esac
  done
  require_lease

  [ -f "$SLOT_MANIFEST" ] || die "Slot $SLOT is not registered; nothing to destroy (clear a stray lease with lock-release)."
  load_manifest || true
  bind_paths

  if [ "$assume_yes" != 1 ]; then
    printf 'Destroy %s? This stops its processes, drops database %s and account %s, and removes %s. [y/N] ' \
      "$SLOT" "$SLOT_DB" "$SLOT_ACCOUNT" "$SLOT_DIR"
    read -r reply || reply=n
    case "$reply" in y|Y|yes|YES) ;; *) printf 'Cancelled.\n'; return 0 ;; esac
  fi

  export DATABASE_URL="$(env_file_field DATABASE_URL)" POSTGRES_DB="$SLOT_DB"
  step "Destroying $SLOT"
  # The watchdog first: it must not observe the teardown as a budget breach
  # and fight the teardown for the process groups.
  stop_watchdog
  local comp
  for comp in $ALL_COMPONENTS; do
    if ! stop_component "$comp"; then failures=$((failures + 1)); fi
  done

  # Drop only on agreement, and only a slot-shaped database. Guard order:
  # slot_name_guards refuses the main database and any off-pattern name before
  # a single statement is built.
  slot_name_guards
  if ! command -v docker >/dev/null 2>&1; then
    warn "docker not found; $SLOT_DB was left in place."
    failures=$((failures + 1))
  elif ! container_running; then
    warn "Nothing answered for container $SLOT_PG_CONTAINER; $SLOT_DB was left in place."
    failures=$((failures + 1))
  elif env_file_agrees_on_database; then
    if db_admin_psql postgres -v ON_ERROR_STOP=1 -c "DROP DATABASE IF EXISTS \"${SLOT_DB}\" WITH (FORCE)" >/dev/null; then
      ok "dropped database $SLOT_DB"
    else
      warn "failed to drop database $SLOT_DB; keeping the manifest"
      failures=$((failures + 1))
    fi
    # Scratch databases the slot account minted at runtime (integration tests
    # create throwaway databases) would keep DROP ROLE from succeeding. Only
    # databases the account itself owns are candidates, so other slots and the
    # main database are unreachable here by construction; the name guards are
    # the belt to that suspenders.
    local scratch
    while IFS= read -r scratch; do
      [ -n "$scratch" ] || continue
      case "$scratch" in
        "$MAIN_DATABASE_NAME"|postgres|template0|template1|multica_dev[12])
          warn "refusing to drop suspect database $scratch (protected name owned by $SLOT_ACCOUNT?)" ;;
        *)
          if db_admin_psql postgres -v ON_ERROR_STOP=1 -c "DROP DATABASE IF EXISTS \"$scratch\" WITH (FORCE)" >/dev/null; then
            ok "dropped scratch database $scratch (owned by $SLOT_ACCOUNT)"
          else
            warn "failed to drop scratch database $scratch"
            failures=$((failures + 1))
          fi ;;
      esac
    done < <(db_admin_psql postgres -tAc "SELECT datname FROM pg_database WHERE pg_get_userbyid(datdba)='${SLOT_ACCOUNT}' AND datname <> '${SLOT_DB}'" 2>/dev/null || true)
    if db_admin_psql postgres -v ON_ERROR_STOP=1 -c "DROP ROLE IF EXISTS \"${SLOT_ACCOUNT}\"" >/dev/null; then
      ok "dropped account $SLOT_ACCOUNT"
    else
      warn "failed to drop account $SLOT_ACCOUNT; keeping the manifest"
      failures=$((failures + 1))
    fi
  else
    warn "refusing to drop database $SLOT_DB: the manifest and $SLOT_ENV_FILE must both name it before a drop"
    info "Compare $SLOT_ENV_FILE with the manifest, resolve by hand, then re-run to release the slot."
    failures=$((failures + 1))
  fi

  if rm -rf "$PROFILE_DIR"; then
    ok "removed CLI profile $SLOT_PROFILE"
  else
    warn "failed to remove CLI profile $SLOT_PROFILE"
    failures=$((failures + 1))
  fi

  local expected_workspaces="$SLOT_WORKSPACES_ROOT"
  if [ "$SLOT_WORKSPACES_ROOT" != "$expected_workspaces" ]; then
    warn "refusing to remove unexpected workspaces root $SLOT_WORKSPACES_ROOT (expected $expected_workspaces)"
    failures=$((failures + 1))
  elif rm -rf "$SLOT_WORKSPACES_ROOT"; then
    ok "removed daemon workspaces $SLOT_WORKSPACES_ROOT"
  else
    warn "failed to remove daemon workspaces $SLOT_WORKSPACES_ROOT"
    failures=$((failures + 1))
  fi

  local expected_desktop_data="$(desktop_user_data_dir "$SLOT_DESKTOP_SUFFIX")"
  if [ -n "$DESKTOP_USER_DATA_DIR" ] && [ "$DESKTOP_USER_DATA_DIR" != "$expected_desktop_data" ]; then
    warn "refusing to remove unexpected Desktop userData $DESKTOP_USER_DATA_DIR"
    failures=$((failures + 1))
  elif [ -d "$expected_desktop_data" ]; then
    if rm -rf "$expected_desktop_data"; then
      ok "removed Desktop userData $expected_desktop_data"
    else
      warn "failed to remove Desktop userData $expected_desktop_data"
      failures=$((failures + 1))
    fi
  fi

  if [ -f "${DESKTOP_ENV_FILE:-}" ] \
    && grep -Fqx "# Managed by scripts/dev-env.sh for slot ${SLOT}." "$DESKTOP_ENV_FILE"; then
    if rm -f "$DESKTOP_ENV_FILE"; then
      ok "removed managed Desktop env file"
    else
      warn "failed to remove $DESKTOP_ENV_FILE"
      failures=$((failures + 1))
    fi
  fi

  if ! slot_worktrees_removable; then
    warn "slot worktrees under $SLOT_WORKTREE_ROOT are not removable; keeping the manifest so destroy can retry after they are handled."
    failures=$((failures + 1))
  fi

  if [ "$failures" -ne 0 ]; then
    die "$SLOT was only partially destroyed ($failures cleanup failure(s)). Its manifest and lease were kept so destroy can retry."
  fi

  remove_slot_worktrees
  rm -rf "$SLOT_DIR"
  rm -f "$SLOT_LOCK_FILE"
  ok "released slot $SLOT"
  printf '\n%s✓ %s destroyed.%s\n' "$C_GREEN" "$SLOT" "$C_OFF"
}

cmd_status() {
  local as_json=0
  while [ $# -gt 0 ]; do
    case "$1" in
      --json) as_json=1; shift ;;
      *) die "Unknown flag for status: $1" ;;
    esac
  done
  [ -f "$SLOT_MANIFEST" ] || {
    if [ "$as_json" = 1 ]; then
      printf '{"slot":"%s","registered":false,"phase":"%s","lease_issue":"%s"}\n' \
        "$(json_escape "$SLOT")" "$(json_escape "$(lease_phase)")" "$(json_escape "$(effective_lease_issue)")"
    else
      printf '%s: not registered (ports %s/%s, database %s)\n' \
        "$SLOT" "$SLOT_BACKEND_PORT" "$SLOT_FRONTEND_PORT" "$SLOT_DB"
    fi
    return 0
  }
  load_manifest || die "Slot $SLOT has a corrupted manifest at $SLOT_MANIFEST."
  bind_paths
  if [ "$as_json" = 1 ]; then print_status_json; else print_status_human; fi
}

cmd_list() {
  local as_json=0 arg
  for arg in "$@"; do
    case "$arg" in
      --json) as_json=1 ;;
      *) die "Unknown flag for list: $arg" ;;
    esac
  done

  local slots names
  names="$(node -e '
    const fs = require("fs");
    const facts = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
    process.stdout.write(facts.slots.map(s => s.name).join("\n"));
  ' "$SLOTS_FILE")"

  if [ "$as_json" = 1 ]; then printf '['; fi
  local first=1 slot
  while read -r slot; do
    [ -n "$slot" ] || continue
    (
      require_slot "$slot"
      local registered=0 api_state="stopped" dir="" issue="" expires=""
      if [ -f "$SLOT_MANIFEST" ]; then
        registered=1
        load_manifest || true
        bind_paths || true
        api_state="$(component_state api)"
        api_state="${api_state%%|*}"
        dir="${DIR:-}"
        issue="${ISSUE:-}"
        expires="${EXPIRES_AT:-}"
      fi
      if [ "$as_json" = 1 ]; then
        printf '{"slot":"%s","phase":"%s","registered":%s,"api":"%s","lease_issue":"%s","dir":"%s","backend_port":%s,"frontend_port":%s,"database":"%s","expires_at":"%s"}' \
          "$(json_escape "$slot")" "$(json_escape "$(lease_phase)")" "$registered" "$api_state" \
          "$(json_escape "$(effective_lease_issue)")" "$(json_escape "$dir")" \
          "$SLOT_BACKEND_PORT" "$SLOT_FRONTEND_PORT" "$(json_escape "$SLOT_DB")" "$(json_escape "$expires")"
      else
        printf '%-6s %-6s %-10s %-9s %-14s %-24s %s\n' "$slot" "$(lease_phase)" \
          "$( [ "$registered" = 1 ] && printf registered || printf free )" "$api_state" \
          "$( [ -n "$issue" ] && printf '%s' "$issue" || printf '-' )" \
          "$SLOT_DB" "${dir:--}"
      fi
    ) | { if [ "$as_json" = 1 ] && [ "$first" != 1 ]; then printf ','; fi; cat; }
    first=0
  done <<EOF
$names
EOF
  if [ "$as_json" = 1 ]; then
    printf ']\n'
  fi
}

# ---- orphan acceptance (RUYI-333: destroy leaves nothing behind) -----------
#
# `orphans` is the acceptance verb for traceability: after `down`/`destroy`
# nothing attributable to the slot may survive. It re-derives everything from
# live system state — /proc, sockets, git, docker — and needs NO manifest or
# pid bookkeeping, so it stays meaningful exactly when the bookkeeping is
# gone. Exit 0 = clean, exit 1 = orphans found (both output modes).

# PIDs listening on a TCP port, via ss(8). Empty output when ss is missing is
# reported as an undetectable scan by the caller, never as silent cleanliness.
port_listener_pids() {
  local port="$1"
  command -v ss >/dev/null 2>&1 || return 0
  ss -ltnpH "sport = :$port" 2>/dev/null | sed -n 's/.*pid=\([0-9]*\).*/\1/p' | sort -u
}

slot_orphans() {
  # Emits "<class>\t<detail>" per orphan; returns 1 when anything was found.
  local pid entry comm cwd root line found=1
  local -a roots=()
  for root in "$SLOT_DIR" "$SLOT_WORKTREE_ROOT" "$SLOT_WORKSPACES_ROOT"; do
    [ -n "$root" ] && roots+=("$root")
  done

  # 1+2) live processes: launch_detached tags the whole component tree with
  # MULTICA_SLOT=<slot>; the cwd sweep also catches untagged strays started by
  # hand inside the slot's own directories.
  for entry in /proc/[0-9]*; do
    [ -d "$entry" ] || continue
    pid="${entry#/proc/}"
    [ "$pid" != "$$" ] && [ "$pid" != "$PPID" ] || continue
    comm="$(cat "$entry/comm" 2>/dev/null || printf '?')"
    # The subshell keeps the shell's own "permission denied" chatter for
    # unreadable /proc entries off our output; an unreadable environ simply
    # means the process is not attributable to this slot.
    if ( tr '\0' '\n' < "$entry/environ" ) 2>/dev/null | grep -qx "MULTICA_SLOT=$SLOT"; then
      printf 'process-env\t%s (%s) carries MULTICA_SLOT=%s\n' "$pid" "$comm" "$SLOT"
      found=0
      continue
    fi
    cwd="$(readlink -f "$entry/cwd" 2>/dev/null || true)"
    [ -n "$cwd" ] || continue
    for root in "${roots[@]}"; do
      case "$cwd" in
        "$root"|"$root"/*)
          printf 'process-cwd\t%s (%s) cwd %s\n' "$pid" "$comm" "$cwd"
          found=0
          break
          ;;
      esac
    done
  done

  # 3) the slot's fixed ports must be free.
  local port
  for port in "$SLOT_BACKEND_PORT" "$SLOT_FRONTEND_PORT" "$SLOT_RENDERER_PORT" "$SLOT_MCP_PORT"; do
    while read -r pid; do
      [ -n "$pid" ] || continue
      comm="$(cat "/proc/$pid/comm" 2>/dev/null || printf '?')"
      printf 'port-listener\tport %s held by %s (%s)\n' "$port" "$pid" "$comm"
      found=0
    done < <(port_listener_pids "$port")
  done

  # 4) worktrees: nothing registered under the slot's worktree root, and no
  # leftover directories in it (destroy is expected to leave the root empty).
  local wt_list
  wt_list="$(
    {
      cd "$REPO_ROOT" && git worktree list --porcelain 2>/dev/null | sed -n 's/^worktree //p'
      [ -d "$SLOT_WORKTREE_ROOT" ] && find "$SLOT_WORKTREE_ROOT" -mindepth 1 -maxdepth 1 2>/dev/null
    } | while IFS= read -r line; do
      case "$line" in "$SLOT_WORKTREE_ROOT"|"$SLOT_WORKTREE_ROOT"/*) printf '%s\n' "$line" ;; esac
    done | sort -u
  )"
  if [ -n "$wt_list" ]; then
    while IFS= read -r line; do
      [ -n "$line" ] || continue
      printf 'worktree\t%s\n' "$line"
      found=0
    done <<< "$wt_list"
  fi

  # 5) docker containers named after the slot (the shared postgres container
  # carries no slot name and is intentionally out of scope).
  if command -v docker >/dev/null 2>&1; then
    while IFS= read -r line; do
      [ -n "$line" ] || continue
      printf 'container\t%s\n' "$line"
      found=0
    done < <(docker ps --format '{{.Names}}' 2>/dev/null | grep -- "$SLOT" || true)
  fi

  return "$found"
}

cmd_orphans() {
  local as_json=0 arg
  for arg in "$@"; do
    case "$arg" in
      --json) as_json=1 ;;
      *) die "Unknown flag for orphans: $arg" ;;
    esac
  done

  local out
  out="$(slot_orphans || true)"
  if [ "$as_json" = 1 ]; then
    printf '['
    local first=1 class detail
    while IFS=$'\t' read -r class detail; do
      [ -n "$class" ] || continue
      if [ "$first" != 1 ]; then printf ','; fi
      first=0
      printf '{"class":"%s","detail":"%s"}' "$(json_escape "$class")" "$(json_escape "$detail")"
    done <<< "$out"
    printf ']\n'
  elif [ -n "$out" ]; then
    while IFS= read -r line; do printf '  %s\n' "$line"; done <<< "$out"
    printf 'Slot %s is NOT clean: orphans listed above.\n' "$SLOT"
    exit 1
  else
    ok "Slot $SLOT is clean: no orphan processes, ports, worktrees or containers."
  fi
}

# Dry-run companion of the destroy-path guard (RUYI-594): the same scan, the
# same evidence files (marked dry_run: true), nothing deleted — the report
# answers "what would the guard say" before any real recycle is attempted.
recycle_guard_gc_dry_scan() {
  local wt any_blocked=0 line
  [ -d "$SLOT_WORKTREE_ROOT" ] || return 0
  for wt in "$SLOT_WORKTREE_ROOT"/*/; do
    [ -d "$wt" ] || continue
    [ -e "$wt/.git" ] || continue
    RG_TOOL="dev-env gc --dry-run"
    if recycle_guard_scan_worktree "$wt" slot-worktree 1; then
      info "guard: $RG_VERDICT — $wt ($RG_COUNTS)"
      [ -n "$RG_EVIDENCE_FILE" ] && info "  evidence: $RG_EVIDENCE_FILE"
    else
      info "guard: blocked — $wt"
      while IFS= read -r line; do info "  $line"; done <<< "$RG_REASONS"
      info "  evidence: $RG_EVIDENCE_FILE"
      any_blocked=1
    fi
  done
  if [ "$any_blocked" = 1 ]; then
    info "destroy of $slot would be REFUSED by the recycle guard: preserve the listed work, then re-run destroy once with MULTICA_RECYCLE_OVERRIDE=1"
  fi
  return 0
}

# Expired qa slots are collected on every gc (and opportunistically on every
# `up`): a busy slot slides its expiry forward on each use/up, a forgotten one
# dies ttl_hours_qa after its last use. dev slots have no TTL — they are
# handed back by lock-release at issue closure, never by a timer.
cmd_gc() {
  local dry_run=0 automatic=0 arg
  while [ $# -gt 0 ]; do
    case "$1" in
      --dry-run) dry_run=1 ;;
      --auto) automatic=1 ;;
      *) die "Unknown flag for gc: $1" ;;
    esac
    shift
  done

  # Evidence housekeeping: the TTL sweep is the only deleter of recycle
  # evidence, on either track.
  recycle_guard_sweep

  local names slot
  names="$(node -e '
    const fs = require("fs");
    const facts = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
    process.stdout.write(facts.slots.map(s => s.name).join("\n"));
  ' "$SLOTS_FILE")"
  while read -r slot; do
    [ -n "$slot" ] || continue
    (
      require_slot "$slot"
      [ -f "$SLOT_MANIFEST" ] || exit 0
      load_manifest || exit 0
      # Only qa-phase slots are collectible: development slots have no TTL —
      # they are handed back at issue closure via lock-release, never by a
      # timer.
      [ "$(lease_phase)" = qa ] || exit 0
      local reason=""
      if [ -n "${DIR:-}" ] && [ ! -d "$DIR" ]; then
        reason="its code directory $DIR is gone"
      fi
      if [ -z "$reason" ] && [ "${TTL_HOURS:-0}" != 0 ] && [ -n "${EXPIRES_AT:-}" ]; then
        local expiry_epoch
        expiry_epoch="$(node -e 'process.stdout.write(String(Math.floor(Date.parse(process.argv[1]) / 1000)))' "$EXPIRES_AT" 2>/dev/null || echo 0)"
        [ "$(now_epoch)" -lt "$expiry_epoch" ] || reason="it expired at $EXPIRES_AT"
      fi
      [ -n "$reason" ] || exit 0
      if [ "$dry_run" = 1 ]; then
        printf '%s would be collected: %s\n' "$slot" "$reason"
        recycle_guard_gc_dry_scan
      else
        printf '%s: %s\n' "$slot" "$reason"
        if ! MULTICA_SLOT_GC_INTERNAL=1 MULTICA_CALLER_OWNER="${MULTICA_CALLER_OWNER:-gc}" \
          bash "$REPO_ROOT/scripts/dev-env.sh" "$slot" destroy --yes; then
          if [ "$automatic" = 1 ]; then
            warn "automatic cleanup of $slot failed; its manifest was kept for retry"
          else
            exit 1
          fi
        fi
      fi
    )
  done <<EOF
$names
EOF
}

# Runs a command with this slot's variables, without the agent runtime's
# production MULTICA_* values and with a durable TMPDIR.
cmd_exec() {
  if [ "${1:-}" = "--" ]; then shift; fi
  [ $# -gt 0 ] || die "Usage: dev-env.sh <slot> exec -- <command> [args...]"
  local lease_issue
  lease_issue="$(effective_lease_issue)"
  if [ -n "$lease_issue" ] && [ -n "${MULTICA_CALLER_OWNER:-}" ] && [ "$lease_issue" != "${MULTICA_CALLER_OWNER}" ]; then
    die "Slot $SLOT is held by $lease_issue; exec as ${MULTICA_CALLER_OWNER} refused."
  fi
  resolve_slot_for_read
  mkdir -p "$DEV_TMPDIR"
  cd "$DIR"
  set -a
  # shellcheck disable=SC1090
  . "$SLOT_ENV_FILE"
  set +a
  export TMPDIR="$DEV_TMPDIR" TMP="$DEV_TMPDIR" TEMP="$DEV_TMPDIR"
  export MULTICA_DEV_PROFILE="$SLOT_PROFILE"
  exec "${CLEAN_ENV[@]}" MULTICA_WORKSPACES_ROOT="$SLOT_WORKSPACES_ROOT" "$@"
}

cmd_logs() {
  local comp="${1:-}" lines="${2:-50}"
  resolve_slot_for_read
  if [ -z "$comp" ]; then
    info "logs for $SLOT live in $LOG_DIR:"
    local f
    for f in "$LOG_DIR"/*.log; do
      [ -f "$f" ] || continue
      info "  $f"
    done
    return 0
  fi
  case " $ALL_COMPONENTS " in *" $comp "*) ;; *) die "Unknown component '$comp'. Valid: $ALL_COMPONENTS" ;; esac
  [ -f "$(log_file "$comp")" ] || die "No log for $comp yet: $(log_file "$comp")"
  tail -n "$lines" "$(log_file "$comp")"
}

# Explicit opt-in read-only window onto the shared instance's main database.
# The main database is managed infrastructure: this entry has SELECT-shaped
# verbs only, and refuses to run without --allow so a slip of the fingers can
# never reach it (the write-side tooling belongs to the shared-face stage).
cmd_main_db() {
  local sub="${1:-}" allow=0
  [ $# -gt 0 ] && shift
  while [ $# -gt 0 ]; do
    case "$1" in
      --allow) allow=1; shift ;;
      *) die "Unknown flag for main-db: $1" ;;
    esac
  done
  case "$sub" in
    status) ;;
    "") die "Usage: dev-env.sh main-db status --allow (read-only inventory of the shared instance)" ;;
    *) die "Unknown main-db verb '$sub'. Only 'status' exists, and it is read-only." ;;
  esac
  [ "$allow" = 1 ] || die "Refusing to touch the main database without --allow. The shared instance is protected infrastructure; read access must be explicit."
  container_running || die "Shared container $SLOT_PG_CONTAINER is not running."
  db_admin_psql postgres -c "SELECT datname AS database, pg_size_pretty(pg_database_size(datname)) AS size FROM pg_database ORDER BY 1"
  info "protected databases: $(shared_field shared_postgres.protected_databases | tr '\n' ' ')"
  info "this entry is read-only; backups and ACL work belong to the shared-face tooling"
}

# --- audit (RUYI-431): read-only bypass detection ----------------------------
# The RUYI-415 lesson: QA built a second server against a scratch multica_*
# database on the shared instance and nothing reported it. `audit` sweeps for
# exactly that shape and never modifies anything: (1) every multica_% database
# on the shared instance that is neither the protected main db nor a slot's
# fixed database; (2) TCP listeners outside the registered ports that look
# like Multica servers — their DATABASE_URL names an unregistered multica_%
# database, their cwd sits under the legacy QA root or the slot home, or they
# are a cmd/api build. Slot-tagged processes and `multica daemon` are the
# slot model's own and never reported. Exit 1 means findings exist.
audit_findings=()

audit_finding() { audit_findings+=("$1"$'\t'"$2"); }

audit_registry() { # registered ports + databases, one line each: port|db TAB value
  node -e '
    const fs = require("fs");
    const facts = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
    const sp = facts.shared_postgres || {};
    for (const db of sp.protected_databases || []) process.stdout.write("db\t" + db + "\n");
    const endpoint = String(sp.endpoint || "").replace(/.*:/, "");
    if (endpoint) process.stdout.write("port\t" + endpoint + "\n");
    for (const s of facts.slots || []) {
      for (const key of ["backend_port", "frontend_port", "desktop_renderer_port", "mcp_port"]) {
        if (s[key] !== undefined) process.stdout.write("port\t" + s[key] + "\n");
      }
      if (s.database) process.stdout.write("db\t" + s.database + "\n");
    }
  ' "$SLOTS_FILE"
}

audit_qa_root() { printf '%s' "${MULTICA_QA_ROOT:-$HOME/.multica/qa}"; }

audit_db_finding() {
  local reg_dbs="$1" name size
  while IFS='|' read -r name size; do
    [ -n "$name" ] || continue
    printf '%s\n' "$reg_dbs" | grep -Fxq "$name" && continue
    audit_finding "unregistered-database" "$name ($size)"
  done
}

audit_bypass_finding() { # line from ss -ltnpH, reg_ports reg_dbs
  local line="$1" reg_ports="$2" reg_dbs="$3"
  local addr port pid cmd cwd db_url db_in_url detail reason
  addr="$(printf '%s\n' "$line" | awk '{print $4}')"
  port="${addr##*:}"
  case "$port" in ''|*[!0-9]*) return 0 ;; esac
  printf '%s\n' "$reg_ports" | grep -Fxq "$port" && return 0
  pid="$(printf '%s\n' "$line" | sed -n 's/.*pid=\([0-9]\{1,\}\).*/\1/p' | head -1)"
  [ -n "$pid" ] || return 0 # no owner visible: report-only verb, cannot judge
  [ -r "/proc/$pid/environ" ] || return 0 # exited between the scan and now
  cmd="$(ps -o cmd= -p "$pid" 2>/dev/null || true)"
  case "$cmd" in *"multica daemon"*) return 0 ;; esac
  if tr '\0' '\n' < "/proc/$pid/environ" 2>/dev/null | grep -q '^MULTICA_SLOT='; then
    return 0 # the slot model's own process, on whatever port it holds
  fi
  reason=""
  db_url="$(tr '\0' '\n' < "/proc/$pid/environ" 2>/dev/null | sed -n 's/^DATABASE_URL=//p' | head -1)"
  if [ -n "$db_url" ]; then
    db_in_url="${db_url%%\?*}"
    db_in_url="${db_in_url##*/}"
    if printf '%s' "$db_in_url" | grep -q '^multica' \
       && ! printf '%s\n' "$reg_dbs" | grep -Fxq "$db_in_url"; then
      reason="DATABASE_URL names $db_in_url, which is not a registered slot database"
    fi
  fi
  if [ -z "$reason" ]; then
    local qa_root
    qa_root="$(audit_qa_root)"
    cwd="$(readlink "/proc/$pid/cwd" 2>/dev/null || true)"
    case "$cwd" in
      "") ;;
      "$qa_root"|"$qa_root"/*|"$SLOT_HOME"|"$SLOT_HOME"/*)
        reason="listener cwd sits under the QA/slot home" ;;
    esac
  fi
  if [ -z "$reason" ]; then
    case "$cmd" in *cmd/api*) reason="process is a Multica api server (cmd/api)" ;; esac
  fi
  [ -n "$reason" ] || return 0
  detail="pid $pid port $port cmd ${cmd:0:120} ($reason)"
  audit_finding "bypass-listener" "$detail"
}

cmd_audit() {
  local json=0
  while [ $# -gt 0 ]; do
    case "$1" in
      --json) json=1; shift ;;
      *) die "Unknown flag for audit: $1" ;;
    esac
  done

  local registry reg_ports reg_dbs line
  registry="$(audit_registry)"
  reg_ports="$(printf '%s\n' "$registry" | awk -F'\t' '$1 == "port" { print $2 }')"
  reg_dbs="$(printf '%s\n' "$registry" | awk -F'\t' '$1 == "db" { print $2 }')"

  if container_running; then
    # Process substitution, not a pipe: audit_finding appends to audit_findings
    # and a pipeline would run the collector in a subshell, losing every row.
    audit_db_finding "$reg_dbs" < <(db_admin_psql postgres -tAc "SELECT datname, pg_size_pretty(pg_database_size(datname)) FROM pg_database WHERE datname LIKE 'multica%' ORDER BY 1")
  else
    info "shared container $SLOT_PG_CONTAINER is not running; the database sweep is skipped"
  fi

  while IFS= read -r line; do
    [ -n "$line" ] || continue
    audit_bypass_finding "$line" "$reg_ports" "$reg_dbs"
  done < <(ss -ltnpH 2>/dev/null || ss -ltnp 2>/dev/null || true)

  if [ "$json" = 1 ]; then
    printf '['
    local first=1 f class detail
    for f in ${audit_findings[@]+"${audit_findings[@]}"}; do
      class="${f%%$'\t'*}"
      detail="${f#*$'\t'}"
      [ "$first" = 1 ] || printf ','
      first=0
      printf '{"class":"%s","detail":"%s"}' "$(json_escape "$class")" "$(json_escape "$detail")"
    done
    printf ']\n'
  elif [ "${#audit_findings[@]}" -eq 0 ]; then
    ok "audit clean: no unregistered multica_% databases, no bypass listeners"
  else
    for f in "${audit_findings[@]}"; do
      printf '%s\n' "$f"
    done
    warn "audit found ${#audit_findings[@]} item(s); this verb is report-only — reclaim with qa-clean/dev-env destroy, never by hand here"
  fi
  [ "${#audit_findings[@]}" -eq 0 ]
}

usage() {
  cat <<'EOF'
Fixed-slot local development environments (fact source: scripts/slots.json).

  dev-env.sh <slot> up      [--components api,web,daemon,desktop] [--all]
  dev-env.sh <slot> down    [--components ...]
  dev-env.sh <slot> status  [--json]
  dev-env.sh <slot> use     [sha]        # bind this checkout, or load a revision
  dev-env.sh <slot> handoff --to dev|qa [--note ...]  # role handover, same issue
  dev-env.sh <slot> lock-status
  dev-env.sh <slot> lock-release [--force]
  dev-env.sh <slot> lock-recover          # take over after the lease issue left in_progress
  dev-env.sh <slot> destroy [--yes]
  dev-env.sh <slot> orphans [--json]      # acceptance: nothing of this slot survives
  dev-env.sh <slot> exec    -- <command> [args...]
  dev-env.sh <slot> logs    [component] [lines]
  dev-env.sh list           [--json]
  dev-env.sh gc             [--dry-run]
  dev-env.sh audit          [--json]  # read-only bypass sweep; exit 1 = findings
  dev-env.sh main-db status --allow     # read-only inventory; refuses without --allow

Slots: dev1, dev2. Each slot owns database multica_<slot> and account
<slot>_app on the shared instance (localhost:5432); its env file is generated
at ~/.multica/slots/<slot>/env and verified before every start. The slot lease
carries a role phase — dev (development) or qa (verification) — with exactly
one holder at a time; `handoff --to` moves it atomically within the same
issue (dev -> qa -> release at closure). qa phase is the only TTL'd phase
(gc collects after 24h idle); dev phase has no timer. A dual-server test
window is the one shape where ONE issue holds BOTH slots: arm each slot's
lease with `use --phase qa`, run the two servers, then close the window by
releasing both leases with `lock-release` right away — a qa-phase holder may
release while its issue is still in_progress (the dev phase releases only at
issue closure). `audit` reports Multica-shaped activity outside this model:
unregistered multica_% databases on the shared instance, and listeners off
the registered ports whose DATABASE_URL names an unregistered multica_%
database, whose cwd sits under the QA/slot home, or that are a cmd/api
build. It never modifies anything.

Resource budget per slot (scripts/slots.json resource_budget): 4 CPU cores
pinned via taskset (dev1=0-3, dev2=4-7), api <= 768MB (GOMEMLIMIT),
web <= 8192MB (node --max-old-space-size), daemon/companion <= 256MB; a
watchdog resamples each component's real process tree plus its proven port
listener every tick and kills it after two consecutive RSS breaches.
Shared PostgreSQL is capped by docker compose (2g / 4 cpus).

Write commands require MULTICA_CALLER_OWNER=<issue-id>. The shared main
database 'multica' and role 'multica' are outside every slot verb's reach.
EOF
}

main() {
  local first="${1:-}"
  case "$first" in
    ""|-h|--help|help)
      usage
      exit 0
      ;;
  esac

  ensure_slots_file

  case "$first" in
    list|ls)
      shift
      cmd_list "$@"
      exit 0
      ;;
    gc)
      shift
      cmd_gc "$@"
      exit 0
      ;;
    main-db)
      shift
      load_shared_facts
      cmd_main_db "$@"
      exit 0
      ;;
    audit)
      shift
      load_shared_facts
      cmd_audit "$@"
      exit "$?"
      ;;
  esac

  local action="${2:-}"
  [ $# -ge 2 ] || { usage >&2; printf '\nAn action is required after the slot (up, down, status, use, destroy, ...).\n' >&2; exit 2; }
  shift 2
  require_slot "$first"
  case "$action" in
    up) cmd_up "$@" ;;
    down) cmd_down "$@" ;;
    status) cmd_status "$@" ;;
    use) cmd_use "$@" ;;
    handoff) cmd_handoff "$@" ;;
    lock-status) cmd_lock_status ;;
    lock-release) cmd_lock_release "$@" ;;
    lock-recover) cmd_lock_recover ;;
    destroy) cmd_destroy "$@" ;;
    orphans) cmd_orphans "$@" ;;
    exec) cmd_exec "$@" ;;
    logs) cmd_logs "$@" ;;
    -h|--help|help) usage ;;
    *) usage >&2; printf '\nUnknown action "%s".\n' "$action" >&2; exit 2 ;;
  esac
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  main "$@"
fi
