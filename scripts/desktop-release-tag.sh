#!/usr/bin/env bash
# Derives the GitHub Release tag for the `Desktop Release` workflow and writes
# `tag_name` / `version` to $GITHUB_OUTPUT.
#
#   desktop-release-tag.sh push     <ref-name>
#   desktop-release-tag.sh dispatch <git-describe-output>
#
# Kept out of the workflow YAML so scripts/desktop-release-tag.test.sh can pin
# the mapping against what apps/desktop/scripts/package.mjs bakes into the
# installers. electron-builder's github publisher resolves the release by
# `v${version}`; if the two derivations drift apart, `--publish always` finds no
# matching release and the release ships with no installers attached.
set -euo pipefail

SEMVER_RE='^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'

mode=${1:-}
input=${2:-}

emit() {
  local tag=$1
  echo "tag_name=$tag" >>"${GITHUB_OUTPUT:-/dev/stdout}"
  echo "version=${tag#v}" >>"${GITHUB_OUTPUT:-/dev/stdout}"
  echo "Desktop release tag: $tag" >&2
}

case "$mode" in
  push)
    if [[ -z "$input" ]]; then
      echo "::error::desktop-release-tag.sh push requires the pushed ref name." >&2
      exit 1
    fi
    if [[ "$input" == *-dirty* ]]; then
      echo "::error::Refusing to release from dirty tag '$input'." >&2
      exit 1
    fi
    if [[ ! "$input" =~ $SEMVER_RE ]]; then
      echo "::error::Release tags must look like vX.Y.Z or vX.Y.Z-suffix; got '$input'." >&2
      exit 1
    fi
    emit "$input"
    ;;

  dispatch)
    if [[ -z "$input" ]]; then
      echo "::error::git describe produced no output; cannot name the release." >&2
      exit 1
    fi
    if [[ "$input" == *-dirty* ]]; then
      echo "::error::Refusing to release from a dirty checkout ('$input')." >&2
      exit 1
    fi
    if [[ "$input" == v* ]]; then
      tag="$input"
    else
      # No reachable tag: `git describe --always` fell back to a bare commit
      # hash, which is never valid semver. package.mjs normalizes it to
      # 0.0.0-g<hash> (the `g` keeps an all-digit hash like `0123456` from
      # forming the invalid `0.0.0-0123456`); mirror that exactly.
      tag="v0.0.0-g${input}"
    fi
    if [[ ! "$tag" =~ $SEMVER_RE ]]; then
      echo "::error::Derived tag '$tag' is not a valid version tag." >&2
      exit 1
    fi
    emit "$tag"
    ;;

  *)
    echo "::error::Unknown mode '$mode'; expected 'push' or 'dispatch'." >&2
    exit 1
    ;;
esac
