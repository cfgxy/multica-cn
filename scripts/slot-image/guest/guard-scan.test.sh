#!/usr/bin/env bash
# guard-scan.test.sh — guest-side recycle guard (image-baked port of
# scripts/lib-recycle-guard.sh, RUYI-594 semantics).
#
# Runs against throwaway git repos in a sandboxed HOME. Every verdict the
# local library produces must match here verbatim in spirit: pass / evidence
# / blocked, topology-aware, fail-closed on unreadable carriers, one-shot
# MULTICA_RECYCLE_OVERRIDE, evidence file with the recycle-evidence v1 fields.
#
# Negative assertions pin verdicts: removing a predicate from guard-scan.sh
# must flip the matching assertion red, not silently pass.
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
guest_dir="$root_dir/scripts/slot-image/guest"
guard="$guest_dir/guard-scan.sh"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT
export HOME="$tmp_dir/home"
mkdir -p "$HOME"
export MULTICA_RECYCLE_EVIDENCE_DIR="$tmp_dir/evidence"

PASS=0; FAIL=0
fail() { printf 'FAIL: %s\n' "$1" >&2; FAIL=$((FAIL + 1)); }
ok()   { PASS=$((PASS + 1)); }
# assert_verdict <expected> <label> <dir> [extra env via prefix command]
assert_verdict() {
  local expected=$1 label=$2 dir=$3 out rc=0 verdict
  out="$(bash "$guard" "$dir" test-kind 0 2>&1)" || rc=$?
  verdict="$(printf '%s\n' "$out" | sed -n 's/^verdict: //p' | head -1)"
  if [ "$verdict" = "$expected" ]; then ok; else
    fail "$label: expected verdict $expected, got '${verdict:-<none>}' (rc=$rc)
$out"
  fi
}
assert_rc() {
  local expected=$1 label=$2 dir=$3 rc=0
  bash "$guard" "$dir" test-kind 0 >/dev/null 2>&1 || rc=$?
  if [ "$rc" = "$expected" ]; then ok; else fail "$label: expected rc=$expected, got $rc"; fi
}

git_init() {
  local dir=$1
  git -C "$dir" init -q -b main
  git -C "$dir" config user.email t@t; git -C "$dir" config user.name t
}

# commit_plus_remote <dir> — a "clean" repo here means everything reachable is
# on a (fake) remote; without a remote every commit counts as unpushed, which
# is the semantics under test, not a fixture artifact.
commit_plus_remote() {
  local dir=$1 msg=$2
  git -C "$dir" add -A; git -C "$dir" commit -qm "$msg"
  git -C "$dir" update-ref refs/remotes/origin/main HEAD
}

# --- 1. clean standalone repo: pass -----------------------------------------
d="$tmp_dir/clean"; mkdir -p "$d"; git_init "$d"
echo a >"$d/a"
commit_plus_remote "$d" init
assert_verdict pass "clean standalone" "$d"
assert_rc 0 "clean standalone rc" "$d"

# --- 2. standalone with sole-ref commit: blocked ----------------------------
d="$tmp_dir/sole"; mkdir -p "$d"; git_init "$d"
echo a >"$d/a"; git -C "$d" add a; git -C "$d" commit -qm init
git -C "$d" commit -q --allow-empty -m orphan
assert_verdict blocked "sole-ref standalone" "$d"
assert_rc 1 "sole-ref rc" "$d"

# --- 3. standalone with stash: blocked --------------------------------------
d="$tmp_dir/stash"; mkdir -p "$d"; git_init "$d"
echo a >"$d/a"; git -C "$d" add a; git -C "$d" commit -qm init
echo b >"$d/a"; git -C "$d" stash -q
assert_verdict blocked "stash standalone" "$d"

# --- 4. standalone dirty tracked: blocked -----------------------------------
d="$tmp_dir/dirty"; mkdir -p "$d"; git_init "$d"
echo a >"$d/a"; git -C "$d" add a; git -C "$d" commit -qm init
echo b >"$d/a"
assert_verdict blocked "dirty standalone" "$d"

# --- 5. standalone unpushed-only (branch has sole upstream ref): blocked ----
d="$tmp_dir/unpushed"; mkdir -p "$d"; git_init "$d"
echo a >"$d/a"; git -C "$d" add a; git -C "$d" commit -qm init
git -C "$d" update-ref refs/heads/shared HEAD   # branch holds the commit…
assert_verdict blocked "unpushed standalone" "$d"

# --- 6. linked worktree: stash+unpushed survive in shared .git → evidence ---
d="$tmp_dir/linked"; mkdir -p "$d"; git_init "$d"
echo a >"$d/a"; git -C "$d" add a; git -C "$d" commit -qm init
git -C "$d" worktree add -q "$tmp_dir/linked-wt" -b side 2>/dev/null
(cd "$tmp_dir/linked-wt" && echo s >s && git add s && git stash -q)
assert_verdict evidence "linked stash note" "$tmp_dir/linked-wt"
assert_rc 0 "linked stash rc" "$tmp_dir/linked-wt"

# --- 7. linked worktree with detached sole-ref commits: blocked -------------
d="$tmp_dir/detached"; mkdir -p "$d"; git_init "$d"
echo a >"$d/a"; git -C "$d" add a; git -C "$d" commit -qm init
git -C "$d" worktree add -q --detach "$tmp_dir/detached-wt" 2>/dev/null
git -C "$tmp_dir/detached-wt" commit -q --allow-empty -m orphan
assert_verdict blocked "linked sole-ref" "$tmp_dir/detached-wt"

