#!/usr/bin/env bash
# Contract test for scripts/agent-branch-cleanup.sh.
#
# Two properties are under test, and both exist to stop the script destroying
# something irreplaceable:
#
#  1. A branch carrying commits nothing else has is NEVER deleted, however it
#     got there. The baseline commit a prepare writes is a snapshot of the
#     user's own uncommitted directory, so a wrong classification here destroys
#     work that exists in no other place.
#  2. A branch is deleted unattended only when the task that owns it is proven
#     finished. A task still queued or running can still deliver into its
#     branch, and a status lookup that fails proves nothing at all — both must
#     fail closed rather than read as "finished".
set -euo pipefail

export LC_ALL=C
export LANGUAGE=

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$ROOT_DIR/scripts/agent-branch-cleanup.sh"

TMP_ROOT="$(mktemp -d)"
trap 'rm -rf "$TMP_ROOT"' EXIT

failures=0

fail() {
  echo "FAIL: $1"
  failures=$((failures + 1))
}

git_q() {
  git -C "$1" "${@:2}" >/dev/null 2>&1
}

AGENT_ID=11112222-3333-4444-5555-000000000002

# Writes the hidden ref Multica uses to prove a branch is its own, naming the
# task it belongs to. Only the ref's existence and its trailers matter here.
record() {
  local dir=$1 branch=$2 task=$3 tree state commit
  tree="$(git -C "$dir" rev-parse "$branch^{tree}")"
  # Same shape as writeBranchRecord: first parent is the user snapshot, second
  # is the branch tip the record was written against.
  state="$(git -C "$dir" commit-tree "$tree" -m "multica: user state")"
  commit="$(git -C "$dir" commit-tree "$tree" -p "$state" -p "$branch" \
    -m "multica: task branch record

Multica-Workspace: 11112222-3333-4444-5555-000000000001
Multica-Agent: $AGENT_ID
Multica-Task: $task")"
  git -C "$dir" update-ref "refs/multica/local-state/$branch" "$commit"
}

# The record shape written before the task/agent trailers existed: the ref is
# there, the ids are not. Every branch left behind by a version older than this
# change looks like this, and those are exactly the branches the script exists
# to clean up.
record_without_ids() {
  local dir=$1 branch=$2 tree state commit
  tree="$(git -C "$dir" rev-parse "$branch^{tree}")"
  state="$(git -C "$dir" commit-tree "$tree" -m "multica: user state")"
  commit="$(git -C "$dir" commit-tree "$tree" -p "$state" -p "$branch" \
    -m "multica: task branch record

Multica-Workspace: 11112222-3333-4444-5555-000000000001")"
  git -C "$dir" update-ref "refs/multica/local-state/$branch" "$commit"
}

