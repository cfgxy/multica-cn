#!/usr/bin/env bash
# Sweep what build/QA phases leave behind in ~/.multica/qa.
#
# The 2026-09-30 incident: three QA dev stacks (next-server + Go api, ~10 GB
# RSS) ran for half a day after their sessions ended, because the QA
# environments were registered OWNER=human TTL=0 and no command existed to
# reclaim them short of manual archaeology. This script is the reclaim
# command — and the one agents are told to run at the end of a QA turn (the
# runtime brief's Background Task Safety section pins it).
#
# Layers, from safe to destructive:
#   1. registered envs whose directory lives under the QA root
#      -> `dev-env.sh destroy` (stops process groups, drops the env database,
#         frees the slot) — the same verb `make gc` uses.
#   2. orphan listeners: any TCP listener whose cwd is inside the QA root and
#      whose owning env is gone (or was just destroyed above) -> TERM then
#      KILL, by process group. `multica daemon` processes are never touched.
#   3. docker containers whose name/image references the target issue
#      -> `docker rm -f` with --yes; generic (no --issue) container sweep
#      additionally requires --docker.
#   4. QA worktree directories are REPORTED ONLY, never deleted — they hold
#      unmerged agent commits. Use scripts/remove-worktree.sh for those.
#
# Without --yes nothing is modified: everything prints as "would ...".

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/dev-env.sh
source "$SCRIPT_DIR/dev-env.sh"

usage() {
  cat <<'EOF'
Reclaim build/QA leftovers under the QA checkout root (~/.multica/qa).

  qa-clean.sh                      # report only (dry run)
  qa-clean.sh --issue ruyi-283     # scope the sweep to one issue
  qa-clean.sh --yes                # execute: destroy envs, kill orphans
  qa-clean.sh --issue ruyi-283 --yes --docker
                                   # also remove containers naming the issue

Registered dev environments are destroyed via scripts/dev-env.sh destroy
(processes stopped, env database dropped, slot freed). Listener processes
whose cwd sits inside the QA root are killed by process group — except any
`multica daemon`. Without --issue, the container sweep only matches names
carrying both this product (multica/ruyi) and "qa"; with --issue it matches
the issue id (separator-insensitive). Worktrees are never deleted here; they
may hold unmerged commits and are listed with their ahead-of-main count
instead.
EOF
}

issue="" assume_yes=0 sweep_docker=0
while [ $# -gt 0 ]; do
  case "$1" in
    --issue) issue="${2:-}"; [ -n "$issue" ] || die "--issue needs a value"; shift 2 ;;
    --issue=*) issue="${1#*=}"; [ -n "$issue" ] || die "--issue= needs a value"; shift ;;
    --yes|-y) assume_yes=1; shift ;;
    --docker) sweep_docker=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done

issue_lc="$(printf '%s' "$issue" | tr '[:upper:]' '[:lower:]')"
# Naming is not consistent across QA envs ("ruyi-283", "ruyi283", "ruyi_283"
# all exist in the wild), so id matching ignores separators before comparing.
flatten_id() { printf '%s' "$1" | tr -d -- '-'; }
issue_flat="$(flatten_id "$issue_lc")"
matches_issue() { # $1 = env name, $2 = env dir
  [ -z "$issue_lc" ] && return 0
  case "${1:-} ${2:-}" in *"$issue_lc"*) return 0 ;; esac
  case "$(flatten_id "${1:-} ${2:-}")" in *"$issue_flat"*) return 0 ;; esac
  return 1
}

if [ "$assume_yes" != 1 ]; then
  step "qa-clean dry run — nothing will be changed (add --yes to execute)"
else
  step "qa-clean: reclaiming QA leftovers under $QA_ROOT"
fi
[ -z "$issue_lc" ] || info "scoped to issue: $issue_lc"

# --- 1. registered environments under the QA root ---------------------------

env_hits=0
while read -r name; do
  [ -n "$name" ] || continue
  (
    load_manifest "$name" || exit 0
    dir_is_qa_checkout "$DIR" || exit 0
    matches_issue "$NAME" "$DIR" || exit 0
    if [ "$assume_yes" != 1 ]; then
      printf 'would destroy environment %s (dir %s, db %s)\n' "$NAME" "$DIR" "$DB_NAME"
    else
      if bash "$SCRIPT_DIR/dev-env.sh" destroy "$NAME" --yes; then
        printf 'destroyed environment %s\n' "$NAME"
      else
        warn "failed to destroy environment $NAME; its manifest was kept for retry"
      fi
    fi
  ) || true
done <<EOF
$(list_env_names)
EOF

# --- 2. orphan listeners with a cwd inside the QA root ----------------------

qa_dir_matches_issue() {
  [ -z "$issue_lc" ] && return 0
  matches_issue "" "$1"
}

