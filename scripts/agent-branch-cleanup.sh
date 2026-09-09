#!/usr/bin/env bash
# Lists the task branches Multica's worktree mode left in a local repository,
# and deletes only the ones that provably carry nothing.
#
# Worktree mode delivers every task as a branch in the user's own repo. A task
# that dies mid-run leaves that branch behind, and the ones a prepare created
# before it could deliver look identical in `git branch` to the ones holding a
# whole turn's work. They are not: the baseline commit a prepare writes is a
# snapshot of the user's OWN uncommitted directory, so deleting such a branch in
# bulk can destroy work that never existed anywhere else.
#
# Hence the split this script reports and enforces:
#   SAFE      recorded as Multica's, and adds nothing to its base.
#   HAS-WORK  carries commits nothing else has. Never deleted by this script.
#   BUSY      a worktree still holds it; a task may be running in it.
#   ORPHAN    adds nothing to its base, but has no record proving whose it is.
#             Branches from before Multica recorded task branches land here, and
#             so does a branch of the user's own that happens to sit on HEAD.
#
# Default is read-only. Deletion needs --delete, and --delete without --yes
# asks per branch. --yes covers SAFE only: an ORPHAN is deleted solely after an
# explicit per-branch confirmation, or with --include-unrecorded when the
# operator has decided the whole class is theirs to drop. Nothing here ever
# deletes a branch with unique commits — the listing prints the `git log` that
# shows what it holds, and stops.
#
# Usage:
#   scripts/agent-branch-cleanup.sh [--repo <path>] [--base <ref>]
#   scripts/agent-branch-cleanup.sh --delete [--yes] [--include-unrecorded] [--repo <path>]
set -euo pipefail

# git's own prose is not parsed here, but the porcelain formats below are
# stable only with the locale pinned — same reason execenv pins it.
export LC_ALL=C
export LANGUAGE=

repo="$PWD"
base=""
prefix="agent/"
do_delete=0
assume_yes=0
include_unrecorded=0

usage() {
  cat >&2 <<'USAGE'
Usage: scripts/agent-branch-cleanup.sh [options]

  --repo <path>          repository to inspect (default: current directory)
  --base <ref>           ref unique commits are counted against (default: the repo's HEAD branch)
  --prefix <str>         branch prefix to consider (default: agent/)
  --delete               delete the branches classified SAFE
  --yes                  with --delete, do not ask per SAFE branch
  --include-unrecorded   also offer the ORPHAN branches (no ownership record)
  -h, --help             this text
USAGE
}

while [ $# -gt 0 ]; do
  case "$1" in
    --repo) repo="${2:?--repo needs a path}"; shift 2 ;;
    --base) base="${2:?--base needs a ref}"; shift 2 ;;
    --prefix) prefix="${2:?--prefix needs a string}"; shift 2 ;;
    --delete) do_delete=1; shift ;;
    --yes) assume_yes=1; shift ;;
    --include-unrecorded) include_unrecorded=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown argument: $1" >&2; usage; exit 2 ;;
  esac
done

if ! git -C "$repo" rev-parse --git-dir >/dev/null 2>&1; then
  echo "Not a git repository: $repo" >&2
  exit 1
fi

if [ -z "$base" ]; then
  base="$(git -C "$repo" symbolic-ref --quiet --short HEAD 2>/dev/null || echo HEAD)"
fi
if ! git -C "$repo" rev-parse --verify --quiet "$base" >/dev/null; then
  echo "Base ref does not resolve: $base" >&2
  exit 1
fi

# Branches a worktree currently holds. A task may still be running in one, and
# git would refuse the delete anyway — reporting it is the useful part.
checked_out_file="$(mktemp)"
trap 'rm -f "$checked_out_file"' EXIT
git -C "$repo" worktree list --porcelain \
  | sed -n 's|^branch refs/heads/||p' > "$checked_out_file"

is_checked_out() {
  grep -Fxq "$1" "$checked_out_file"
}

# The hidden ref Multica writes next to every branch it owns. Its absence means
# the branch is someone else's, or predates the record — either way not ours to
# delete.
has_record() {
  git -C "$repo" rev-parse --verify --quiet "refs/multica/local-state/$1" >/dev/null
}

