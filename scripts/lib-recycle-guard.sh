#!/usr/bin/env bash
# Recycle guard (RUYI-594): shared scan predicates + evidence for every verb
# that removes a git carrier (slot destroy, dev-env gc, remove-worktree.sh,
# qa-clean reporting). Sourced by scripts/dev-env.sh, scripts/remove-worktree.sh
# and scripts/qa-clean.sh; the daemon side lives in
# server/internal/daemon/execenv/gc_guard.go and must keep the same routing.
#
# Three-level disposition, identical to the daemon guard:
#   pass      L0 — nothing scanned would lose its last reference: recycle.
#   evidence  L1 — everything referenced survives elsewhere (shared .git),
#                  but the scan saw content worth a paper trail: recycle, keep
#                  evidence.
#   blocked   L2 — the target holds the last reference to commits/stash/dirty
#                  tracked work: refuse. A human preserves the work, then
#                  re-runs once with MULTICA_RECYCLE_OVERRIDE=1 (one-shot,
#                  recorded in the evidence file).
#
# Routing is topology-aware: a LINKED worktree (its .git is a file pointing at
# a surviving repository) loses only its checked-out state — stash and pushed
# history survive in the shared object store, so they are notes, not refusals.
# A STANDALONE checkout (its .git is a directory that dies with it) takes its
# whole history with it, so any sole-reference commit, stash, dirty tracked
# file or unpushed commit blocks. A git command that FAILS during a scan is
# fail-closed: an unreadable carrier is exactly the shape that used to slip
# through as "empty output" — the only legitimate failure is an unborn HEAD
# (no commits yet, certified by `git status` still reading the repository).
#
# Kill switch: MULTICA_GC_GUARD_ENABLED=false|0|off restores the legacy
# unconditional behaviour (no scanning, no evidence, no refusals) on BOTH
# tracks. Evidence root: ${MULTICA_RECYCLE_EVIDENCE_DIR:-~/.multica/recycle-evidence}
# (the daemon writes <WorkspacesRoot>/.recycle-evidence/ in JSON; same fields,
# same TTL). Evidence TTL: ${MULTICA_RECYCLE_EVIDENCE_TTL_DAYS:-30} days,
# swept by recycle_guard_sweep — the only code path that ever deletes an
# evidence file.
#
# This library prints nothing on its own; callers read the RG_* globals and
# render through their own output helpers.

RECYCLE_GUARD_EVIDENCE_ROOT="${MULTICA_RECYCLE_EVIDENCE_DIR:-$HOME/.multica/recycle-evidence}"
RECYCLE_GUARD_EVIDENCE_TTL_DAYS="${MULTICA_RECYCLE_EVIDENCE_TTL_DAYS:-30}"
RECYCLE_GUARD_LIST_CAP=50

# Scan results (reset by recycle_guard_scan_worktree):
RG_VERDICT=""      # pass | evidence | blocked | disabled
RG_ALLOWED=0       # 1 = the caller may proceed with the recycle
RG_OVERRIDE=0      # 1 = MULTICA_RECYCLE_OVERRIDE released a blocked verdict
RG_REASONS=""      # newline-joined refusal reasons (blocked only)
RG_NOTES=""        # newline-joined observations worth surfacing
RG_COUNTS=""       # one-line per-predicate counts, aligned with the evidence file
RG_EVIDENCE_FILE=""
# Set RECYCLE_GUARD_EVIDENCE=skip to scan without writing an evidence file
# (report-only callers like qa-clean.sh); the verdict and counts still land
# in the RG_* globals.

recycle_guard_enabled() {
  case "${MULTICA_GC_GUARD_ENABLED:-true}" in
    false|FALSE|0|off|OFF) return 1 ;;
    *) return 0 ;;
  esac
}

_rg_block() { RG_VERDICT=blocked; RG_REASONS="${RG_REASONS:+$RG_REASONS
}$1"; }
_rg_note() { RG_NOTES="${RG_NOTES:+$RG_NOTES
}$1"; }
# _rg_note_ev notes AND raises the verdict to evidence (L1): content worth a
# paper trail, but nothing referenced dies with the recycle. Never downgrades
# an already-blocked verdict.
_rg_note_ev() { [ "$RG_VERDICT" = blocked ] || RG_VERDICT=evidence; _rg_note "$1"; }

# _rg_run <dir> <PREFIX> <git-args...>
# Runs one git predicate, capturing the exact command, exit code and stdout
# into <PREFIX>_CMD / <PREFIX>_RC / <PREFIX>_OUT. Failures are recorded, never
# swallowed into an empty string — that equivalence was the fail-open.
_rg_run() {
  local dir=$1 prefix=$2
  shift 2
  local arg cmd="git -C $(printf '%q' "$dir")"
  for arg in "$@"; do cmd="$cmd $(printf '%q' "$arg")"; done
  printf -v "${prefix}_CMD" '%s' "$cmd"
  if out="$(git -C "$dir" "$@" 2>/dev/null)"; then
    printf -v "${prefix}_RC" '0'
  else
    printf -v "${prefix}_RC" '1'
    out=""
  fi
  printf -v "${prefix}_OUT" '%s' "$out"
  return 0
}

