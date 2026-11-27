#!/usr/bin/env bash
# Sweep what build/QA phases leave behind on the slot model.
#
# The 2026-09-30 incident: three QA dev stacks (next-server + Go api, ~10 GB
# RSS) ran for half a day after their sessions ended, because the QA
# environments were registered OWNER=human TTL=0 and no command existed to
# reclaim them short of manual archaeology. This script is the reclaim
# command — and the one agents are told to run at the end of a QA turn (the
# runtime brief's Background Task Safety section pins it).
#
# On the slot model (RUYI-333) there are exactly two general slots (dev1,
# dev2) and QA verification reuses the issue's own slot: the lease carries a
# role phase (dev -> qa via `handoff --to qa`), and the slot keeps its fixed
# database, account and ports the whole time. qa phase additionally expires
# (gc collects it); this script handles what expiry cannot: the issue-scoped
# teardown at the end of a turn, and orphans that outlive their registration.
#
# A verification window only owns a lease. Never destroy the Issue's
# database, fixtures, profile or checkout while closing that window.
# Unregistered listeners, containers and worktrees are report-only.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/dev-env.sh
source "$SCRIPT_DIR/dev-env.sh"

QA_ROOT="${MULTICA_QA_ROOT:-$HOME/.multica/qa}"

usage() {
  cat <<'EOF'
Report build/QA leftovers from the shared slots and the legacy QA root (~/.multica/qa).

  qa-clean.sh                      # report only (dry run)
  qa-clean.sh --issue ruyi-283     # scope the sweep to one issue
  qa-clean.sh --issue ruyi-283 --yes
                                   # stop the managed slot; preserve Issue data
  qa-clean.sh --issue ruyi-283 --yes --docker
                                   # also report containers naming the issue

An owned slot is stopped without deleting its database, fixtures, profile,
checkout or daemon identity. A QA lease may then be returned. Mutations require
a matching MULTICA_CALLER_OWNER; --issue only scopes the operation. With --yes but
no --issue, only QA leases owned by MULTICA_CALLER_OWNER are considered.
Listeners, containers and worktrees found by name or path are report-only.
EOF
}

issue="" assume_yes=0 sweep_docker=0
while [ $# -gt 0 ]; do
  case "$1" in
    --issue) issue="${2:-}"; [ -n "$issue" ] || die "--issue needs a value"; shift 2 ;;
    --issue=*) issue="${1#*=}"; [ -n "$issue" ] || die "--issue= needs a value"; shift ;;
    --yes|-y) assume_yes=1; shift ;;
    --docker) sweep_docker=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done

issue_lc="$(printf '%s' "$issue" | tr '[:upper:]' '[:lower:]')"
# Naming is not consistent across issues ("ruyi-283", "ruyi283", "ruyi_283"
# all exist in the wild), so id matching ignores separators before comparing.
flatten_id() { printf '%s' "$1" | tr -d -- '-_'; }
issue_flat="$(flatten_id "$issue_lc")"
matches_issue() { # $1 = text to test (any case)
  [ -z "$issue_lc" ] && return 0
  case "$(printf '%s' "${1:-}" | tr '[:upper:]' '[:lower:]' | tr -d -- '-_')" in *"$issue_flat"*) return 0 ;; esac
  return 1
}

issue_lifecycle() { # active | terminal | unknown
  case "$(issue_status_category "$1")" in
    in_progress|in_review|todo|backlog|blocked) printf 'active' ;;
    done|cancelled) printf 'terminal' ;;
    *) printf 'unknown' ;;
  esac
}

ensure_slots_file

if [ "$assume_yes" != 1 ]; then
  step "qa-clean dry run — nothing will be changed (add --yes to execute)"
else
  step "qa-clean: closing owned QA windows and reporting leftovers"
fi
[ -z "$issue_lc" ] || info "scoped to issue: $issue_lc"

# --- 1. slots naming the target issue (any phase; unscoped: qa phase only) ---

