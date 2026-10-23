#!/usr/bin/env bash
# Verify the classic-script contract used by Android Expo DOM WebViews.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
mobile_dir="$(cd "$script_dir/.." && pwd)"
output_dir="$(mktemp -d "${TMPDIR:-/tmp}/multica-dom-bundle.XXXXXX")"

cleanup() {
  rm -rf "$output_dir"
}
trap cleanup EXIT

cd "$mobile_dir"
EXPO_NO_BUNDLE_SPLITTING=1 pnpm exec expo export --platform android --output-dir "$output_dir"

bundle_count="$(find "$output_dir/www.bundle" -maxdepth 1 -type f -name '*.js' | wc -l | tr -d ' ')"
if [ "$bundle_count" != "1" ]; then
  echo "Expected one Android DOM bundle, found $bundle_count" >&2
  exit 1
fi

bundle_path="$(find "$output_dir/www.bundle" -maxdepth 1 -type f -name '*.js' -print -quit)"
if rg -q -F 'import.meta' "$bundle_path"; then
  echo "Android DOM bundle contains unsupported import.meta syntax: $bundle_path" >&2
  exit 1
fi

printf 'Android DOM bundle verified: %s  %s\n' "$(sha256sum "$bundle_path" | cut -d ' ' -f 1)" "$bundle_path"