# _rg_count <PREFIX> — line count of a porcelain/list output.
_rg_count() {
  local prefix=$1 out_ref="${1}_OUT"
  local out="${!out_ref}"
  if [ -z "$out" ]; then
    printf '0'
  else
    printf '%s\n' "$out" | wc -l | tr -d ' '
  fi
}

recycle_guard_evidence_write() { # <target> <kind> <topology> <dry_run> <predicates-file>
  local target=$1 kind=$2 topology=$3 dry_run=$4 preds_file=$5
  mkdir -p "$RECYCLE_GUARD_EVIDENCE_ROOT"
  chmod 700 "$RECYCLE_GUARD_EVIDENCE_ROOT" 2>/dev/null || true
  RG_EVIDENCE_FILE="$RECYCLE_GUARD_EVIDENCE_ROOT/$(date +%Y%m%dT%H%M%S.%N)_${kind}_$(basename "$target")_$$.txt"
  {
    printf 'recycle-evidence v1\n'
    printf 'timestamp: %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
    printf 'tool: %s\n' "${RG_TOOL:-unknown}"
    printf 'target: %s\n' "$target"
    printf 'kind: %s\n' "$kind"
    printf 'topology: %s\n' "$topology"
    printf 'dry_run: %s\n' "$([ "$dry_run" = 1 ] && printf true || printf false)"
    printf 'override: %s\n' "$([ "$RG_OVERRIDE" = 1 ] && printf true || printf false)"
    printf 'verdict: %s\n' "$RG_VERDICT"
    if [ -n "$RG_REASONS" ]; then
      printf 'reasons:\n'
      printf '%s\n' "$RG_REASONS" | sed 's/^/  - /'
    fi
    if [ -n "$RG_NOTES" ]; then
      printf 'notes:\n'
      printf '%s\n' "$RG_NOTES" | sed 's/^/  - /'
    fi
    cat "$preds_file"
  } > "$RG_EVIDENCE_FILE"
  chmod 600 "$RG_EVIDENCE_FILE" 2>/dev/null || true
}

