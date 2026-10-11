#!/usr/bin/env bash
# artifact-install.sh — startup unpack/load for pool slot leases (plan §3.7
# "启动解压语义"): takes artifacts from the lease mount dir, verifies every
# sha256/size against a manifest BEFORE touching anything, then unpacks each
# into its target with an atomic temp-dir + rename swap. Re-running with the
# same inputs reproduces the same deploy tree (idempotent); any verification
# or unpack failure aborts with the deploy dir untouched — a failed load is
# an environment fault, never a half-deployed state.
#
# Usage:
#   artifact-install.sh <mount-dir> <manifest.json> <deploy-dir>
# Manifest:
#   {"artifacts":[{"name":"web-dist.zip","sha256":"<hex>","size_bytes":123,
#                  "target":"srv/web/dist"}, ...]}
#     name       file inside <mount-dir> (flat)
#     sha256     required; lowercase hex
#     size_bytes optional; enforced when present
#     target     relative path under <deploy-dir> (created as needed)
# Output: one "installed: <name> -> <target>" line per artifact on success.
# Exit codes: 0 installed · 1 environment fault (fail-fast, nothing deployed).
set -uo pipefail

usage() { printf 'usage: artifact-install.sh <mount-dir> <manifest.json> <deploy-dir>\n' >&2; exit 1; }

mount_dir=${1:-}; manifest=${2:-}; deploy_dir=${3:-}
[ -n "$mount_dir" ] && [ -n "$manifest" ] && [ -n "$deploy_dir" ] || usage
[ -d "$mount_dir" ] || { printf 'artifact-install: mount dir %s not found\n' "$mount_dir" >&2; exit 1; }
[ -f "$manifest" ] || { printf 'artifact-install: manifest %s not found\n' "$manifest" >&2; exit 1; }

command -v jq >/dev/null || { printf 'artifact-install: jq is required\n' >&2; exit 1; }
command -v unzip >/dev/null || { printf 'artifact-install: unzip is required\n' >&2; exit 1; }

mkdir -p "$deploy_dir"
work="$(mktemp -d "$deploy_dir/.incoming.XXXXXXXX")"
cleanup() { # on failure also drop the deploy shell we created (rmdir: only if empty)
  local rc=$?
  rm -rf "$work"
  if [ "$rc" -gt 0 ]; then rmdir "$deploy_dir" 2>/dev/null || true; fi
}
trap cleanup EXIT

count="$(jq -r '.artifacts | length' "$manifest" 2>/dev/null)" || count=""
case "$count" in ''|*[!0-9]*) printf 'artifact-install: manifest has no artifacts array\n' >&2; exit 1 ;; esac
[ "$count" -gt 0 ] || { printf 'artifact-install: manifest lists no artifacts\n' >&2; exit 1; }

# Pass 1 — verify everything against the manifest. No deploy-dir writes here
# (the staging dir is inside deploy but dot-hidden; targets are untouched).
i=0
while [ "$i" -lt "$count" ]; do
  name="$(jq -r ".artifacts[$i].name" "$manifest")"
  want_sha="$(jq -r ".artifacts[$i].sha256" "$manifest")"
  want_size="$(jq -r ".artifacts[$i].size_bytes // empty" "$manifest")"
  target="$(jq -r ".artifacts[$i].target" "$manifest")"

  [ -n "$name" ] && [ "$name" != "null" ] || { printf 'artifact-install: artifact %d has no name\n' "$i" >&2; exit 1; }
  [ -n "$target" ] && [ "$target" != "null" ] || { printf 'artifact-install: artifact %s has no target\n' "$name" >&2; exit 1; }
  case "$target" in */*..*|..*|*..) printf 'artifact-install: unsafe target %s\n' "$target" >&2; exit 1 ;; esac

  file="$mount_dir/$name"
  if [ ! -f "$file" ]; then
    printf 'artifact-install: artifact %s missing from mount dir %s\n' "$name" "$mount_dir" >&2
    exit 1
  fi
  have_sha="$(sha256sum "$file" | awk '{print $1}')"
  if [ "$have_sha" != "$want_sha" ]; then
    printf 'artifact-install: artifact %s sha256 mismatch: manifest %s, actual %s\n' \
      "$name" "$want_sha" "$have_sha" >&2
    exit 1
  fi
  if [ -n "$want_size" ]; then
    have_size="$(stat -c %s "$file")"
    if [ "$have_size" != "$want_size" ]; then
      printf 'artifact-install: artifact %s size mismatch: manifest %s, actual %s\n' \
        "$name" "$want_size" "$have_size" >&2
      exit 1
    fi
  fi

  # Stage the unpack under the hidden work dir now; the swap happens in pass 2.
  stage="$work/stage.$i"
  mkdir -p "$stage"
  if ! unzip -q "$file" -d "$stage" </dev/null; then
    printf 'artifact-install: artifact %s failed to unpack (corrupt archive?)\n' "$name" >&2
    exit 1
  fi
  i=$((i + 1))
done

# Pass 2 — swap each staged tree into place. Each target switches atomically
# (rename), and a swap never leaves the old tree missing on failure.
i=0
while [ "$i" -lt "$count" ]; do
  name="$(jq -r ".artifacts[$i].name" "$manifest")"
  target="$(jq -r ".artifacts[$i].target" "$manifest")"
  dest="$deploy_dir/$target"
  stage="$work/stage.$i"

  mkdir -p "$(dirname "$dest")"
  if [ -e "$dest" ]; then
    trash="$work/.trash.$i"
    mv "$dest" "$trash" || { printf 'artifact-install: cannot move aside %s\n' "$dest" >&2; exit 1; }
  fi
  if ! mv "$stage" "$dest"; then
    printf 'artifact-install: cannot install %s -> %s\n' "$name" "$dest" >&2
    [ -n "${trash:-}" ] && [ -e "${trash:-}" ] && mv "$trash" "$dest"
    exit 1
  fi
  printf 'installed: %s -> %s\n' "$name" "$target"
  i=$((i + 1))
done
