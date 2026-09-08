#!/usr/bin/env bash
# Contract test for scripts/desktop-release-tag.sh.
#
# The tag this script prints is what `Desktop Release` creates the GitHub
# Release under, while the installers' own version comes from
# apps/desktop/scripts/package.mjs (`git describe` → normalizeGitVersion).
# electron-builder's github publisher then looks for the release named
# `v${version}`. If the two derivations ever disagree, `--publish always`
# uploads nothing and the release ships empty — so the cases below pin the
# mapping on both sides.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$ROOT_DIR/scripts/desktop-release-tag.sh"

failures=0

fail() {
  echo "FAIL: $1"
  failures=$((failures + 1))
}

# Runs the script and asserts on `tag_name=`/`version=`/`prerelease=` in its
# GITHUB_OUTPUT. The expected prerelease flag is optional: pass it as the fourth
# argument to assert on it.
expect_output() {
  local label=$1 expected_tag=$2 expected_version=$3 expected_prerelease=$4
  shift 4

  local out_file
  out_file="$(mktemp)"
  if ! GITHUB_OUTPUT="$out_file" bash "$SCRIPT" "$@" >/dev/null 2>&1; then
    fail "$label: script exited non-zero"
    rm -f "$out_file"
    return
  fi

  local actual_tag actual_version actual_prerelease
  actual_tag="$(sed -n 's/^tag_name=//p' "$out_file")"
  actual_version="$(sed -n 's/^version=//p' "$out_file")"
  actual_prerelease="$(sed -n 's/^prerelease=//p' "$out_file")"
  rm -f "$out_file"

  if [[ "$actual_tag" != "$expected_tag" ]]; then
    fail "$label: tag_name expected '$expected_tag', got '$actual_tag'"
  fi
  if [[ "$actual_version" != "$expected_version" ]]; then
    fail "$label: version expected '$expected_version', got '$actual_version'"
  fi
  if [[ -n "$expected_prerelease" && "$actual_prerelease" != "$expected_prerelease" ]]; then
    fail "$label: prerelease expected '$expected_prerelease', got '$actual_prerelease'"
  fi
}

# Runs the script and asserts only that it succeeds, for modes that emit no
# GITHUB_OUTPUT of their own.
expect_success() {
  local label=$1
  shift

  local out_file
  out_file="$(mktemp)"
  if ! GITHUB_OUTPUT="$out_file" bash "$SCRIPT" "$@" >/dev/null 2>&1; then
    fail "$label: script exited non-zero, expected success"
  fi
  rm -f "$out_file"
}

expect_failure() {
  local label=$1
  shift

  local out_file
  out_file="$(mktemp)"
  if GITHUB_OUTPUT="$out_file" bash "$SCRIPT" "$@" >/dev/null 2>&1; then
    fail "$label: script exited 0, expected failure"
  fi
  rm -f "$out_file"
}

# --- push channel: the tag itself is the release name ------------------------

expect_output "stable tag" "v0.4.42" "0.4.42" "false" push v0.4.42
expect_output "prerelease tag" "v1.0.0-rc.1" "1.0.0-rc.1" "true" push v1.0.0-rc.1

expect_failure "non-semver tag" push desktop-latest
expect_failure "missing patch segment" push v1.2
expect_failure "dirty tag" push v0.4.42-dirty
expect_failure "empty tag on push" push ""

# --- dispatch channel: `git describe` decides the release name ---------------
#
# The manual channel must never name, reuse or write into a release that a tag
# push owns. package.mjs bakes `git describe` straight into the installer and
# electron-builder resolves the release by `v${version}`, so a dispatch that
# lands exactly on a tag cannot be renamed to a separate prerelease without
# breaking that mapping — it is refused instead (fail closed).

expect_failure "dispatch exactly on a stable tag" dispatch v0.4.42
expect_failure "dispatch exactly on a prerelease tag" dispatch v1.0.0-rc.1

# Commits past a tag: `git describe`'s own prerelease form is already valid
# semver once the leading `v` is stripped, so it passes through untouched, and
# the `-N-g<hash>` distance suffix proves it is not a tagged release point.
expect_output "dispatch past a tag" \
  "v0.4.41-182-g0a76682d0" "0.4.41-182-g0a76682d0" "true" dispatch v0.4.41-182-g0a76682d0

# No reachable tag: package.mjs coerces a bare hash to 0.0.0-g<hash> because a
# hash is never valid semver. The tag must follow it, `g` prefix included.
expect_output "dispatch with no reachable tag" \
  "v0.0.0-g0a76682d0" "0.0.0-g0a76682d0" "true" dispatch 0a76682d0
expect_output "dispatch with all-digit hash" \
  "v0.0.0-g0123456" "0.0.0-g0123456" "true" dispatch 0123456

expect_failure "dispatch from dirty tree" dispatch v0.4.41-182-g0a76682d0-dirty
expect_failure "dispatch with empty describe" dispatch ""

# --- reuse guard: an already existing release ---------------------------------
#
# `verify-reuse <event> <tag> <release-commit> <github-sha> <release-is-prerelease>`
# decides whether the workflow may upload into a release that already exists.
# Reuse is only safe when that release points at the very commit being built.

expect_success "reuse own release on tag re-run" \
  verify-reuse push v0.4.42 aaaaaaaa aaaaaaaa false
expect_success "reuse own prerelease on dispatch re-run" \
  verify-reuse dispatch v0.4.41-182-g0a76682d0 bbbbbbbb bbbbbbbb true

expect_failure "reuse release built from another commit" \
  verify-reuse push v0.4.42 aaaaaaaa cccccccc false
expect_failure "dispatch reusing a stable release" \
  verify-reuse dispatch v0.4.42 aaaaaaaa aaaaaaaa false
expect_failure "reuse with unresolvable release commit" \
  verify-reuse push v0.4.42 "" aaaaaaaa false

# --- unknown channel ---------------------------------------------------------

expect_failure "unknown mode" schedule v0.4.42

if (( failures > 0 )); then
  echo "desktop-release-tag.test.sh: $failures assertion(s) failed"
  exit 1
fi

echo "desktop-release-tag.test.sh: all assertions passed"