# recycle_guard_scan_worktree <dir> <kind> <dry_run:0|1>
# Scans one git carrier about to be recycled. Sets the RG_* globals and writes
# an evidence file (unless the guard is disabled, or RECYCLE_GUARD_EVIDENCE=skip
# for report-only callers). Returns 0 when the caller may recycle.
recycle_guard_scan_worktree() {
  local dir=$1 kind=$2 dry_run=${3:-0}
  RG_VERDICT=pass; RG_ALLOWED=1; RG_OVERRIDE=0
  RG_REASONS=""; RG_NOTES=""; RG_COUNTS=""; RG_EVIDENCE_FILE=""

  if ! recycle_guard_enabled; then
    RG_VERDICT=disabled
    return 0
  fi
  [ "${MULTICA_RECYCLE_OVERRIDE:-}" = "1" ] && RG_OVERRIDE=1

  local topology=standalone
  if [ -f "$dir/.git" ]; then
    topology=linked
  elif [ ! -d "$dir/.git" ]; then
    topology=none
  fi

  _rg_run "$dir" SOLE rev-list --count HEAD --not --branches --remotes
  _rg_run "$dir" UNPUSHED rev-list --count HEAD --not --remotes
  _rg_run "$dir" STASH stash list
  _rg_run "$dir" DIRTY status --porcelain --untracked-files=no
  _rg_run "$dir" UNTRACKED status --porcelain

  local sole_n unpushed_n stash_n dirty_n untracked_n
  # rev-list --count emits the number itself; porcelain/stash emit lines.
  case "$SOLE_OUT" in ''|*[!0-9]*) sole_n=0 ;; *) sole_n=$SOLE_OUT ;; esac
  case "$UNPUSHED_OUT" in ''|*[!0-9]*) unpushed_n=0 ;; *) unpushed_n=$UNPUSHED_OUT ;; esac
  stash_n=$(_rg_count STASH); dirty_n=$(_rg_count DIRTY)
  untracked_n=$(_rg_count UNTRACKED)

  local preds
  preds="$(mktemp)"
  {
    printf 'predicates:\n'
    printf '  predicate: sole_ref cmd="%s" exit=%s count=%s\n' "$SOLE_CMD" "$SOLE_RC" "$sole_n"
    printf '  predicate: unpushed cmd="%s" exit=%s count=%s\n' "$UNPUSHED_CMD" "$UNPUSHED_RC" "$unpushed_n"
    printf '  predicate: stash cmd="%s" exit=%s count=%s\n' "$STASH_CMD" "$STASH_RC" "$stash_n"
    printf '  predicate: dirty_tracked cmd="%s" exit=%s count=%s\n' "$DIRTY_CMD" "$DIRTY_RC" "$dirty_n"
    printf '  predicate: untracked cmd="%s" exit=%s count=%s\n' "$UNTRACKED_CMD" "$UNTRACKED_RC" "$untracked_n"
  } > "$preds"

  # Fail-closed: every predicate must have read the repository. An unborn HEAD
  # (no commit ever made) is the one legitimate rev-list failure — certified
  # by `git status` still reading the same repository.
  local scan_failed=0
  if [ "$DIRTY_RC" != "0" ]; then
    scan_failed=1
  elif [ "$SOLE_RC" != "0" ] && [ "$UNPUSHED_RC" != "0" ]; then
    : # unborn HEAD: both rev-list predicates are NA, counts stay 0
  elif [ "$SOLE_RC" != "0" ] || [ "$UNPUSHED_RC" != "0" ]; then
    scan_failed=1
  fi

  if [ "$scan_failed" = 1 ]; then
    _rg_block "git scan of $dir failed (fail-closed): the carrier cannot be read, so its contents cannot be certified recyclable."
  elif [ "$topology" = "standalone" ]; then
    [ "$sole_n" -gt 0 ] && _rg_block "$dir holds $sole_n commit(s) referenced by no branch or remote; the standalone .git dies with the directory and takes them along."
    [ "$stash_n" -gt 0 ] && _rg_block "$dir holds $stash_n stash entries; the standalone .git dies with the directory."
    [ "$dirty_n" -gt 0 ] && _rg_block "$dir holds $dirty_n modified tracked file(s); deleting it loses the changes."
    [ "$unpushed_n" -gt 0 ] && [ "$sole_n" = 0 ] && _rg_block "$dir holds $unpushed_n unpushed commit(s) on its branch; the standalone .git dies with the directory."
  else
    [ "$sole_n" -gt 0 ] && _rg_block "$dir holds $sole_n commit(s) referenced by no branch or remote (detached HEAD); removing the worktree orphans them."
    # linked topology: stash and branch commits survive in the shared .git.
    [ "$stash_n" -gt 0 ] && _rg_note_ev "$stash_n stash entries survive in the shared repository .git"
    [ "$unpushed_n" -gt 0 ] && _rg_note_ev "$unpushed_n unpushed commit(s) survive in the shared repository .git"
  fi
  [ "$untracked_n" -gt 0 ] && _rg_note "$untracked_n untracked file(s) (report only; routing decided by tracked content)"

  # The concrete answer to "what would have been lost": sole-ref commit list.
  if [ "$sole_n" -gt 0 ] && [ "$SOLE_RC" = "0" ]; then
    {
      printf '  sole_ref_list:\n'
      git -C "$dir" rev-list HEAD --not --branches --remotes 2>/dev/null | head -n "$RECYCLE_GUARD_LIST_CAP" | \
        while IFS= read -r sha; do
          printf '    %s %s\n' "$sha" "$(git -C "$dir" log -1 --format=%s "$sha" 2>/dev/null)"
        done
      local total
      total="$(git -C "$dir" rev-list --count HEAD --not --branches --remotes 2>/dev/null || printf '%s' "$sole_n")"
      [ "$total" -gt "$RECYCLE_GUARD_LIST_CAP" ] && printf '    ... (%s more)\n' "$((total - RECYCLE_GUARD_LIST_CAP))"
    } >> "$preds"
  fi

  [ "$RG_VERDICT" = blocked ] && RG_ALLOWED=0
  RG_COUNTS="sole_ref=$sole_n unpushed=$unpushed_n stash=$stash_n dirty_tracked=$dirty_n untracked=$untracked_n"
  if [ "$RG_ALLOWED" = 0 ] && [ "$RG_OVERRIDE" = 1 ]; then
    RG_ALLOWED=1
    _rg_note "blocked verdict released by MULTICA_RECYCLE_OVERRIDE=1; preservation is the operator's recorded responsibility"
  fi

  if [ "${RECYCLE_GUARD_EVIDENCE:-write}" != "skip" ]; then
    recycle_guard_evidence_write "$dir" "$kind" "$topology" "$dry_run" "$preds"
  fi
  rm -f "$preds"
  [ "$RG_ALLOWED" = 1 ]
}

# recycle_guard_sweep — delete evidence past the TTL. The ONLY deleter.
recycle_guard_sweep() {
  [ -d "$RECYCLE_GUARD_EVIDENCE_ROOT" ] || return 0
  local cutoff_epoch
  cutoff_epoch="$(date -u -d "$RECYCLE_GUARD_EVIDENCE_TTL_DAYS days ago" '+%s' 2>/dev/null)" || return 0
  [ -n "$cutoff_epoch" ] || return 0
  find "$RECYCLE_GUARD_EVIDENCE_ROOT" -type f ! -newermt "@$cutoff_epoch" -delete 2>/dev/null || true
}
