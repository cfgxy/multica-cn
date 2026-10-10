#!/usr/bin/env bash
# Verifiable production-build entry for Playwright E2E (RUYI-632).
#
# QA round 4 (2026-10-10) could not turn 45-case E2E results into a business
# verdict because the web entry was a source dev server: no way to tie the
# served HTML/JS back to a git SHA + artifact digest. This script closes that
# gap:
#
#   scripts/e2e-release-entry.sh dev1 build    # production build + manifest
#   scripts/e2e-release-entry.sh dev1 verify   # re-hash artifacts vs manifest
#
# `build` runs `next build` inside the slot-bound worktree (manifest DIR) with
# the slot env exported, then writes e2e-artifacts/manifest.json + a
# file-hashes.txt covering every file under apps/web/.next except the
# webpack cache (not part of the served artifact). `verify` recomputes the
# digest into a temp copy (never touching the recorded evidence) so a QA
# report can cite "manifest sha256 == recomputed" as mechanical evidence.
# Serve the artifact with:
#
#   MULTICA_WEB_MODE=release scripts/dev-env.sh dev1 up --components api,web
#
# Full runbook: e2e/README.md.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Platform runtime injects this block into AGENTS.md on every worktree; it is
# not part of the tested delta and must not block a build (RUYI-632).
DIRTY_EXEMPT_FILES="AGENTS.md"

usage() {
  cat <<'EOF'
usage: scripts/e2e-release-entry.sh <slot> <verb> [--env-file <path>]

  slot: dev1 | dev2 (the slot whose bound worktree should be built)
  verb: build | verify

  build   production-build the web app in the slot worktree and write
          e2e-artifacts/manifest.json + file-hashes.txt there.
  verify  recompute the artifact digest and compare against the manifest;
          exit 0 with a QA-citable summary, non-zero on any mismatch.

options:
  --env-file <path>   env file for the build (default: the slot env file from
                      the manifest). NEXT_PUBLIC_* values are baked at build
                      time, so this must be the same env the slot serves with.
EOF
  exit 2
}

fail() { printf 'e2e-release-entry: %s\n' "$*" >&2; exit 1; }
info() { printf '==> %s\n' "$*"; }
ok() { printf '    %s\n' "$*"; }

manifest_field() { # $1 = manifest.env path, $2 = key
  grep -E "^${2}=" "$1" 2>/dev/null | head -1 | cut -d= -f2-
}

sha256_file() { sha256sum "$1" 2>/dev/null | cut -d' ' -f1; }

# ---------------------------------------------------------------- parsing ---

SLOT="${1:-}"
VERB="${2:-}"
shift 2 2>/dev/null || usage
[ -n "$SLOT" ] && [ -n "$VERB" ] || usage
case "$VERB" in build|verify) ;; *) usage ;; esac

ENV_FILE_OVERRIDE=""
while [ $# -gt 0 ]; do
  case "$1" in
    --env-file) ENV_FILE_OVERRIDE="${2:-}"; shift 2 ;;
    *) usage ;;
  esac
done

SLOT_HOME="${MULTICA_SLOTS_HOME:-$HOME/.multica/slots}"
SLOT_DIR="$SLOT_HOME/$SLOT"
SLOT_MANIFEST="$SLOT_DIR/manifest.env"
[ -f "$SLOT_MANIFEST" ] || fail "no manifest at $SLOT_MANIFEST — bind the slot first: MULTICA_CALLER_OWNER=<issue> scripts/dev-env.sh $SLOT use"

BOUND_DIR="$(manifest_field "$SLOT_MANIFEST" DIR)"
CODE_SHA="$(manifest_field "$SLOT_MANIFEST" CODE_SHA)"
[ -n "$BOUND_DIR" ] && [ -d "$BOUND_DIR" ] || fail "slot manifest DIR missing or not a directory: '$BOUND_DIR'"
[ -f "$BOUND_DIR/package.json" ] || fail "$BOUND_DIR does not look like a multica checkout"

ENV_FILE="${ENV_FILE_OVERRIDE:-$(manifest_field "$SLOT_MANIFEST" ENV_FILE)}"
[ -n "$ENV_FILE" ] && [ -f "$ENV_FILE" ] || fail "env file not found: '$ENV_FILE' (pass --env-file)"

