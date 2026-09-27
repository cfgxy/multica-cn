#!/usr/bin/env bash
set -euo pipefail

# The Makefile `include`s the env file, and makefile syntax has no continuation
# for values: one multi-line value there used to abort every target — `up`,
# `list`, `down`, `test` — with "missing separator" before any recipe ran
# (RUYI-218). Since the project tells every new worktree and QA environment to
# copy the main checkout's .env, a PEM key landing in that file took out the
# first command anyone runs.
#
# Two properties have to hold together, and neither alone is the fix:
#   * make parses the file, and
#   * the multi-line value still reaches subprocesses whole — `make server` /
#     `make start` / `make setup` give `go run` nothing but the environment the
#     Makefile's top-level `export` builds.
#
# Nothing else covers this: no Go or Vitest suite runs the Makefile.

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT

fail() {
  echo "FAIL: $1" >&2
  exit 1
}

# Printing through a recipe is what proves the value reached a subprocess's
# environment; reading the make variable would only prove it parsed. Kept in a
# second -f file so the assertion needs no probe target in the real Makefile.
cat >"$work_dir/probe.mk" <<'MK'
probe-env:
	@printenv PROBE_KEY

probe-port:
	@printenv POSTGRES_PORT
MK

# A self-invented placeholder in PEM shape. Never copy a real key into a test.
cat >"$work_dir/multiline.env" <<'ENV'
POSTGRES_PORT=55432
FRONTEND_PORT=13000
PROBE_KEY="-----BEGIN TESTING PLACEHOLDER-----
QUJDREVGRwo=
SElKS0xNTk9QCg==
-----END TESTING PLACEHOLDER-----"
MULTICA_PUBLIC_URL=http://localhost:18080
ENV

expected_probe="-----BEGIN TESTING PLACEHOLDER-----
QUJDREVGRwo=
SElKS0xNTk9QCg==
-----END TESTING PLACEHOLDER-----"

# ---- 1. Targets run at all when the env file carries a multi-line value ----

# `help` and `-n` touch no environment registry, database or port, so the
# assertion is about make parsing the file rather than about what a target does.
for target in help "-n down" "-n list" "-n up" "-n test"; do
  # shellcheck disable=SC2086
  if ! output="$(make $target ENV_FILE="$work_dir/multiline.env" 2>&1)"; then
    fail "make $target aborted on an env file with a multi-line value:
$output"
  fi
  case "$output" in
    *"missing separator"*|*"缺失分隔符"*)
      fail "make $target still reports a separator error:
$output"
      ;;
  esac
done

# ---- 2. The multi-line value reaches a subprocess with its newlines ----

probe="$(make -f Makefile -f "$work_dir/probe.mk" --no-print-directory \
  probe-env ENV_FILE="$work_dir/multiline.env")"
[ "$probe" = "$expected_probe" ] ||
  fail "the multi-line value did not reach the subprocess intact.
expected:
$expected_probe
got:
$probe"

# The quotes delimit the value, so they are not part of it — a PEM that keeps
# them parses nowhere.
case "$probe" in
  \"*|*\") fail "the delimiting quotes leaked into the value: $probe" ;;
esac

# ---- 3. Single-line keys still reach make and subprocesses ----

# A multi-line value must not swallow the keys around it: everything after it
# used to be unreachable because make never got past it.
port="$(make -f Makefile -f "$work_dir/probe.mk" --no-print-directory \
  probe-port ENV_FILE="$work_dir/multiline.env")"
[ "$port" = 55432 ] ||
  fail "POSTGRES_PORT from the env file did not reach the subprocess, got '$port'"

frag="$(bash scripts/env-make-include.sh "$work_dir/multiline.env")"
grep -Fqx 'POSTGRES_PORT=55432' "$frag" ||
  fail "a single-line key next to the multi-line value was lost from $frag"
grep -Fqx 'MULTICA_PUBLIC_URL=http://localhost:18080' "$frag" ||
  fail "a single-line key after the multi-line value was lost from $frag"

# ---- 4. Regression: a file without multi-line values is passed through ----

# Byte-for-byte identity below the checksum header is the strongest available
# statement that make's handling of comments, blank lines, empty values, `#`
# inside a value, `export` prefixes and `$` expansion is unchanged for the files
# everyone already has.
cat >"$work_dir/plain.env" <<'ENV'
# a comment

POSTGRES_PORT=5432
EMPTY_VALUE=
QUOTED_SINGLE_LINE="value with spaces"
HASH_IN_VALUE=http://localhost:8080/#anchor
export EXPORTED_KEY=exported
TRAILING_SPACES=value
ENV

plain_frag="$(bash scripts/env-make-include.sh "$work_dir/plain.env")"
head -n 1 "$plain_frag" | grep -q '^# env-make-include.sh source: ' ||
  fail "the fragment lost its checksum header, which is how reuse is decided:
$(head -n 1 "$plain_frag")"
tail -n +2 "$plain_frag" >"$work_dir/plain.body"
cmp -s "$work_dir/plain.env" "$work_dir/plain.body" ||
  fail "an env file without multi-line values was rewritten:
$(diff "$work_dir/plain.env" "$work_dir/plain.body" || true)"

# ---- 5. A value whose quote never closes keeps make's own diagnostics ----

# Guessing where such a value ends would invent content. Degrading to the
# original file means the operator sees make's parse error and fixes the file.
cat >"$work_dir/unterminated.env" <<'ENV'
POSTGRES_PORT=5432
BROKEN="never closed
ENV

unterminated_frag="$(bash scripts/env-make-include.sh "$work_dir/unterminated.env" 2>/dev/null)"
[ "$unterminated_frag" = "$work_dir/unterminated.env" ] ||
  fail "an unterminated value should fall back to the env file, got $unterminated_frag"

# ---- 6. A missing env file is not an error for the caller ----

missing_frag="$(bash scripts/env-make-include.sh "$work_dir/absent.env" 2>/dev/null)"
[ "$missing_frag" = "$work_dir/absent.env" ] ||
  fail "a missing env file should echo its own path, got $missing_frag"

# ---- 7. Same basename, different directories, different fragments ----

# A recursive `make ENV_FILE=…` can name a file whose basename matches another
# checkout's; one shared fragment would serve the wrong values.
mkdir -p "$work_dir/a" "$work_dir/b"
printf 'WHICH=a\n' >"$work_dir/a/.env"
printf 'WHICH=b\n' >"$work_dir/b/.env"
frag_a="$(bash scripts/env-make-include.sh "$work_dir/a/.env")"
frag_b="$(bash scripts/env-make-include.sh "$work_dir/b/.env")"
[ "$frag_a" != "$frag_b" ] ||
  fail "two env files with the same basename shared the fragment $frag_a"

# ---- 8. A rewritten env file is picked up, not served from cache ----

# Backdated on purpose: copying an env file in with its mtime preserved is the
# documented worktree/QA setup step, so reuse cannot be decided by timestamp.
printf 'WHICH=a2\n' >"$work_dir/a/.env"
touch -d '2001-01-01 00:00:00' "$work_dir/a/.env"
frag_a2="$(bash scripts/env-make-include.sh "$work_dir/a/.env")"
grep -Fqx 'WHICH=a2' "$frag_a2" ||
  fail "a fragment was reused after its env file changed: $frag_a2"

echo "OK: env file inclusion survives multi-line values and preserves them"