record_owner() {
  local ref
  ref="$(git -C "$repo" rev-parse --verify --quiet "refs/multica/local-state/$1" || true)"
  [ -n "$ref" ] || return 0
  git -C "$repo" log -1 --format=%B "$ref" 2>/dev/null \
    | sed -n -e 's/^Multica-Task: /task=/p' -e 's/^Multica-Conversation: /conversation=/p' \
    | paste -sd' ' -
}

# for-each-ref matches a pattern without wildcards as a full path PREFIX, and a
# `*` in it never crosses a `/`. `agent/` therefore has to be passed through as
# is: turning it into `agent/*` would miss every agent/<name>/<task> branch,
# which is all of them.
pattern="refs/heads/${prefix}"
case "$prefix" in
  */) ;;
  *) pattern="${pattern}*" ;;
esac
branches="$(git -C "$repo" for-each-ref --format='%(refname:short)' "$pattern")"
if [ -z "$branches" ]; then
  echo "No branches under '${prefix}' in $repo"
  exit 0
fi

safe_list=()
orphan_list=()
kept=0

printf '%-52s %8s  %-9s %s\n' BRANCH UNIQUE STATE DETAIL
while IFS= read -r branch; do
  [ -n "$branch" ] || continue
  unique="$(git -C "$repo" rev-list --count "$base..$branch")"
  detail="$(record_owner "$branch")"
  if is_checked_out "$branch"; then
    state="BUSY"
    detail="checked out by a worktree${detail:+; $detail}"
    kept=$((kept + 1))
  elif [ "$unique" -gt 0 ]; then
    state="HAS-WORK"
    detail="inspect: git -C $repo log $base..$branch${detail:+ | $detail}"
    kept=$((kept + 1))
  elif ! has_record "$branch"; then
    state="ORPHAN"
    detail="no ownership record; needs an explicit confirmation"
    orphan_list+=("$branch")
  else
    state="SAFE"
    safe_list+=("$branch")
  fi
  printf '%-52s %8s  %-9s %s\n' "$branch" "$unique" "$state" "$detail"
done <<< "$branches"

echo
echo "SAFE: ${#safe_list[@]}   ORPHAN: ${#orphan_list[@]}   kept: $kept   base: $base"

delete_list=("${safe_list[@]}")
if [ "$include_unrecorded" = "1" ]; then
  delete_list+=("${orphan_list[@]}")
fi

if [ "$do_delete" != "1" ]; then
  if [ "${#safe_list[@]}" -gt 0 ]; then
    echo "Re-run with --delete to remove the SAFE branches."
  fi
  if [ "${#orphan_list[@]}" -gt 0 ]; then
    echo "ORPHAN branches are offered only with --delete --include-unrecorded, one confirmation each."
  fi
  exit 0
fi

if [ "${#delete_list[@]}" -eq 0 ]; then
  echo "Nothing to delete."
  exit 0
fi

# Whether this branch may go without asking. --yes speaks for the SAFE class
# only: an ORPHAN has no record proving Multica made it, so the operator
# confirms it one at a time however the run was invoked.
auto_ok() {
  [ "$assume_yes" = "1" ] || return 1
  local candidate=$1 orphan
  for orphan in ${orphan_list[@]+"${orphan_list[@]}"}; do
    [ "$orphan" = "$candidate" ] && return 1
  done
  return 0
}

deleted=0
for branch in "${delete_list[@]}"; do
  if ! auto_ok "$branch"; then
    printf 'Delete %s ? [y/N] ' "$branch"
    # The terminal when there is one, so the prompt still works with the
    # listing piped somewhere; plain stdin otherwise, which is how a script or
    # a test answers.
    if [ -t 0 ] && [ -r /dev/tty ]; then
      read -r answer </dev/tty || answer=""
    else
      read -r answer || answer=""
    fi
    case "$answer" in
      y|Y|yes|YES) ;;
      *) echo "  skipped"; continue ;;
    esac
  fi
  # -d, never -D: it refuses anything not merged into its upstream or HEAD, so
  # a branch that gained a commit between the listing above and this line is
  # kept rather than silently destroyed.
  if git -C "$repo" branch -d "$branch" >/dev/null 2>&1; then
    git -C "$repo" update-ref -d "refs/multica/local-state/$branch" 2>/dev/null || true
    echo "  deleted $branch"
    deleted=$((deleted + 1))
  else
    echo "  refused by git, kept: $branch" >&2
  fi
done

echo "Deleted $deleted of ${#delete_list[@]} candidate branches."
