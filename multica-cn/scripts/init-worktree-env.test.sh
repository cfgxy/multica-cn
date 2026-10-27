#!/usr/bin/env bash
# Regression tests for scripts/init-worktree-env.sh database-password
# derivation (RUYI-75). The two regimes are both intentional:
#
#   - linked worktree: the instance shares the platform's postgres container
#     (compose project "multica"), so the password MUST be inherited from the
#     main checkout's .env; missing there fails closed — a generated password
#     would desync from, or reset, the shared role and take the platform down.
#
#   - standalone checkout (CI, plain clone): there is no shared stack and no
#     main checkout to inherit from, so the script must stay self-sufficient
#     and fall back to the upstream-generated weak default.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
UNDER_TEST="$SCRIPT_DIR/init-worktree-env.sh"

tmp_root="$(mktemp -d)"
trap 'rm -rf "$tmp_root"' EXIT

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

git_commit_root_tree() {
  local repo=$1
  mkdir -p "$repo"
  git -C "$repo" init -q
  git -C "$repo" -c user.name=test -c user.email=test@example.com commit -q --allow-empty -m init
}

read_generated_password() { # $1 = generated env file
  sed -n 's/^POSTGRES_PASSWORD=//p' "$1"
}

# --- linked worktree inherits the main checkout's password ------------------
main_repo="$tmp_root/main"
git_commit_root_tree "$main_repo"
printf 'POSTGRES_PASSWORD=fixture-main-password\nPOSTGRES_DB=fixture_db\n' >"$main_repo/.env"
git -C "$main_repo" worktree add -q "$tmp_root/wt" -b wt

(
  cd "$tmp_root/wt" &&
    WORKTREE_NAME=inherit-case bash "$UNDER_TEST" "$tmp_root/inherit.env" >/dev/null
)
generated="$(read_generated_password "$tmp_root/inherit.env")"
[ "$generated" = "fixture-main-password" ] ||
  fail "linked worktree must inherit the main checkout password, got: $generated"

# The worktree shares the platform's main database (RUYI-66): the generated
# file must name `multica` even when the main checkout's .env names a
# different database (the fixture uses POSTGRES_DB=fixture_db). The password
# is inherited; the database name is not.
grep -q '^POSTGRES_DB=multica$' "$tmp_root/inherit.env" ||
  fail "generated env must record the shared main database 'multica', got: $(grep '^POSTGRES_DB=' "$tmp_root/inherit.env" || echo '<missing>')"
grep -q '^DATABASE_URL=postgres://multica:fixture-main-password@localhost:5432/multica?sslmode=disable$' "$tmp_root/inherit.env" ||
  fail "generated DATABASE_URL must point at the shared main database, got: $(grep '^DATABASE_URL=' "$tmp_root/inherit.env" || echo '<missing>')"

# --- linked worktree without a main .env fails closed -----------------------
mv "$main_repo/.env" "$main_repo/.env.bak"
if (
  cd "$tmp_root/wt" &&
    WORKTREE_NAME=failclosed-case bash "$UNDER_TEST" "$tmp_root/failclosed.env"
) >/dev/null 2>"$tmp_root/failclosed.err"; then
  fail "linked worktree without a readable main .env must refuse to generate a password"
fi
grep -q '拒绝生成弱口令环境文件' "$tmp_root/failclosed.err" ||
  fail "refusal message missing, got: $(cat "$tmp_root/failclosed.err")"
mv "$main_repo/.env.bak" "$main_repo/.env"

# --- standalone checkout stays self-sufficient (CI-equivalent) --------------
solo_repo="$tmp_root/solo"
git_commit_root_tree "$solo_repo"
(
  cd "$solo_repo" &&
    WORKTREE_NAME=standalone-case bash "$UNDER_TEST" "$tmp_root/standalone.env" >/dev/null
)
generated="$(read_generated_password "$tmp_root/standalone.env")"
[ "$generated" = "multica" ] ||
  fail "standalone checkout without .env must fall back to the upstream default, got: $generated"

# --- standalone checkout still prefers its own .env -------------------------
printf 'POSTGRES_PASSWORD=fixture-solo-password\n' >"$solo_repo/.env"
(
  cd "$solo_repo" &&
    WORKTREE_NAME=standalone-env-case bash "$UNDER_TEST" "$tmp_root/standalone-env.env" >/dev/null
)
generated="$(read_generated_password "$tmp_root/standalone-env.env")"
[ "$generated" = "fixture-solo-password" ] ||
  fail "standalone checkout with .env must inherit its own password, got: $generated"

echo "init-worktree-env derivation ok"