ARTIFACT_DIR="$BOUND_DIR/e2e-artifacts"
WEB_APP="$BOUND_DIR/apps/web"
NEXT_DIR="$WEB_APP/.next"
HASHES_FILE="$ARTIFACT_DIR/file-hashes.txt"
MANIFEST_JSON="$ARTIFACT_DIR/manifest.json"

# Artifact set: everything next serves from .next except cache/ (webpack
# build cache — huge, never served, and machine-specific). NUL-delimited and
# LC_ALL=C sorted so the enumeration itself is deterministic. find prints
# paths without a leading ./ (start point is the bare `.next`), so the
# exclusion pattern must not carry one either.
artifact_files() {
  cd "$WEB_APP" && find .next -type f ! -path '.next/cache/*' -print0 | LC_ALL=C sort -z
}

write_file_hashes() { # $1 = output path for `<sha256>  <relpath>` lines
  local out="$1"
  artifact_files | while IFS= read -r -d '' f; do
    printf '%s  %s\n' "$(sha256_file "$WEB_APP/$f")" "${f#./}"
  done > "$out"
}

# -------------------------------------------------------------- dirty tree ---

dirty_check() {
  local dirty_exempt="^($DIRTY_EXEMPT_FILES)$"
  local modified
  modified="$(git -C "$BOUND_DIR" status --porcelain --untracked-files=no | grep -Ev "$dirty_exempt" || true)"
  [ -z "$modified" ] || fail "slot worktree has uncommitted tracked changes besides {$DIRTY_EXEMPT_FILES}:
$modified
The artifact must be traceable to a commit; commit or stash first."
}

# ------------------------------------------------------------------ build ---

cmd_build() {
  dirty_check
  [ -d "$BOUND_DIR/node_modules" ] || { info "node_modules missing; running pnpm install"; (cd "$BOUND_DIR" && pnpm install) || fail "pnpm install failed"; }

  # NEXT_PUBLIC_* values are baked into the client bundle at build time, so
  # the build must see the same env the server will serve with.
  info "building web production bundle in $BOUND_DIR (env: $ENV_FILE)"
  (cd "$BOUND_DIR" && set -a && . "$ENV_FILE" && set +a && pnpm --filter @multica/web build) \
    || fail "web production build failed"
  [ -f "$NEXT_DIR/BUILD_ID" ] || fail "build finished but $NEXT_DIR/BUILD_ID is missing"

  mkdir -p "$ARTIFACT_DIR"
  info "hashing artifact files"
  write_file_hashes "$HASHES_FILE"

  local total_bytes file_count agg
  file_count="$(wc -l < "$HASHES_FILE" | tr -d ' ')"
  total_bytes="$(artifact_files | while IFS= read -r -d '' f; do stat -c %s "$WEB_APP/$f"; done | awk '{s+=$1} END{print s+0}')"
  agg="$(sha256_file "$HASHES_FILE")"

  local node_ver pnpm_ver next_ver branch bake built_at
  node_ver="$(node --version 2>/dev/null || echo unknown)"
  pnpm_ver="$(pnpm --version 2>/dev/null || echo unknown)"
  next_ver="$(node -e "console.log(require('$BOUND_DIR/node_modules/next/package.json').version)" 2>/dev/null || echo unknown)"
  branch="$(git -C "$BOUND_DIR" branch --show-current 2>/dev/null || true)"
  [ -n "$branch" ] || branch="DETACHED"
  # NEXT_PUBLIC_* are public by definition (baked into the served client
  # bundle); recording them proves which backend origin the artifact targets.
  bake="$(grep -E '^NEXT_PUBLIC_[A-Z_]+=' "$ENV_FILE" || true)"
  built_at="$(date -Iseconds)"

  node -e '
    const fs = require("fs");
    const m = {
      schema: "multica-e2e-release-entry/1",
      generated_at: process.argv[3],
      source: {
        git_sha: process.argv[4],
        git_branch: process.argv[5],
        worktree_path: process.argv[6],
        dirty_exempt_files: process.argv[7].split(","),
      },
      build: {
        built_at: process.argv[8],
        mode: "production",
        env_file: process.argv[9],
        env_file_sha256: process.argv[10],
        next_public_bake: process.argv[11],
        tool_versions: { node: process.argv[12], pnpm: process.argv[13], next: process.argv[14] },
        build_id: process.argv[15].trim(),
      },
      artifact: {
        root: "apps/web/.next (excluding .next/cache)",
        file_count: Number(process.argv[16]),
        total_bytes: Number(process.argv[17]),
        sha256: process.argv[18],
        sha256_definition: "sha256 of e2e-artifacts/file-hashes.txt (per-file `sha256sum  relpath` lines, LC_ALL=C sorted)",
        hashes_file: "e2e-artifacts/file-hashes.txt",
      },
    };
    fs.writeFileSync(process.argv[2], JSON.stringify(m, null, 2) + "\n");
  ' x "$MANIFEST_JSON" "$built_at" "$CODE_SHA" "$branch" "$BOUND_DIR" "$DIRTY_EXEMPT_FILES" \
    "$built_at" "$ENV_FILE" "$(sha256_file "$ENV_FILE")" "$bake" \
    "$node_ver" "$pnpm_ver" "$next_ver" "$(cat "$NEXT_DIR/BUILD_ID")" \
    "$file_count" "$total_bytes" "$agg" || fail "failed to write manifest"

  ok "build_id:      $(cat "$NEXT_DIR/BUILD_ID")"
  ok "git_sha:       $CODE_SHA"
  ok "artifact:      $file_count files, $total_bytes bytes"
  ok "sha256:        $agg"
  ok "manifest:      $MANIFEST_JSON"
  info "serve with: MULTICA_CALLER_OWNER=<issue> MULTICA_WEB_MODE=release scripts/dev-env.sh $SLOT up --components api,web"
}