self_pid=$$
protected_pid="$(ps -o ppid= -p "$self_pid" 2>/dev/null | tr -d ' ' || true)"
orphans=""
if command -v lsof >/dev/null 2>&1; then
  for pid in $(lsof -nP -iTCP -sTCP:LISTEN 2>/dev/null | awk 'NR>1 {print $2}' | sort -u); do
    [ -n "$pid" ] || continue
    [ "$pid" != "$self_pid" ] && [ "$pid" != "$protected_pid" ] || continue
    cwd="$(readlink "/proc/$pid/cwd" 2>/dev/null || true)"
    [ -n "$cwd" ] || continue
    [ "${cwd#"$QA_ROOT"}" != "$cwd" ] || continue   # cwd must start with QA_ROOT
    qa_dir_matches_issue "$cwd" || continue
    cmd="$(ps -o cmd= -p "$pid" 2>/dev/null || true)"
    case "$cmd" in *"multica daemon"*) continue ;; esac
    orphans="$orphans $pid"
  done
else
  warn "lsof not found; skipping orphan-listener sweep"
fi

if [ -z "$orphans" ]; then
  info "no orphan listeners with a cwd under $QA_ROOT"
else
  for pid in $orphans; do
    cwd="$(readlink "/proc/$pid/cwd" 2>/dev/null || echo '?')"
    desc="$(ps -o args= -p "$pid" 2>/dev/null | head -c 100 || true)"
    if [ "$assume_yes" != 1 ]; then
      printf 'would kill pid %s (cwd %s): %s\n' "$pid" "$cwd" "$desc"
    else
      pgid="$(ps -o pgid= -p "$pid" 2>/dev/null | tr -d ' ' || true)"
      if [ -z "$pgid" ]; then
        warn "pid $pid already exited"
        continue
      fi
      kill -TERM -- "-$pgid" 2>/dev/null || true
      sleep 3
      if kill -0 -- "-$pgid" 2>/dev/null; then
        kill -KILL -- "-$pgid" 2>/dev/null || true
      fi
      printf 'killed process group %s (pid %s, cwd %s)\n' "$pgid" "$pid" "$cwd"
    fi
  done
fi

# --- 3. docker containers referencing the issue -----------------------------

if command -v docker >/dev/null 2>&1; then
  containers="$(docker ps --format '{{.ID}}\t{{.Names}}\t{{.Image}}' 2>/dev/null || true)"
  while IFS=$'\t' read -r cid cname cimage; do
    [ -n "$cid" ] || continue
    label="$(printf '%s %s' "$cname" "$cimage" | tr '[:upper:]' '[:lower:]')"
    if [ -n "$issue_lc" ]; then
      case "$(flatten_id "$label")" in *"$issue_flat"*) ;; *) continue ;; esac
    else
      # No issue scope: only containers that name this product are in scope.
      # A bare "*qa*" match would sweep every other project's QA stack
      # (sub2api-qa*, shanghui-*-qa* all live on shared machines).
      case "$label" in *multica*|*ruyi*) ;;
        *) continue ;;
      esac
      case "$label" in *qa*) ;; *) continue ;; esac
    fi
    if [ "$sweep_docker" != 1 ]; then
      printf 'matched container %s (%s); add --docker to remove it on a --yes run\n' "$cname" "$cimage"
      continue
    fi
    if [ "$assume_yes" != 1 ]; then
      printf 'would remove container %s (%s)\n' "$cname" "$cimage"
    else
      docker rm -f "$cid" >/dev/null && printf 'removed container %s\n' "$cname" \
        || warn "failed to remove container $cname"
    fi
  done <<EOF2
$containers
EOF2
  [ -n "$containers" ] || info "no running containers matched"
else
  info "docker not found; skipping container sweep"
fi

# --- 4. QA checkouts: report only, never delete -----------------------------

if [ -d "$QA_ROOT" ]; then
  step "QA checkout directories (reported only — they may hold unmerged commits)"
  for qa_dir in "$QA_ROOT"/*/; do
    [ -d "$qa_dir" ] || continue
    qa_dir_matches_issue "$qa_dir" || continue
    summary="dir $qa_dir, last modified $(stat -c %y "$qa_dir" 2>/dev/null | cut -d. -f1 || echo '?')"
    wt="$(find "$qa_dir" -maxdepth 2 -name .git -type f 2>/dev/null | head -1 || true)"
    if [ -n "$wt" ]; then
      tree="$(dirname "$wt")"
      branch="$(git -C "$tree" rev-parse --abbrev-ref HEAD 2>/dev/null || echo detached)"
      ahead="$(git -C "$tree" rev-list --count origin/main..HEAD 2>/dev/null || echo '?')"
      summary="$summary · worktree $tree on $branch, $ahead commit(s) ahead of origin/main"
    fi
    info "$summary"
  done
  info "reclaim a checkout by hand: scripts/remove-worktree.sh, or rm -rf after the branch is merged"
else
  info "$QA_ROOT does not exist — nothing to sweep"
fi

[ "$assume_yes" = 1 ] && ok "qa-clean finished" || ok "qa-clean report finished (dry run)"
