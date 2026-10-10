#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
worktree_input="${1:-}"

if [ -z "$worktree_input" ]; then
  echo "Usage: make remove-worktree WORKTREE=/path/to/worktree" >&2
  exit 1
fi

repo_root="$(git rev-parse --show-toplevel)"
if [[ "$worktree_input" = /* ]]; then
  candidate="$worktree_input"
else
  candidate="$PWD/$worktree_input"
fi

if [ ! -d "$candidate" ]; then
  echo "Worktree directory does not exist: $worktree_input" >&2
  exit 1
fi

target="$(cd "$candidate" && pwd -P)"
current_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
current="$(cd "$current_root" && pwd -P)"
primary=""
registered=0
locked=0
listed_path=""

while IFS= read -r line || [ -n "$line" ]; do
  case "$line" in
    worktree\ *)
      listed_path="${line#worktree }"
      if [ -z "$primary" ]; then
        primary="$listed_path"
      fi
      if [ "$listed_path" = "$target" ]; then
        registered=1
      fi
      ;;
    locked*)
      if [ "$listed_path" = "$target" ]; then
        locked=1
      fi
      ;;
  esac
done < <(git -C "$repo_root" worktree list --porcelain)

if [ "$registered" != "1" ]; then
  echo "Not a registered worktree of this repository: $target" >&2
  exit 1
fi

if [ "$target" = "$primary" ]; then
  echo "Refusing to remove the primary checkout: $target" >&2
  exit 1
fi

if [ "$target" = "$current" ]; then
  echo "Refusing to remove the current worktree from inside itself." >&2
  echo "Run this command from another checkout." >&2
  exit 1
fi

if [ "$locked" = "1" ]; then
  echo "Refusing to remove locked worktree: $target" >&2
  exit 1
fi

if [ -n "$(git -C "$target" status --porcelain --untracked-files=all)" ]; then
  echo "Refusing to remove dirty worktree: $target" >&2
  echo "Commit, stash, or discard its changes first." >&2
  exit 1
fi

# Recycle guard (RUYI-594): the same scan the daemon and slot destroy run,
# so every removal path shares one notion of "safe to delete". Blocks
# sole-reference commits and carriers git cannot read (fail-closed); notes
# stash/unpushed content that survives in the shared repository; and writes
# an evidence file either way under ~/.multica/recycle-evidence/.
# shellcheck source=lib-recycle-guard.sh
source "$script_dir/lib-recycle-guard.sh"
RG_TOOL="remove-worktree.sh"
if ! recycle_guard_scan_worktree "$target" worktree 0; then
  echo "Recycle guard blocks removing worktree: $target" >&2
  printf '%s\n' "$RG_REASONS" | sed 's/^/  /' >&2
  echo "  Preserve the work (push, branch, or bundle), then re-run once with MULTICA_RECYCLE_OVERRIDE=1." >&2
  echo "Evidence: $RG_EVIDENCE_FILE" >&2
  exit 1
fi
if [ -n "$RG_NOTES" ]; then
  printf 'Guard notes:\n%s\n' "$RG_NOTES" | sed 's/^/  /'
fi
if [ -n "$RG_EVIDENCE_FILE" ]; then
  echo "Recycle evidence: $RG_EVIDENCE_FILE"
fi

worktree_env="$target/.env.worktree"
if [ -f "$worktree_env" ]; then
  db_name="$(bash -c 'set -a; . "$1"; printf "%s" "${POSTGRES_DB:-}"' _ "$worktree_env")"
  if [ "$db_name" = "multica" ]; then
    echo "Preserving shared main database 'multica'."
  else
    db_drop_status=0
    bash "$script_dir/drop-database.sh" "$worktree_env" || db_drop_status=$?
    case "$db_drop_status" in
      0) ;;
      2)
        echo "Worktree was not removed."
        exit 0
        ;;
      *) exit "$db_drop_status" ;;
    esac
  fi
else
  echo "No .env.worktree found; skipping database cleanup."
fi

git -C "$repo_root" worktree remove "$target"
echo "Removed worktree '$target'."
