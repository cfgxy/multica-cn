#!/usr/bin/env bash
# Generate one human-reviewed shadow draft without granting the model platform credentials.
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/shadow-draft.sh --issue-ref RUYI-123 --snapshot FILE --audit-dir DIR

The snapshot is a human-exported, read-only issue context. The model process
receives no MULTICA_* variables and runs in Codex's read-only sandbox. This
script writes a draft and an append-only local ledger; it never calls Multica.
Linux bubblewrap namespace isolation and the required Codex exec options must
be available before the model process is started.
EOF
}

issue_ref=""
snapshot=""
audit_dir=""
timeout_seconds=${SHADOW_DRAFT_TIMEOUT_SECONDS:-120}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --issue-ref) issue_ref=${2:-}; shift 2 ;;
    --snapshot) snapshot=${2:-}; shift 2 ;;
    --audit-dir) audit_dir=${2:-}; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) printf 'unknown argument: %s\n' "$1" >&2; usage >&2; exit 2 ;;
  esac
done

[[ $issue_ref =~ ^[A-Z][A-Z0-9]{1,9}-[0-9]+$ ]] || { echo 'invalid --issue-ref' >&2; exit 2; }
[[ -f $snapshot ]] || { echo 'snapshot must be a regular file' >&2; exit 2; }
[[ -n $audit_dir ]] || { echo '--audit-dir is required' >&2; exit 2; }
[[ $timeout_seconds =~ ^[0-9]+$ ]] && (( timeout_seconds >= 1 && timeout_seconds <= 600 )) || {
  echo 'SHADOW_DRAFT_TIMEOUT_SECONDS must be an integer from 1 to 600' >&2
  exit 2
}
[[ -n ${CODEX_HOME:-} && -f $CODEX_HOME/auth.json ]] || {
  echo 'CODEX_HOME/auth.json is required for provider authentication' >&2
  exit 2
}
codex_bin=$(command -v codex) || { echo 'codex CLI is required' >&2; exit 2; }
codex_bin=$(readlink -f "$codex_bin")
node_bin=""
codex_package_root=""
if head -n 1 "$codex_bin" | grep -Fq '/env node'; then
  node_bin=$(command -v node) || { echo 'node runtime for Codex is required' >&2; exit 2; }
  node_bin=$(readlink -f "$node_bin")
  codex_package_root=$(dirname "$(dirname "$codex_bin")")
fi
command -v setsid >/dev/null || { echo 'setsid is required for process cleanup' >&2; exit 2; }
bwrap_bin=$(command -v bwrap) || { echo 'bubblewrap is required for Codex sandboxing' >&2; exit 2; }
codex_help=$("$codex_bin" exec --help 2>&1) || {
  echo 'unable to inspect Codex exec sandbox support' >&2
  exit 2
}
for required_flag in --ephemeral --ignore-user-config --ignore-rules --sandbox --skip-git-repo-check --cd; do
  grep -Fq -- "$required_flag" <<<"$codex_help" || {
    printf 'installed Codex CLI lacks required isolation option: %s\n' "$required_flag" >&2
    exit 2
  }
done
if ! "$bwrap_bin" --unshare-user --unshare-uts --unshare-ipc --die-with-parent \
  --ro-bind /usr /usr --symlink usr/lib /lib --symlink usr/lib64 /lib64 \
  --dir /etc --ro-bind /etc/ld.so.cache /etc/ld.so.cache \
  --proc /proc --dev /dev -- /usr/bin/true; then
  echo 'bubblewrap namespace isolation is unavailable' >&2
  exit 2
fi

mkdir -p "$audit_dir"
timestamp=$(date -u +%Y%m%dT%H%M%SZ)
# Atomically reserve a directory so even concurrent calls cannot share artifacts.
run_audit=$(mktemp -d "$audit_dir/${timestamp}-${issue_ref}-XXXXXX")
draft="$run_audit/shadow-draft.md"
: >"$draft"
ledger="$audit_dir/ledger.jsonl"
events="$run_audit/events.jsonl"
stderr_log="$run_audit/stderr.log"
run_home=$(mktemp -d)
run_input=$(mktemp)
run_context=$(mktemp -d)
install -m 400 "$snapshot" "$run_context/issue-snapshot"
codex_pid=""
watchdog_pid=""
timed_out_marker="$run_audit/timed-out"

