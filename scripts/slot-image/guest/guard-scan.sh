#!/usr/bin/env bash
# guard-scan.sh — guest-side recycle guard baked into multica-slot-base images.
# Self-contained port of scripts/lib-recycle-guard.sh (RUYI-594): same
# predicates, same topology-aware routing, same three-level disposition and
# the same recycle-evidence v1 file format, so evidence written inside a pool
# VM is byte-comparable with the local slot track (~/.multica/recycle-evidence).
#
# Usage:
#   guard-scan.sh <dir> <kind> <dry_run:0|1>
#     <dir>   a git carrier, or a root whose subdirectories are carriers
#             (every carrier found is scanned; any blocked verdict blocks).
#     <kind>  free-form label for the evidence file (e.g. slot-destroy, gc).
# Output (machine-readable lines on stdout):
#   verdict: pass | evidence | blocked | disabled
#   counts:  sole_ref=N unpushed=N stash=N dirty_tracked=N untracked=N
#   evidence: <evidence file path>
#   carrier: <path> <verdict>        (one per scanned carrier)
# Exit codes: 0 the caller may recycle · 1 blocked (fail-closed included).
#
# Overrides match the local track exactly: MULTICA_RECYCLE_OVERRIDE=1 releases
# one blocked verdict (recorded in evidence as override: true with the original
# verdict); MULTICA_GC_GUARD_ENABLED=false|0|off disables scanning entirely.
# A git predicate that cannot read the repository is fail-closed: an unreadable
# carrier is exactly the shape that used to slip through as "empty output".
set -uo pipefail

EVIDENCE_ROOT="${MULTICA_RECYCLE_EVIDENCE_DIR:-$HOME/.multica/recycle-evidence}"
LIST_CAP=50

VERDICT="pass"
OVERRIDE=0
REASONS=""
NOTES=""

guard_enabled() {
  case "${MULTICA_GC_GUARD_ENABLED:-true}" in
    false|FALSE|0|off|OFF) return 1 ;;
    *) return 0 ;;
  esac
}

_block() { VERDICT=blocked; REASONS="${REASONS:+$REASONS
}$1"; }
_note() { NOTES="${NOTES:+$NOTES
}$1"; }
_note_ev() { [ "$VERDICT" = blocked ] || VERDICT=evidence; _note "$1"; }

# _pred <dir> <PREFIX> <git-args...> — run one predicate, record cmd/rc/out.
_pred() {
  local dir=$1 prefix=$2
  shift 2
  local out
  if out="$(git -C "$dir" "$@" 2>/dev/null)"; then
    printf -v "${prefix}_RC" '0'
  else
    printf -v "${prefix}_RC" '1'
    out=""
  fi
  printf -v "${prefix}_OUT" '%s' "$out"
}

_count() {
  local out_ref="${1}_OUT"
  local out
  out="${!out_ref}"
  if [ -z "$out" ]; then printf '0'; else printf '%s\n' "$out" | wc -l | tr -d ' '; fi
}

