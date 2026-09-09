#!/usr/bin/env bash
# Lists the task branches Multica's worktree mode left in a local repository,
# and deletes only the ones that provably carry nothing and belong to a task
# that has provably finished.
#
# Worktree mode delivers every task as a branch in the user's own repo. A task
# that dies mid-run leaves that branch behind, and the ones a prepare created
# before it could deliver look identical in `git branch` to the ones holding a
# whole turn's work. They are not: the baseline commit a prepare writes is a
# snapshot of the user's OWN uncommitted directory, so deleting such a branch in
# bulk can destroy work that never existed anywhere else.
#
# Two independent facts are therefore reported per branch, in two columns:
#
#   TASK_STATUS    the task's real lifecycle status, read from the platform for
#                  the task id recorded with the branch: queued / dispatched /
#                  running / waiting_local_directory / completed / failed /
#                  cancelled. NONE when no record names a task, UNKNOWN when the
#                  status could not be established — a failed query and a task
#                  the platform no longer lists both land here, and neither is
#                  ever reported as a finished task.
#
#   CLEANUP_STATE  what this script may do with the branch:
#     SAFE      recorded as Multica's, adds nothing to its base, and its task
#               reached a terminal status. The only class --yes may delete.
#     HAS-WORK  carries commits nothing else has. Never deleted by this script.
#     BUSY      a worktree still holds it; a task may be running in it.
#     ACTIVE    recorded and empty, but its task has NOT finished. Never
#               deleted: the run that owns it can still deliver into it.
#     UNPROVEN  empty, but nothing proves it is finished — no ownership record,
#               a record from before the task id was written (TASK_STATUS NONE),
#               or a task status that could not be established. Deleted solely
#               after an explicit per-branch confirmation.
#
# Default is read-only. Deletion needs --delete, and --delete without --yes asks
# per branch. --yes covers SAFE only; UNPROVEN additionally needs
# --include-unrecorded and still asks one branch at a time. Nothing here ever
# deletes a branch with unique commits, and nothing deletes on an unknown task
# status — the listing prints the `git log` that shows what a branch holds, and
# stops.
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
status_command=""

usage() {
  cat >&2 <<'USAGE'
Usage: scripts/agent-branch-cleanup.sh [options]

  --repo <path>           repository to inspect (default: current directory)
  --base <ref>            ref unique commits are counted against (default: the repo's HEAD branch)
  --prefix <str>          branch prefix to consider (default: agent/)
  --status-command <cmd>  command called as `<cmd> <agent-id>` to list task statuses,
                          one `<task-id> <status>` per line (default: the multica CLI;
                          pass `none` to skip the lookup, leaving every status UNKNOWN)
  --delete                delete the branches classified SAFE
  --yes                   with --delete, do not ask per SAFE branch
  --include-unrecorded    also offer the UNPROVEN branches, one confirmation each
  -h, --help              this text
USAGE
}

while [ $# -gt 0 ]; do
  case "$1" in
    --repo) repo="${2:?--repo needs a path}"; shift 2 ;;
    --base) base="${2:?--base needs a ref}"; shift 2 ;;
    --prefix) prefix="${2:?--prefix needs a string}"; shift 2 ;;
    --status-command) status_command="${2:?--status-command needs a command}"; shift 2 ;;
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

work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT

# Branches a worktree currently holds. A task may still be running in one, and
# git would refuse the delete anyway — reporting it is the useful part.
checked_out_file="$work_dir/checked-out"
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

# One trailer out of the branch record: the task and agent ids the status lookup
# needs, and nothing else.
record_trailer() {
  local ref
  ref="$(git -C "$repo" rev-parse --verify --quiet "refs/multica/local-state/$1" || true)"
  [ -n "$ref" ] || return 0
  git -C "$repo" log -1 --format=%B "$ref" 2>/dev/null \
    | sed -n "s/^$2: //p" | head -1
}

# The default status source. The CLI is the only supported way to reach the
# platform (workspace rule), and it is asked for the OWNING agent's tasks, which
# is the one listing that contains the recorded task id.
multica_task_statuses() {
  local agent=$1 json
  command -v multica >/dev/null 2>&1 || return 1
  json="$(multica agent tasks "$agent" --output json 2>/dev/null)" || return 1
  [ -n "$json" ] || return 1
  if command -v jq >/dev/null 2>&1; then
    printf '%s' "$json" | jq -r '.[] | "\(.id) \(.status)"'
  elif command -v python3 >/dev/null 2>&1; then
    printf '%s' "$json" | python3 -c 'import json,sys
for t in json.load(sys.stdin):
    print(t.get("id",""), t.get("status",""))'
  else
    return 1
  fi
}