# Builds a repo with one branch of each classification the script reports.
make_repo() {
  local dir
  dir="$(mktemp -d -p "$TMP_ROOT")"
  git_q "$dir" init -b main
  git_q "$dir" config user.name "Test User"
  git_q "$dir" config user.email "test@test.com"
  echo original > "$dir/tracked.txt"
  git_q "$dir" add -A
  git_q "$dir" commit -m initial

  # SAFE: created by Multica, sits exactly on main, task reached a terminal
  # status.
  git_q "$dir" branch agent/j/safebranch01
  record "$dir" agent/j/safebranch01 task-completed

  # HAS-WORK: carries a commit main does not — the baseline snapshot case. Its
  # task is finished, which must not be allowed to outrank the commits.
  git_q "$dir" branch agent/j/hasworkbranch
  git_q "$dir" worktree add --quiet "$dir/wt-haswork" agent/j/hasworkbranch
  echo "the user's uncommitted work" > "$dir/wt-haswork/wip.txt"
  git_q "$dir/wt-haswork" add -A
  git_q "$dir/wt-haswork" commit -m "chore(agent): baseline — uncommitted work from the local directory"
  git_q "$dir" worktree remove --force "$dir/wt-haswork"
  record "$dir" agent/j/hasworkbranch task-completed

  # UNPROVEN: same naming, empty, but no record proving whose it is.
  git_q "$dir" branch agent/j/usersbranch01

  # BUSY: recorded, empty, and a worktree still holds it.
  git_q "$dir" branch agent/j/busybranch012
  record "$dir" agent/j/busybranch012 task-completed
  git_q "$dir" worktree add --quiet "$dir/wt-busy" agent/j/busybranch012

  # ACTIVE: recorded and empty, but its task is still running.
  git_q "$dir" branch agent/j/runningbranch
  record "$dir" agent/j/runningbranch task-running

  # ACTIVE: a queued task has not started, and its branch is just as much off
  # limits as a running one's.
  git_q "$dir" branch agent/j/queuedbranch0
  record "$dir" agent/j/queuedbranch0 task-queued

  # UNPROVEN: recorded and empty, but the platform does not list its task, so
  # nothing establishes that it finished.
  git_q "$dir" branch agent/j/goneetaskbr01
  record "$dir" agent/j/goneetaskbr01 task-not-listed

  # UNPROVEN: an old record with no task id at all. Nothing establishes that
  # its task finished, so it may not be deleted unattended — but it must stay
  # reachable through a per-branch confirmation, or the pre-upgrade leftovers
  # this script was written for could never be removed with it.
  git_q "$dir" branch agent/j/oldrecordbr01
  record_without_ids "$dir" agent/j/oldrecordbr01

  # HAS-WORK still outranks a missing task id: an old record on a branch that
  # carries the user's baseline snapshot must never become deletable.
  git_q "$dir" branch agent/j/oldrecordwork
  git_q "$dir" worktree add --quiet "$dir/wt-oldwork" agent/j/oldrecordwork
  echo "older uncommitted work" > "$dir/wt-oldwork/wip-old.txt"
  git_q "$dir/wt-oldwork" add -A
  git_q "$dir/wt-oldwork" commit -m "chore(agent): baseline — uncommitted work from the local directory"
  git_q "$dir" worktree remove --force "$dir/wt-oldwork"
  record_without_ids "$dir" agent/j/oldrecordwork

  echo "$dir"
}

# Stands in for `multica agent tasks <agent> --output json`, reduced to the two
# fields the script reads. Called as `<cmd> <agent-id>`.
statuses_ok="$TMP_ROOT/statuses-ok.sh"
cat > "$statuses_ok" <<EOF
#!/usr/bin/env bash
[ "\$1" = "$AGENT_ID" ] || exit 1
cat <<'ROWS'
task-completed completed
task-running running
task-queued queued
task-failed failed
task-cancelled cancelled
ROWS
EOF
chmod +x "$statuses_ok"

# A status source that is simply broken — the CLI missing, unauthenticated, or
# the server unreachable.
statuses_broken="$TMP_ROOT/statuses-broken.sh"
printf '#!/usr/bin/env bash\nexit 1\n' > "$statuses_broken"
chmod +x "$statuses_broken"

# Column readers over the listing's fixed layout: BRANCH UNIQUE TASK_STATUS
# CLEANUP_STATE DETAIL.
column_of() {
  awk -v b="$2" -v c="$3" '$1 == b { print $c; exit }' "$1"
}
state_of() { column_of "$1" "$2" 4; }
task_status_of() { column_of "$1" "$2" 3; }

cleanup_repo() {
  git -C "$1" worktree remove --force "$1/wt-busy" >/dev/null 2>&1 || true
  rm -rf "$1"
}

listing="$TMP_ROOT/listing"

# --- classification ---------------------------------------------------------
repo="$(make_repo)"
bash "$SCRIPT" --repo "$repo" --status-command "$statuses_ok" > "$listing"

for expected in "agent/j/safebranch01 completed SAFE" \
                "agent/j/hasworkbranch completed HAS-WORK" \
                "agent/j/usersbranch01 NONE UNPROVEN" \
                "agent/j/busybranch012 completed BUSY" \
                "agent/j/runningbranch running ACTIVE" \
                "agent/j/queuedbranch0 queued ACTIVE" \
                "agent/j/goneetaskbr01 UNKNOWN UNPROVEN" \
                "agent/j/oldrecordbr01 NONE UNPROVEN" \
                "agent/j/oldrecordwork NONE HAS-WORK"; do
  set -- $expected
  got_status="$(task_status_of "$listing" "$1")"
  got_state="$(state_of "$listing" "$1")"
  if [ "$got_status" != "$2" ]; then
    fail "$1 reported task status $got_status, want $2"
  fi
  if [ "$got_state" != "$3" ]; then
    fail "$1 classified $got_state, want $3"
  fi
done