# scan_one <dir> <kind> <dry_run> — scan a single carrier; returns 0 allowed.
scan_one() {
  local dir=$1 kind=$2 dry_run=$3
  VERDICT=pass; OVERRIDE=0; REASONS=""; NOTES=""

  local topology=standalone
  if [ -f "$dir/.git" ]; then
    topology=linked
  elif [ ! -d "$dir/.git" ]; then
    topology=none
  fi

  _pred "$dir" SOLE rev-list --count HEAD --not --branches --remotes
  _pred "$dir" UNPUSHED rev-list --count HEAD --not --remotes
  _pred "$dir" STASH stash list
  _pred "$dir" DIRTY status --porcelain --untracked-files=no
  _pred "$dir" UNTRACKED status --porcelain

  local sole_n unpushed_n stash_n dirty_n untracked_n
  case "${SOLE_OUT:-}" in ''|*[!0-9]*) sole_n=0 ;; *) sole_n=$SOLE_OUT ;; esac
  case "${UNPUSHED_OUT:-}" in ''|*[!0-9]*) unpushed_n=0 ;; *) unpushed_n=$UNPUSHED_OUT ;; esac
  stash_n=$(_count STASH); dirty_n=$(_count DIRTY); untracked_n=$(_count UNTRACKED)

  local preds
  preds="$(mktemp)"
  {
    printf 'predicates:\n'
    printf '  predicate: sole_ref cmd="git -C %s rev-list --count HEAD --not --branches --remotes" exit=%s count=%s\n' "$(printf '%q' "$dir")" "${SOLE_RC:-1}" "$sole_n"
    printf '  predicate: unpushed cmd="git -C %s rev-list --count HEAD --not --remotes" exit=%s count=%s\n' "$(printf '%q' "$dir")" "${UNPUSHED_RC:-1}" "$unpushed_n"
    printf '  predicate: stash cmd="git -C %s stash list" exit=%s count=%s\n' "$(printf '%q' "$dir")" "${STASH_RC:-1}" "$stash_n"
    printf '  predicate: dirty_tracked cmd="git -C %s status --porcelain --untracked-files=no" exit=%s count=%s\n' "$(printf '%q' "$dir")" "${DIRTY_RC:-1}" "$dirty_n"
    printf '  predicate: untracked cmd="git -C %s status --porcelain" exit=%s count=%s\n' "$(printf '%q' "$dir")" "${UNTRACKED_RC:-1}" "$untracked_n"
  } > "$preds"

  # Fail-closed: every predicate must have read the repository. An unborn HEAD
  # is the one legitimate rev-list failure, certified by git status reading.
  local scan_failed=0
  if [ "${DIRTY_RC:-1}" != "0" ]; then
    scan_failed=1
  elif [ "${SOLE_RC:-1}" != "0" ] && [ "${UNPUSHED_RC:-1}" != "0" ]; then
    : # unborn HEAD: both rev-list predicates NA, counts stay 0
  elif [ "${SOLE_RC:-1}" != "0" ] || [ "${UNPUSHED_RC:-1}" != "0" ]; then
    scan_failed=1
  fi

  if [ "$scan_failed" = 1 ]; then
    _block "git scan of $dir failed (fail-closed): the carrier cannot be read, so its contents cannot be certified recyclable."
  elif [ "$topology" = "standalone" ]; then
    [ "$sole_n" -gt 0 ] && _block "$dir holds $sole_n commit(s) referenced by no branch or remote; the standalone .git dies with the directory and takes them along."
    [ "$stash_n" -gt 0 ] && _block "$dir holds $stash_n stash entries; the standalone .git dies with the directory."
    [ "$dirty_n" -gt 0 ] && _block "$dir holds $dirty_n modified tracked file(s); deleting it loses the changes."
    [ "$unpushed_n" -gt 0 ] && [ "$sole_n" = 0 ] && _block "$dir holds $unpushed_n unpushed commit(s) on its branch; the standalone .git dies with the directory."
  else
    [ "$sole_n" -gt 0 ] && _block "$dir holds $sole_n commit(s) referenced by no branch or remote (detached HEAD); removing the worktree orphans them."
    [ "$stash_n" -gt 0 ] && _note_ev "$stash_n stash entries survive in the shared repository .git"
    [ "$unpushed_n" -gt 0 ] && _note_ev "$unpushed_n unpushed commit(s) survive in the shared repository .git"
  fi
  [ "$untracked_n" -gt 0 ] && _note "$untracked_n untracked file(s) (report only; routing decided by tracked content)"

  if [ "$sole_n" -gt 0 ] && [ "${SOLE_RC:-1}" = "0" ]; then
    {
      printf '  sole_ref_list:\n'
      git -C "$dir" rev-list HEAD --not --branches --remotes 2>/dev/null | head -n "$LIST_CAP" | \
        while IFS= read -r sha; do
          printf '    %s %s\n' "$sha" "$(git -C "$dir" log -1 --format=%s "$sha" 2>/dev/null)"
        done
      local total
      total="$(git -C "$dir" rev-list --count HEAD --not --branches --remotes 2>/dev/null || printf '%s' "$sole_n")"
      [ "$total" -gt "$LIST_CAP" ] && printf '    ... (%s more)\n' "$((total - LIST_CAP))"
    } >> "$preds"
  fi

  [ "${MULTICA_RECYCLE_OVERRIDE:-}" = "1" ] && OVERRIDE=1
  local allowed=1
  [ "$VERDICT" = blocked ] && allowed=0
  if [ "$allowed" = 0 ] && [ "$OVERRIDE" = 1 ]; then
    allowed=1
    _note "blocked verdict released by MULTICA_RECYCLE_OVERRIDE=1; preservation is the operator's recorded responsibility"
  fi

  mkdir -p "$EVIDENCE_ROOT"
  chmod 700 "$EVIDENCE_ROOT" 2>/dev/null || true
  local ev_file="$EVIDENCE_ROOT/$(date +%Y%m%dT%H%M%S.%N)_${kind}_$(basename "$dir")_$$.txt"
  {
    printf 'recycle-evidence v1\n'
    printf 'timestamp: %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
    printf 'tool: %s\n' "${RG_TOOL:-slot-pool-guard}"
    printf 'target: %s\n' "$dir"
    printf 'kind: %s\n' "$kind"
    printf 'topology: %s\n' "$topology"
    printf 'dry_run: %s\n' "$([ "$dry_run" = 1 ] && printf true || printf false)"
    printf 'override: %s\n' "$([ "$OVERRIDE" = 1 ] && printf true || printf false)"
    printf 'verdict: %s\n' "$VERDICT"
    if [ -n "$REASONS" ]; then
      printf 'reasons:\n'
      printf '%s\n' "$REASONS" | sed 's/^/  - /'
    fi
    if [ -n "$NOTES" ]; then
      printf 'notes:\n'
      printf '%s\n' "$NOTES" | sed 's/^/  - /'
    fi
    cat "$preds"
  } > "$ev_file"
  chmod 600 "$ev_file" 2>/dev/null || true
  rm -f "$preds"

  printf 'counts: sole_ref=%s unpushed=%s stash=%s dirty_tracked=%s untracked=%s\n' \
    "$sole_n" "$unpushed_n" "$stash_n" "$dirty_n" "$untracked_n"
  printf 'carrier: %s %s\n' "$dir" "$VERDICT"
  printf 'evidence: %s\n' "$ev_file"
  [ "$allowed" = 1 ]
}

