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

# Runs the script and asserts on `tag_name=`/`version=` in its GITHUB_OUTPUT.
expect_output() {
  local label=$1 expected_tag=$2 expected_version=$3
  shift 3

  local out_file
  out_file="$(mktemp)"
  if ! GITHUB_OUTPUT="$out_file" bash "$SCRIPT" "$@" >/dev/null 2>&1; then
    fail "$label: script exited non-zero"
    rm -f "$out_file"
    return
  fi

  local actual_tag actual_version
  actual_tag="$(sed -n 's/^tag_name=//p' "$out_file")"
  actual_version="$(sed -n 's/^version=//p' "$out_file")"
  rm -f "$out_file"

  if [[ "$actual_tag" != "$expected_tag" ]]; then
    fail "$label: tag_name expected '$expected_tag', got '$actual_tag'"
  fi
  if [[ "$actual_version" != "$expected_version" ]]; then
    fail "$label: version expected '$expected_version', got '$actual_version'"
  fi
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

expect_output "stable tag" "v0.4.42" "0.4.42" push v0.4.42
expect_output "prerelease tag" "v1.0.0-rc.1" "1.0.0-rc.1" push v1.0.0-rc.1

expect_failure "non-semver tag" push desktop-latest
expect_failure "missing patch segment" push v1.2
expect_failure "dirty tag" push v0.4.42-dirty
expect_failure "empty tag on push" push ""

# --- dispatch channel: `git describe` decides the release name ---------------

# Exactly on a tag: same value package.mjs bakes into the installer.
expect_output "dispatch on tagged commit" "v0.4.42" "0.4.42" dispatch v0.4.42

# Commits past a tag: `git describe`'s own prerelease form is already valid
# semver once the leading `v` is stripped, so it passes through untouched.
expect_output "dispatch past a tag" \
  "v0.4.41-182-g0a76682d0" "0.4.41-182-g0a76682d0" dispatch v0.4.41-182-g0a76682d0

# No reachable tag: package.mjs coerces a bare hash to 0.0.0-g<hash> because a
# hash is never valid semver. The tag must follow it, `g` prefix included.
expect_output "dispatch with no reachable tag" \
  "v0.0.0-g0a76682d0" "0.0.0-g0a76682d0" dispatch 0a76682d0
expect_output "dispatch with all-digit hash" \
  "v0.0.0-g0123456" "0.0.0-g0123456" dispatch 0123456

expect_failure "dispatch from dirty tree" dispatch v0.4.41-182-g0a76682d0-dirty
expect_failure "dispatch with empty describe" dispatch ""

# --- unknown channel ---------------------------------------------------------

expect_failure "unknown mode" schedule v0.4.42

if (( failures > 0 )); then
  echo "desktop-release-tag.test.sh: $failures assertion(s) failed"
  exit 1
fi

echo "desktop-release-tag.test.sh: all assertions passed"
