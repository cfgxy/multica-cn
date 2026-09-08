#!/usr/bin/env bash
# Derives the GitHub Release tag for the `Desktop Release` workflow and writes
# `tag_name` / `version` / `prerelease` to $GITHUB_OUTPUT, and guards reuse of
# an already existing release.
#
#   desktop-release-tag.sh push        <ref-name>
#   desktop-release-tag.sh dispatch    <git-describe-output> [<git-describe-exact-match>]
#   desktop-release-tag.sh verify-reuse <event> <tag> <release-commit> <github-sha> <release-is-prerelease>
#
# Kept out of the workflow YAML so scripts/desktop-release-tag.test.sh can pin
# the mapping against what apps/desktop/scripts/package.mjs bakes into the
# installers. electron-builder's github publisher resolves the release by
# `v${version}`; if the two derivations drift apart, `--publish always` finds no
# matching release and the release ships with no installers attached.
#
# Publishing boundary: only a `vX.Y.Z` tag push may own a release named after a
# tag. Because package.mjs bakes `git describe` verbatim into the installers,
# a manual run sitting exactly on a tag cannot be given a separate prerelease
# name without breaking that mapping — so it is refused rather than allowed to
# overwrite the release that tag push owns.
set -euo pipefail

SEMVER_RE='^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'

mode=${1:-}
input=${2:-}
# dispatch only: `git describe --exact-match` output, empty when HEAD is not a
# tag. verify-reuse re-reads $2..$6 under its own names and ignores this.
exact_match=${3:-}

emit() {
  local tag=$1 prerelease=$2
  echo "tag_name=$tag" >>"${GITHUB_OUTPUT:-/dev/stdout}"
  echo "version=${tag#v}" >>"${GITHUB_OUTPUT:-/dev/stdout}"
  echo "prerelease=$prerelease" >>"${GITHUB_OUTPUT:-/dev/stdout}"
  echo "Desktop release tag: $tag (prerelease=$prerelease)" >&2
}

# A tag carrying a prerelease suffix (`vX.Y.Z-...`) is published as a
# prerelease; a bare `vX.Y.Z` is a stable release.
is_prerelease_tag() {
  [[ "$1" == *-* ]]
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
    if is_prerelease_tag "$input"; then
      emit "$input" true
    else
      emit "$input" false
    fi
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
    # `git describe` returns the bare tag only when HEAD *is* that tag; any
    # commit past it carries a `-<distance>-g<hash>` suffix. A bare tag here
    # means the manual run would land on the release the tag push owns, and
    # package.mjs would stamp the installers with that same version — so the
    # only safe answer is to refuse and let the tag push channel handle it.
    #
    # Two independent checks, because neither alone is sufficient:
    #   1. `git describe --exact-match`'s answer, passed in by the caller. It
    #      knows whether HEAD carries a tag; string shape does not.
    #   2. A syntax backstop for when that argument is absent: a describe value
    #      that is valid release-tag syntax yet carries no `-<distance>-g<hash>`
    #      suffix can only be a bare tag. This must cover every shape `push`
    #      accepts — including the hyphenated prerelease suffixes SEMVER_RE
    #      allows, e.g. `v1.0.0-rc-test` — or the manual channel would publish
    #      into a release the push channel considers its own.
    if [[ -n "$exact_match" ]]; then
      echo "::error::HEAD is exactly at release tag '$exact_match'. A manual run must not publish into a tagged release; push the tag to run the release channel, or dispatch from a commit past the tag." >&2
      exit 1
    fi
    if [[ "$input" =~ $SEMVER_RE ]] && [[ ! "$input" =~ -[0-9]+-g[0-9A-Za-z]+$ ]]; then
      echo "::error::HEAD is exactly at release tag '$input'. A manual run must not publish into a tagged release; push the tag to run the release channel, or dispatch from a commit past the tag." >&2
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
    # Everything reaching here is a describe value with a distance/hash suffix
    # or a `0.0.0-g<hash>` fallback: never a release point, always a prerelease.
    emit "$tag" true
    ;;

  verify-reuse)
    event=${2:-}
    tag=${3:-}
    release_commit=${4:-}
    build_commit=${5:-}
    release_is_prerelease=${6:-}

    if [[ -z "$release_commit" ]]; then
      echo "::error::Release '$tag' already exists but its target commit could not be resolved; refusing to publish into it." >&2
      exit 1
    fi
    if [[ "$release_commit" != "$build_commit" ]]; then
      echo "::error::Release '$tag' already exists and points at $release_commit, not the commit being built ($build_commit). Refusing to overwrite it." >&2
      exit 1
    fi
    if [[ "$event" != "push" && "$release_is_prerelease" != "true" ]]; then
      echo "::error::Release '$tag' is a published (non-prerelease) release; a manual run must not publish into it." >&2
      exit 1
    fi
    echo "Release $tag already exists at $release_commit (this build); installers will be uploaded into it." >&2
    ;;

  *)
    echo "::error::Unknown mode '$mode'; expected 'push' or 'dispatch'." >&2
    exit 1
    ;;
esac