while read -r slot; do
  [ -n "$slot" ] || continue
  (
    require_slot "$slot"
    [ -f "$SLOT_MANIFEST" ] || exit 0
    load_manifest || exit 0
    slot_issue="${ISSUE:-}"
    if [ -n "$issue" ] && [ "${slot_issue,,}" != "$issue_lc" ]; then
      exit 0
    fi
    # The issue-scoped sweep checks the slot whatever its phase (QA reuses
    # the issue's dev slot, so the lease may be back in dev after its qa
    # handoff). An unscoped --yes sweep only takes qa-phase slots — dev-phase
    # slots have no TTL and are released at issue closure, never by a timer.
    if [ -z "$issue_lc" ] && [ "$(lease_phase)" != qa ]; then
      exit 0
    fi
    if [ "$assume_yes" != 1 ]; then
      printf 'would check whether to stop slot %s (issue %s, dir %s, db %s)\n' "$slot" "${slot_issue:-?}" "${DIR:-?}" "$SLOT_DB"
      exit 0
    fi
    local_owner="${MULTICA_CALLER_OWNER:-}"
    if [ -z "$local_owner" ] || [ "$local_owner" != "$slot_issue" ]; then
      printf 'skipped slot %s: matching MULTICA_CALLER_OWNER required (owner: %s)\n' "$slot" "${slot_issue:-?}"
      exit 0
    fi
    lifecycle="$(issue_lifecycle "$slot_issue")"
    if [ "$lifecycle" = unknown ]; then
      warn "could not read Issue $slot_issue state; preserved slot $slot"
      exit 0
    fi
    if [ ! -f "$SLOT_LOCK_FILE" ]; then
      printf 'preserved %s slot %s (no active lease)\n' "$lifecycle" "$slot"
      exit 0
    fi
    if bash "$SCRIPT_DIR/dev-env.sh" "$slot" down --components api,web,mcp,desktop; then
      printf 'preserved %s slot %s (database, profile and daemon identity retained)\n' "$lifecycle" "$slot"
      if [ "$(lease_phase)" = qa ]; then
        bash "$SCRIPT_DIR/dev-env.sh" "$slot" lock-release
      fi
    else
      warn "failed to stop slot $slot; its identity and lease were preserved"
    fi
  ) || true