cleanup() {
  if [[ -n $codex_pid ]]; then
    kill -TERM -- "-$codex_pid" 2>/dev/null || true
    sleep 0.1
    kill -KILL -- "-$codex_pid" 2>/dev/null || true
  fi
  if [[ -n $watchdog_pid ]]; then
    kill "$watchdog_pid" 2>/dev/null || true
    wait "$watchdog_pid" 2>/dev/null || true
  fi
  rm -rf "$run_home" "$run_context" "$run_input"
}
trap cleanup EXIT
trap 'exit 143' INT TERM HUP

# Keep the child home and its only working file inside the per-run context.
install -m 600 "$CODEX_HOME/auth.json" "$run_home/auth.json"
cat >"$run_input" <<EOF
You are a shadow drafting model. Produce a concise Chinese dispatch recommendation
for the issue snapshot below. This is a draft for a human Leader only.

Hard constraints:
- Do not execute commands, call tools, access network resources, or alter files.
- Do not send comments, mentions, status changes, PR actions, deployments, or requests.
- State assumptions and risks; propose no irreversible action.
- Return only the draft body, with no preamble.

Issue reference: $issue_ref

Snapshot:
EOF
cat "$snapshot" >>"$run_input"

# The Codex process sees only its runtime, provider auth, snapshot and output.
# Its network stays available for provider inference; Codex's read-only tool
# sandbox remains responsible for denying model-invoked network access.
bwrap_args=(
  --unshare-user --unshare-uts --unshare-ipc --die-with-parent
  --ro-bind /usr /usr
  --symlink usr/lib /lib
  --symlink usr/lib64 /lib64
  --proc /proc --dev /dev --tmpfs /tmp
  --dir /run --dir /etc --dir /etc/ssl
  --ro-bind /etc/ld.so.cache /etc/ld.so.cache
  --bind "$run_home" /run/codex-home
  --ro-bind "$run_context" /run/issue-context
  --bind "$draft" /run/shadow-draft.md
  --ro-bind /etc/hosts /etc/hosts
  --ro-bind /etc/ssl/certs /etc/ssl/certs
)
declare -a bwrap_dirs=(/run /etc /etc/ssl)
add_bwrap_dir() {
  local candidate=$1 existing
  for existing in "${bwrap_dirs[@]}"; do
    [[ $existing == "$candidate" ]] && return
  done
  bwrap_dirs+=("$candidate")
  bwrap_args+=(--dir "$candidate")
}
if [[ -f /etc/resolv.conf ]]; then
  resolv_conf=$(readlink -f /etc/resolv.conf)
  resolver_parent=${resolv_conf%/*}
  parent_chain=()
  while [[ -n $resolver_parent && $resolver_parent != / && $resolver_parent != /etc ]]; do
    parent_chain=("$resolver_parent" "${parent_chain[@]}")
    resolver_parent=${resolver_parent%/*}
  done
  for dir in "${parent_chain[@]}"; do add_bwrap_dir "$dir"; done
  if [[ $resolv_conf != /etc/resolv.conf ]]; then
    bwrap_args+=(--ro-bind "$resolv_conf" "$resolv_conf")
  fi
  bwrap_args+=(--ro-bind "$resolv_conf" /etc/resolv.conf)
fi
if [[ $codex_bin != /usr/* ]]; then
  codex_parent=${codex_bin%/*}
  parent_chain=()
  while [[ -n $codex_parent && $codex_parent != / && $codex_parent != /usr ]]; do
    parent_chain=("$codex_parent" "${parent_chain[@]}")
    codex_parent=${codex_parent%/*}
  done
  for dir in "${parent_chain[@]}"; do add_bwrap_dir "$dir"; done
  bwrap_args+=(--ro-bind "$codex_bin" "$codex_bin")
fi
if [[ -n $node_bin && $node_bin != /usr/* ]]; then
  node_parent=${node_bin%/*}
  parent_chain=()
  while [[ -n $node_parent && $node_parent != / && $node_parent != /usr ]]; do
    parent_chain=("$node_parent" "${parent_chain[@]}")
    node_parent=${node_parent%/*}
  done
  for dir in "${parent_chain[@]}"; do add_bwrap_dir "$dir"; done
  bwrap_args+=(--ro-bind "$node_bin" "$node_bin")
fi
if [[ -n $codex_package_root && $codex_package_root != /usr/* ]]; then
  package_parent=${codex_package_root%/*}
  parent_chain=()
  while [[ -n $package_parent && $package_parent != / && $package_parent != /usr ]]; do
    parent_chain=("$package_parent" "${parent_chain[@]}")
    package_parent=${package_parent%/*}
  done
  for dir in "${parent_chain[@]}"; do add_bwrap_dir "$dir"; done
  bwrap_args+=(--ro-bind "$codex_package_root" "$codex_package_root")
fi

runtime_path=/usr/bin:/bin
if [[ -n $node_bin ]]; then runtime_path="$(dirname "$node_bin"):$runtime_path"; fi

set +e
setsid env -i \
  PATH="$runtime_path" \
  HOME=/run/codex-home \
  CODEX_HOME=/run/codex-home \
  LANG="${LANG:-C.UTF-8}" \
  "$bwrap_bin" "${bwrap_args[@]}" --chdir /run/issue-context -- \
    "$codex_bin" exec --ephemeral --ignore-user-config --ignore-rules --sandbox read-only \
      --skip-git-repo-check --model deepseek-v4-flash --cd /run/issue-context \
      --output-last-message /run/shadow-draft.md --json \
      <"$run_input" >"$events" 2>"$stderr_log" &
codex_pid=$!
(
  sleep "$timeout_seconds" &
  timer_pid=$!
  trap 'kill "$timer_pid" 2>/dev/null || true; wait "$timer_pid" 2>/dev/null || true; exit 0' INT TERM HUP
  wait "$timer_pid"
  if kill -0 "$codex_pid" 2>/dev/null; then
    : >"$timed_out_marker"
    kill -TERM -- "-$codex_pid" 2>/dev/null || true
    sleep 2 &
    timer_pid=$!
    wait "$timer_pid"
    kill -KILL -- "-$codex_pid" 2>/dev/null || true
  fi
) &
watchdog_pid=$!
wait "$codex_pid"
status=$?
if [[ -f $timed_out_marker ]]; then
  wait "$watchdog_pid" 2>/dev/null || true
else
  kill "$watchdog_pid" 2>/dev/null || true
  wait "$watchdog_pid" 2>/dev/null || true
fi
watchdog_pid=""
codex_pid=""
set -e
if [[ -f $timed_out_marker ]]; then
  status=124
fi

snapshot_sha=$(sha256sum "$snapshot" | awk '{print $1}')
if [[ $status -eq 0 && -s $draft && ! -f $timed_out_marker ]]; then
  draft_sha=$(sha256sum "$draft" | awk '{print $1}')
  printf '{"timestamp":"%s","issue_ref":"%s","model":"deepseek-v4-flash","outcome":"draft_created","snapshot_sha256":"%s","draft":"%s","draft_sha256":"%s","events":"%s","stderr":"%s"}\n' \
    "$timestamp" "$issue_ref" "$snapshot_sha" "$(basename "$run_audit")/$(basename "$draft")" "$draft_sha" "$(basename "$run_audit")/$(basename "$events")" "$(basename "$run_audit")/$(basename "$stderr_log")" >>"$ledger"
  printf 'draft=%s\nledger=%s\n' "$draft" "$ledger"
  exit 0
fi

rm -f "$draft"
# A successful process without a draft or a deadline expiry is a channel error.
if [[ $status -eq 0 ]]; then
  status=1
fi
printf '{"timestamp":"%s","issue_ref":"%s","model":"deepseek-v4-flash","outcome":"channel_error","exit_code":%s,"snapshot_sha256":"%s","events":"%s","stderr":"%s"}\n' \
  "$timestamp" "$issue_ref" "$status" "$snapshot_sha" "$(basename "$run_audit")/$(basename "$events")" "$(basename "$run_audit")/$(basename "$stderr_log")" >>"$ledger"
printf 'shadow draft failed; recorded channel_error in %s\n' "$ledger" >&2
exit "$status"
