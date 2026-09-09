#!/usr/bin/env bash
# Contract test for scripts/agent-branch-cleanup.sh.
#
# The property under test is the one that makes the script safe to run at all:
# a branch carrying commits nothing else has is NEVER deleted, however it got
# there. The baseline commit a prepare writes is a snapshot of the user's own
# uncommitted directory, so a wrong classification here destroys work that
# exists in no other place.
set -euo pipefail

export LC_ALL=C
export LANGUAGE=

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$ROOT_DIR/scripts/agent-branch-cleanup.sh"

failures=0

fail() {
  echo "FAIL: $1"
  failures=$((failures + 1))
}

git_q() {
  git -C "$1" "${@:2}" >/dev/null 2>&1
}

# Builds a repo with one branch of each classification the script reports.
make_repo() {
  local dir
  dir="$(mktemp -d)"
  git_q "$dir" init -b main
  git_q "$dir" config user.name "Test User"
  git_q "$dir" config user.email "test@test.com"
  echo original > "$dir/tracked.txt"
  git_q "$dir" add -A
  git_q "$dir" commit -m initial

  # SAFE: created by Multica, sits exactly on main.
  git_q "$dir" branch agent/j/safebranch01
  record "$dir" agent/j/safebranch01

  # HAS-WORK: carries a commit main does not — the baseline snapshot case.
  git_q "$dir" branch agent/j/hasworkbranch
  git_q "$dir" worktree add --quiet "$dir/../wt-haswork" agent/j/hasworkbranch
  echo "the user's uncommitted work" > "$dir/../wt-haswork/wip.txt"
  git_q "$dir/../wt-haswork" add -A
  git_q "$dir/../wt-haswork" commit -m "chore(agent): baseline — uncommitted work from the local directory"
  git_q "$dir" worktree remove --force "$dir/../wt-haswork"
  record "$dir" agent/j/hasworkbranch

  # ORPHAN: same naming, empty, but no record proving whose it is.
  git_q "$dir" branch agent/j/usersbranch01

  # BUSY: recorded, empty, and a worktree still holds it.
  git_q "$dir" branch agent/j/busybranch012
  record "$dir" agent/j/busybranch012
  git_q "$dir" worktree add --quiet "$dir/../wt-busy" agent/j/busybranch012

  echo "$dir"
}

# Writes the hidden ref Multica uses to prove a branch is its own. Only the
# ref's existence and its trailers matter to the script.
record() {
  local dir=$1 branch=$2 tree state commit
  tree="$(git -C "$dir" rev-parse "$branch^{tree}")"
  # Same shape as writeBranchRecord: first parent is the user snapshot, second
  # is the branch tip the record was written against.
  state="$(git -C "$dir" commit-tree "$tree" -m "multica: user state")"
  commit="$(git -C "$dir" commit-tree "$tree" -p "$state" -p "$branch" \
    -m "multica: task branch record

Multica-Workspace: 11112222-3333-4444-5555-000000000001
Multica-Agent: 11112222-3333-4444-5555-000000000002
Multica-Task: 11112222-3333-4444-5555-aaaaaaaaaaaa")"
  git -C "$dir" update-ref "refs/multica/local-state/$branch" "$commit"
}

state_of() {
  sed -n "s|^$2 *[0-9]* *\([A-Z-]*\).*|\1|p" "$1"
}

repo="$(make_repo)"
listing="$(mktemp)"
bash "$SCRIPT" --repo "$repo" > "$listing"

for expected in "agent/j/safebranch01 SAFE" "agent/j/hasworkbranch HAS-WORK" \
                "agent/j/usersbranch01 ORPHAN" "agent/j/busybranch012 BUSY"; do
  branch="${expected% *}"
  want="${expected#* }"
  got="$(state_of "$listing" "$branch")"
  if [ "$got" != "$want" ]; then
    fail "$branch classified $got, want $want"
  fi
done

if [ "$(git -C "$repo" for-each-ref --format='%(refname:short)' 'refs/heads/agent/' | wc -l)" != "4" ]; then
  fail "the read-only listing changed the branch list"
fi

bash "$SCRIPT" --repo "$repo" --delete --yes > "$listing"

if git -C "$repo" rev-parse --verify --quiet agent/j/safebranch01 >/dev/null; then
  fail "--delete kept the SAFE branch"
fi
# --yes speaks for the SAFE class only. An ORPHAN carries no proof of whose it
# is, so an unattended run must never take one — that is the whole reason the
# class exists rather than being folded into SAFE.
if ! git -C "$repo" rev-parse --verify --quiet agent/j/usersbranch01 >/dev/null; then
  fail "--delete --yes removed an ORPHAN branch without a confirmation"
fi
# Even with --include-unrecorded it asks: answering "n" leaves it in place.
if printf 'n\n' | bash "$SCRIPT" --repo "$repo" --delete --yes --include-unrecorded > "$listing" 2>&1; then
  if ! git -C "$repo" rev-parse --verify --quiet agent/j/usersbranch01 >/dev/null; then
    fail "a declined ORPHAN confirmation still deleted the branch"
  fi
else
  fail "--include-unrecorded run exited non-zero"
fi
# ...and answering "y" is what deletes it.
printf 'y\n' | bash "$SCRIPT" --repo "$repo" --delete --yes --include-unrecorded > "$listing" 2>&1 || true
if git -C "$repo" rev-parse --verify --quiet agent/j/usersbranch01 >/dev/null; then
  fail "a confirmed ORPHAN branch was not deleted"
fi
if git -C "$repo" rev-parse --verify --quiet refs/multica/local-state/agent/j/safebranch01 >/dev/null; then
  fail "--delete left the record ref of a deleted branch behind"
fi
for kept in agent/j/hasworkbranch agent/j/busybranch012; do
  if ! git -C "$repo" rev-parse --verify --quiet "$kept" >/dev/null; then
    fail "--delete removed $kept, which it must never touch"
  fi
done

git -C "$repo" worktree remove --force "$repo/../wt-busy" >/dev/null 2>&1 || true
rm -rf "$repo" "$listing"

if [ "$failures" -eq 0 ]; then
  echo "PASS: agent-branch-cleanup.sh"
else
  echo "$failures check(s) failed"
  exit 1
fi