done <<EOF
$(node -e '
  const fs = require("fs");
  const facts = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
  process.stdout.write(facts.slots.map(s => s.name).join("\n"));
' "$SLOTS_FILE")
EOF

# --- 2. orphan listeners with a cwd inside the QA root or the slot home ------

in_sweep_roots() { # $1 = cwd
  local cwd="${1:-}"
  case "$cwd" in "$QA_ROOT"|"$QA_ROOT"/*|"$SLOT_HOME"|"$SLOT_HOME"/*) return 0 ;; esac
  return 1
}

qa_dir_matches_issue() {
  [ -z "$issue_lc" ] && return 0
  matches_issue "$1"
}

self_pid=$$
protected_pid="$(ps -o ppid= -p "$self_pid" 2>/dev/null | tr -d ' ' || true)"
orphans=""
if command -v lsof >/dev/null 2>&1; then
  for pid in $(lsof -nP -iTCP -sTCP:LISTEN 2>/dev/null | awk 'NR>1 {print $2}' | sort -u); do
    [ -n "$pid" ] || continue
    [ "$pid" != "$self_pid" ] && [ "$pid" != "$protected_pid" ] || continue
    cwd="$(readlink "/proc/$pid/cwd" 2>/dev/null || true)"
    [ -n "$cwd" ] || continue
    in_sweep_roots "$cwd" || continue
    qa_dir_matches_issue "$cwd" || continue
    cmd="$(ps -o cmd= -p "$pid" 2>/dev/null || true)"
    case "$cmd" in *"multica daemon"*) continue ;; esac
    orphans="$orphans $pid"
  done
else
  warn "lsof not found; skipping orphan-listener sweep"
fi

if [ -z "$orphans" ]; then
  info "no orphan listeners with a cwd under $QA_ROOT or $SLOT_HOME"
else
  for pid in $orphans; do
    cwd="$(readlink "/proc/$pid/cwd" 2>/dev/null || echo '?')"
    desc="$(ps -o args= -p "$pid" 2>/dev/null | head -c 100 || true)"
    printf 'reported listener pid %s (cwd %s): %s\n' "$pid" "$cwd" "$desc"
  done
fi

# --- 3. docker containers referencing the issue ------------------------------

if command -v docker >/dev/null 2>&1; then
  containers="$(docker ps --format '{{.ID}}\t{{.Names}}\t{{.Image}}' 2>/dev/null || true)"
  while IFS=$'\t' read -r cid cname cimage; do
    [ -n "$cid" ] || continue
    label="$(printf '%s %s' "$cname" "$cimage" | tr '[:upper:]' '[:lower:]')"
    if [ -n "$issue_lc" ]; then
      case "$(flatten_id "$label")" in *"$issue_flat"*) ;; *) continue ;; esac
    else
      # No issue scope: only containers that name this product are in scope.
      # A bare "*qa*" match would sweep every other project's QA stack
      # (sub2api-qa*, shanghui-*-qa* all live on shared machines).
      case "$label" in *multica*|*ruyi*) ;;
        *) continue ;;
      esac
      case "$label" in *qa*) ;; *) continue ;; esac
    fi
    [ "$sweep_docker" = 1 ] || continue
    printf 'reported container %s (%s); ownership not verified\n' "$cname" "$cimage"
  done <<EOF2
$containers
EOF2
  [ -n "$containers" ] || info "no running containers matched"
else
  info "docker not found; skipping container sweep"
fi

# --- 4. QA checkouts and slot worktrees: report only, never delete -----------

# The report uses the recycle guard's own predicates (RUYI-594), report-only:
# no evidence files, no refusals — but the numbers it prints are exactly the
# numbers destroy/remove-worktree will act on, so the report can never say 0
# while the guard blocks. A branch/detached mismatch with the guard's
# sole_ref count is itself a finding.
report_worktree() { # $1 = directory that may be a git worktree/checkout
  local dir=$1 summary wt tree
  summary="dir $dir, last modified $(stat -c %y "$dir" 2>/dev/null | cut -d. -f1 || echo '?')"
  wt="$(find "$dir" -maxdepth 2 -name .git 2>/dev/null | head -1 || true)"
  if [ -n "$wt" ]; then
    tree="$(dirname "$wt")"
    RECYCLE_GUARD_EVIDENCE=skip
    RG_TOOL="qa-clean report"
    if recycle_guard_scan_worktree "$tree" report 1; then
      summary="$summary · guard: $RG_VERDICT ($RG_COUNTS)"
    else
      summary="$summary · guard: BLOCKED ($RG_COUNTS)"
      local reason
      while IFS= read -r reason; do summary="$summary"$'\n'"    $reason"; done <<< "$RG_REASONS"
    fi
  fi
  info "$summary"
}

reported=0
if [ -d "$QA_ROOT" ]; then
  step "Legacy QA checkout directories (reported only — they may hold unmerged commits)"
  for qa_dir in "$QA_ROOT"/*/; do
    [ -d "$qa_dir" ] || continue
    qa_dir_matches_issue "$qa_dir" || continue
    report_worktree "$qa_dir"
    reported=$((reported + 1))
  done
fi
if [ -d "$SLOT_HOME" ]; then
  if [ "$reported" = 0 ]; then
    step "Slot worktree directories (reported only — they may hold unmerged commits)"
  fi
  for slot_dir in "$SLOT_HOME"/*/; do
    [ -d "$slot_dir" ] || continue
    wt_root="$slot_dir/worktrees"
    [ -d "$wt_root" ] || continue
    for wt in "$wt_root"/*/; do
      [ -d "$wt" ] || continue
      qa_dir_matches_issue "$wt" || continue
      report_worktree "$wt"
      reported=$((reported + 1))
    done
  done
fi
if [ "$reported" = 0 ] && [ ! -d "$QA_ROOT" ] && [ ! -d "$SLOT_HOME" ]; then
  info "$QA_ROOT and $SLOT_HOME do not exist — nothing to sweep"
fi
if [ "$reported" != 0 ]; then
  info "verify Issue ownership, lifecycle and recoverability before reclaiming any reported checkout"
fi

[ "$assume_yes" = 1 ] && ok "qa-clean finished" || ok "qa-clean report finished (dry run)"