# ----------------------------------------------------------------- verify ---

cmd_verify() {
  [ -f "$MANIFEST_JSON" ] || fail "no manifest at $MANIFEST_JSON — run build first"
  [ -f "$HASHES_FILE" ] || fail "no hashes file at $HASHES_FILE — run build first"
  [ -f "$NEXT_DIR/BUILD_ID" ] || fail "$NEXT_DIR/BUILD_ID missing — artifact not built in this worktree"

  info "verifying artifact digest against manifest (recomputed into a temp copy)"
  local tmp
  tmp="$(mktemp)"
  trap 'rm -f "$tmp"' RETURN

  local cur_agg man_agg man_sha man_build_id cur_sha cur_build_id man_files
  write_file_hashes "$tmp"
  cur_agg="$(sha256_file "$tmp")"
  man_agg="$(node -e 'console.log(JSON.parse(require("fs").readFileSync(process.argv[1],"utf8")).artifact.sha256)' "$MANIFEST_JSON")"
  man_sha="$(node -e 'console.log(JSON.parse(require("fs").readFileSync(process.argv[1],"utf8")).source.git_sha)' "$MANIFEST_JSON")"
  man_build_id="$(node -e 'console.log(JSON.parse(require("fs").readFileSync(process.argv[1],"utf8")).build.build_id)' "$MANIFEST_JSON")"
  man_files="$(node -e 'console.log(JSON.parse(require("fs").readFileSync(process.argv[1],"utf8")).artifact.file_count)' "$MANIFEST_JSON")"
  cur_build_id="$(cat "$NEXT_DIR/BUILD_ID")"
  cur_sha="$(git -C "$BOUND_DIR" rev-parse HEAD)"

  local failed=0
  if [ "$cur_agg" = "$man_agg" ]; then
    ok "artifact sha256 match: $cur_agg ($man_files files)"
  else
    printf '    MISMATCH artifact sha256: manifest=%s recomputed=%s\n' "$man_agg" "$cur_agg"
    printf '    changed files (manifest vs current, first 20):\n'
    diff "$HASHES_FILE" "$tmp" | head -20 || true
    failed=1
  fi
  if [ "$cur_build_id" = "$man_build_id" ]; then
    ok "BUILD_ID match: $cur_build_id"
  else
    printf '    MISMATCH BUILD_ID: manifest=%s current=%s\n' "$man_build_id" "$cur_build_id"
    failed=1
  fi
  if [ "$cur_sha" = "$man_sha" ]; then
    ok "git sha match: $cur_sha"
  else
    printf '    WARN worktree HEAD moved since build: manifest=%s current=%s (artifact still matches its manifest)\n' "$man_sha" "$cur_sha"
  fi

  if [ "$failed" -ne 0 ]; then
    fail "verify FAILED — the served artifact does not match the manifest; rebuild"
  fi
  printf 'e2e-release-entry: PASS — cite in the QA report as: release entry git %s, artifact sha256 %s\n' "$man_sha" "$man_agg"
}

case "$VERB" in
  build) cmd_build ;;
  verify) cmd_verify ;;
esac
