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
if ! "$bwrap_bin" --unshare-all --die-with-parent --ro-bind / / --proc /proc --dev /dev -- /usr/bin/true; then
  echo 'bubblewrap namespace isolation is unavailable' >&2
  exit 2
fi

mkdir -p "$audit_dir"
timestamp=$(date -u +%Y%m%dT%H%M%SZ)
# Atomically reserve a directory so even concurrent calls cannot share artifacts.
run_audit=$(mktemp -d "$audit_dir/${timestamp}-${issue_ref}-XXXXXX")
draft="$run_audit/shadow-draft.md"
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

set +e
setsid env -i \
  PATH="$PATH" \
  HOME="$run_home" \
  CODEX_HOME="$run_home" \
  LANG="${LANG:-C.UTF-8}" \
  "$codex_bin" exec --ephemeral --ignore-user-config --ignore-rules --sandbox read-only \
    --skip-git-repo-check --model deepseek-v4-flash --cd "$run_context" \
    --output-last-message "$draft" --json <"$run_input" >"$events" 2>"$stderr_log" &
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