# --- 8. unborn HEAD (no commits): pass --------------------------------------
d="$tmp_dir/unborn"; mkdir -p "$d"; git_init "$d"
echo a >"$d/a"
assert_verdict pass "unborn HEAD" "$d"

# --- 9. unreadable carrier: fail-closed blocked -----------------------------
d="$tmp_dir/corrupt"; mkdir -p "$d"; git_init "$d"
echo a >"$d/a"; git -C "$d" add a; git -C "$d" commit -qm init
rm -f "$d/.git/refs/heads/main" "$d/.git/packed-refs" 2>/dev/null
mkdir -p "$d/.git/refs/heads"; printf 'garbage' >"$d/.git/HEAD"
assert_verdict blocked "corrupt repo fail-closed" "$d"

# --- 10. override releases a blocked verdict, evidence records it -----------
d="$tmp_dir/sole2"; mkdir -p "$d"; git_init "$d"
echo a >"$d/a"; git -C "$d" add a; git -C "$d" commit -qm init
git -C "$d" commit -q --allow-empty -m orphan
rc=0
out="$(MULTICA_RECYCLE_OVERRIDE=1 bash "$guard" "$d" test-kind 0 2>&1)" || rc=$?
verdict="$(printf '%s\n' "$out" | sed -n 's/^verdict: //p' | head -1)"
if [ "$verdict" = "blocked" ] && [ "$rc" = "0" ]; then ok; else
  fail "override: verdict=$verdict rc=$rc (expect verdict blocked + rc 0)"
fi
ev_file="$(printf '%s\n' "$out" | sed -n 's/^evidence: //p' | head -1)"
if [ -n "$ev_file" ] && [ -f "$ev_file" ] && grep -q '^override: true$' "$ev_file" \
   && grep -q '^verdict: blocked$' "$ev_file"; then ok; else
  fail "override evidence missing override:true or original verdict ($ev_file)"
fi

# --- 11. evidence file carries the v1 fields --------------------------------
d="$tmp_dir/clean2"; mkdir -p "$d"; git_init "$d"
echo a >"$d/a"
commit_plus_remote "$d" init
out="$(bash "$guard" "$d" slot-destroy 0 2>&1)" || true
ev_file="$(printf '%s\n' "$out" | sed -n 's/^evidence: //p' | head -1)"
for field in 'recycle-evidence v1' '^timestamp: ' '^tool: ' '^target: ' '^kind: slot-destroy' \
             '^topology: standalone' '^dry_run: false' '^override: false' '^verdict: pass' \
             '^predicates:' 'predicate: sole_ref ' 'predicate: unpushed ' 'predicate: stash ' \
             'predicate: dirty_tracked ' 'predicate: untracked '; do
  if grep -qE "$field" "$ev_file"; then ok; else fail "evidence missing $field"; fi
done

# --- 12. dry-run lands dry_run: true ----------------------------------------
out="$(bash "$guard" "$d" slot-destroy 1 2>&1)" || true
ev_file="$(printf '%s\n' "$out" | sed -n 's/^evidence: //p' | head -1)"
grep -q '^dry_run: true$' "$ev_file" && ok || fail "dry-run evidence missing dry_run: true"

# --- 13. counts line matches the local library format -----------------------
d="$tmp_dir/counts"; mkdir -p "$d"; git_init "$d"
echo a >"$d/a"
commit_plus_remote "$d" init
echo x >"$d/untracked.txt"
out="$(bash "$guard" "$d" test-kind 0 2>&1)" || true
counts="$(printf '%s\n' "$out" | sed -n 's/^counts: //p' | head -1)"
if [ "$counts" = "sole_ref=0 unpushed=0 stash=0 dirty_tracked=0 untracked=1" ]; then ok; else
  fail "counts line mismatch: '$counts'"
fi

# --- 14. guard disabled by kill switch: verdict disabled, rc 0 --------------
out="$(MULTICA_GC_GUARD_ENABLED=false bash "$guard" "$d" test-kind 0 2>&1)" || rc=$?
verdict="$(printf '%s\n' "$out" | sed -n 's/^verdict: //p' | head -1)"
if [ "$verdict" = "disabled" ] && [ "$rc" = "0" ]; then ok; else
  fail "kill switch: verdict=$verdict rc=$rc"
fi

# --- 15. multiple carriers: any blocked → blocked; summary lists each -------
root="$tmp_dir/multi"; mkdir -p "$root/a" "$root/b"; git_init "$root/a"; git_init "$root/b"
echo a >"$root/a/a"; git -C "$root/a" add a; git -C "$root/a" commit -qm init
echo b >"$root/b/b"; git -C "$root/b" add b; git -C "$root/b" commit -qm init
git -C "$root/b" commit -q --allow-empty -m orphan
out="$(bash "$guard" "$root" test-kind 0 2>&1)" || rc=$?
verdict="$(printf '%s\n' "$out" | sed -n 's/^verdict: //p' | head -1)"
if [ "$verdict" = "blocked" ] && [ "$rc" = "1" ] && printf '%s\n' "$out" | grep -q "$root/b"; then ok; else
  fail "multi-carrier: verdict=$verdict rc=$rc, offending path not named"
fi

printf '\nguard-scan.test.sh: %d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" = 0 ]