# Fills the per-agent status cache once. A lookup that fails leaves a marker
# instead of an empty listing: "the query broke" and "this agent has no tasks"
# must not collapse into the same answer, or a broken query would read as proof
# that every branch is finished.
load_agent_statuses() {
  local agent=$1 out="$work_dir/statuses-$agent"
  [ -e "$out" ] || [ -e "$out.failed" ] || {
    if [ "$status_command" = "none" ]; then
      : > "$out.failed"
    elif "${status_command:-multica_task_statuses}" "$agent" > "$out.tmp" 2>/dev/null; then
      mv "$out.tmp" "$out"
    else
      rm -f "$out.tmp"
      : > "$out.failed"
    fi
  }
}

# The task's real lifecycle status, or UNKNOWN. Never invents a terminal status.
task_status_of() {
  local agent=$1 task=$2 out status
  load_agent_statuses "$agent"
  out="$work_dir/statuses-$agent"
  [ -e "$out" ] || { echo UNKNOWN; return; }
  status="$(awk -v t="$task" '$1 == t { print $2; exit }' "$out")"
  echo "${status:-UNKNOWN}"
}

is_terminal_status() {
  case "$1" in
    completed|failed|cancelled) return 0 ;;
    *) return 1 ;;
  esac
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
unproven_list=()
kept=0

printf '%-46s %6s  %-22s %-9s %s\n' BRANCH UNIQUE TASK_STATUS CLEANUP_STATE DETAIL
while IFS= read -r branch; do
  [ -n "$branch" ] || continue
  unique="$(git -C "$repo" rev-list --count "$base..$branch")"
  task="$(record_trailer "$branch" Multica-Task)"
  agent="$(record_trailer "$branch" Multica-Agent)"
  if [ -n "$task" ] && [ -n "$agent" ]; then
    task_status="$(task_status_of "$agent" "$task")"
    detail="task=$task"
  else
    task_status="NONE"
    detail=""
  fi

  if is_checked_out "$branch"; then
    state="BUSY"
    detail="checked out by a worktree${detail:+; $detail}"
    kept=$((kept + 1))
  elif [ "$unique" -gt 0 ]; then
    # A branch's own commits outrank every other signal, including a task the
    # platform reports as finished: the commit may be the snapshot of the
    # user's uncommitted directory and exist nowhere else.
    state="HAS-WORK"
    detail="inspect: git -C $repo log $base..$branch${detail:+ | $detail}"
    kept=$((kept + 1))
  elif ! has_record "$branch"; then
    state="UNPROVEN"
    detail="no ownership record; needs an explicit confirmation"
    unproven_list+=("$branch")
  elif [ "$task_status" = "UNKNOWN" ] || [ "$task_status" = "NONE" ]; then
    # NONE is a record written before the task/agent trailers existed: it names
    # no task, so no status can be looked up. That is the pre-upgrade leftover
    # this script exists to clear, and it belongs with the other unprovable
    # branches — never deleted unattended, still reachable one confirmation at
    # a time. Treating it as ACTIVE would make it undeletable forever.
    state="UNPROVEN"
    if [ "$task_status" = "NONE" ]; then
      detail="ownership record names no task; needs an explicit confirmation"
    else
      detail="$detail; task status could not be established; needs an explicit confirmation"
    fi
    unproven_list+=("$branch")
  elif ! is_terminal_status "$task_status"; then
    state="ACTIVE"
    detail="$detail; task has not finished"
    kept=$((kept + 1))
  else
    state="SAFE"
    safe_list+=("$branch")
  fi
  printf '%-46s %6s  %-22s %-9s %s\n' "$branch" "$unique" "$task_status" "$state" "$detail"
done <<< "$branches"

echo
echo "SAFE: ${#safe_list[@]}   UNPROVEN: ${#unproven_list[@]}   kept: $kept   base: $base"

delete_list=("${safe_list[@]}")
if [ "$include_unrecorded" = "1" ]; then
  delete_list+=("${unproven_list[@]}")
fi

if [ "$do_delete" != "1" ]; then
  if [ "${#safe_list[@]}" -gt 0 ]; then
    echo "Re-run with --delete to remove the SAFE branches."
  fi
  if [ "${#unproven_list[@]}" -gt 0 ]; then
    echo "UNPROVEN branches are offered only with --delete --include-unrecorded, one confirmation each."
  fi
  exit 0
fi

if [ "${#delete_list[@]}" -eq 0 ]; then
  echo "Nothing to delete."
  exit 0
fi

# Whether this branch may go without asking. --yes speaks for the SAFE class
# only: an UNPROVEN branch has no proof that Multica is done with it, so the
# operator confirms it one at a time however the run was invoked.
auto_ok() {
  [ "$assume_yes" = "1" ] || return 1
  local candidate=$1 unproven
  for unproven in ${unproven_list[@]+"${unproven_list[@]}"}; do
    [ "$unproven" = "$candidate" ] && return 1
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