main() {
  local target=${1:-}; local kind=${2:-unspecified}; local dry_run=${3:-0}
  if [ -z "$target" ] || [ ! -d "$target" ]; then
    printf 'usage: guard-scan.sh <dir> <kind> <dry_run:0|1>\n' >&2
    exit 1
  fi

  if ! guard_enabled; then
    printf 'verdict: disabled\n'
    printf 'counts: sole_ref=0 unpushed=0 stash=0 dirty_tracked=0 untracked=0\n'
    exit 0
  fi

  # Collect carriers: the target itself, or every carrier below it.
  local carriers=()
  if [ -e "$target/.git" ]; then
    carriers+=("$target")
  else
    while IFS= read -r gitpath; do
      carriers+=("$(dirname "$gitpath")")
    done < <(find "$target" -maxdepth 5 -name .git \( -type d -o -type f \) 2>/dev/null | sort)
  fi

  if [ "${#carriers[@]}" = 0 ]; then
    printf 'verdict: pass\n'
    printf 'counts: sole_ref=0 unpushed=0 stash=0 dirty_tracked=0 untracked=0\n'
    printf 'carrier: %s none (no git carriers found)\n' "$target"
    exit 0
  fi

  local allowed=1 overall="pass"
  local c out cv
  for c in "${carriers[@]}"; do
    out="$(scan_one "$c" "$kind" "$dry_run")" || allowed=0
    printf '%s\n' "$out"
    cv="$(printf '%s\n' "$out" | sed -n 's/^carrier: \S* //p' | head -1)"
    case "$cv" in
      blocked) overall="blocked" ;;
      evidence) [ "$overall" != "blocked" ] && overall="evidence" ;;
    esac
  done

  if [ "$allowed" = 1 ]; then
    printf 'verdict: %s\n' "$overall"
    exit 0
  fi
  printf 'verdict: blocked\n'
  exit 1
}

main "$@"