if [ "$(git -C "$repo" for-each-ref --format='%(refname:short)' 'refs/heads/agent/' | wc -l)" != "9" ]; then
  fail "the read-only listing changed the branch list"
fi

# --- deletion ---------------------------------------------------------------
bash "$SCRIPT" --repo "$repo" --status-command "$statuses_ok" --delete --yes > "$listing"

if git -C "$repo" rev-parse --verify --quiet agent/j/safebranch01 >/dev/null; then
  fail "--delete kept the SAFE branch"
fi
if git -C "$repo" rev-parse --verify --quiet refs/multica/local-state/agent/j/safebranch01 >/dev/null; then
  fail "--delete left the record ref of a deleted branch behind"
fi
# Every other class survives an unattended run: work, a live worktree, a task
# that has not finished, and a task whose status is unknown.
for kept in agent/j/hasworkbranch agent/j/busybranch012 agent/j/runningbranch \
            agent/j/queuedbranch0 agent/j/goneetaskbr01 agent/j/usersbranch01 \
            agent/j/oldrecordbr01 agent/j/oldrecordwork; do
  if ! git -C "$repo" rev-parse --verify --quiet "$kept" >/dev/null; then
    fail "--delete --yes removed $kept, which it must never take unattended"
  fi
done

# Even with --include-unrecorded it asks: answering "n" leaves them in place.
if printf 'n\nn\nn\n' | bash "$SCRIPT" --repo "$repo" --status-command "$statuses_ok" \
     --delete --yes --include-unrecorded > "$listing" 2>&1; then
  for kept in agent/j/usersbranch01 agent/j/goneetaskbr01 agent/j/oldrecordbr01; do
    if ! git -C "$repo" rev-parse --verify --quiet "$kept" >/dev/null; then
      fail "a declined confirmation still deleted $kept"
    fi
  done
else
  fail "--include-unrecorded run exited non-zero"
fi
# ...and answering "y" is what deletes them. The old record with no task id is
# among them: it is the pre-upgrade leftover the script has to be able to clear.
printf 'y\ny\ny\n' | bash "$SCRIPT" --repo "$repo" --status-command "$statuses_ok" \
  --delete --yes --include-unrecorded > "$listing" 2>&1 || true
for gone in agent/j/usersbranch01 agent/j/goneetaskbr01 agent/j/oldrecordbr01; do
  if git -C "$repo" rev-parse --verify --quiet "$gone" >/dev/null; then
    fail "a confirmed UNPROVEN branch was not deleted: $gone"
  fi
done
# A confirmation covers one branch, not the classes that are never offered.
for kept in agent/j/runningbranch agent/j/queuedbranch0 agent/j/hasworkbranch \
            agent/j/busybranch012 agent/j/oldrecordwork; do
  if ! git -C "$repo" rev-parse --verify --quiet "$kept" >/dev/null; then
    fail "--include-unrecorded removed $kept, which it must never offer"
  fi
done
cleanup_repo "$repo"

# --- a status lookup that fails must fail closed ----------------------------
repo="$(make_repo)"
bash "$SCRIPT" --repo "$repo" --status-command "$statuses_broken" > "$listing"
if [ "$(task_status_of "$listing" agent/j/safebranch01)" != "UNKNOWN" ]; then
  fail "a failed status lookup did not report UNKNOWN"
fi
if [ "$(state_of "$listing" agent/j/safebranch01)" != "UNPROVEN" ]; then
  fail "a failed status lookup left a branch deletable"
fi
bash "$SCRIPT" --repo "$repo" --status-command "$statuses_broken" --delete --yes > "$listing"
if ! git -C "$repo" rev-parse --verify --quiet agent/j/safebranch01 >/dev/null; then
  fail "an unattended run deleted a branch while the task status was unknown"
fi
cleanup_repo "$repo"

# `none` is the documented way to run without the platform at all; it must be
# just as closed as a broken lookup, not an escape hatch.
repo="$(make_repo)"
bash "$SCRIPT" --repo "$repo" --status-command none --delete --yes > "$listing"
if ! git -C "$repo" rev-parse --verify --quiet agent/j/safebranch01 >/dev/null; then
  fail "--status-command none deleted a branch with no status evidence"
fi
cleanup_repo "$repo"

if [ "$failures" -eq 0 ]; then
  echo "PASS: agent-branch-cleanup.sh"
else
  echo "$failures check(s) failed"
  exit 1
fi
