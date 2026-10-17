#!/usr/bin/env bash
# Generate one human-reviewed shadow draft without granting the model platform credentials.
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/shadow-draft.sh --issue-ref RUYI-123 --snapshot FILE --audit-dir DIR

The snapshot is a human-exported, read-only issue context. The model process
receives no MULTICA_* variables and runs in Codex's read-only sandbox. This
script writes a draft and an append-only local ledger; it never calls Multica.
EOF
}

issue_ref=""
snapshot=""
audit_dir=""

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
[[ -n ${CODEX_HOME:-} && -f $CODEX_HOME/auth.json ]] || {
  echo 'CODEX_HOME/auth.json is required for provider authentication' >&2
  exit 2
}

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
trap 'rm -rf "$run_home" "$run_input"' EXIT

# Copy only provider authentication. Do not copy config, MCP servers, skills, or task state.
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
env -i \
  PATH="$PATH" \
  HOME="$run_home" \
  CODEX_HOME="$run_home" \
  LANG="${LANG:-C.UTF-8}" \
  codex exec --ephemeral --ignore-user-config --sandbox read-only \
    --skip-git-repo-check --model deepseek-v4-flash --cd "$(dirname "$snapshot")" \
    --output-last-message "$draft" --json <"$run_input" >"$events" 2>"$stderr_log"
status=$?
set -e

snapshot_sha=$(sha256sum "$snapshot" | awk '{print $1}')
if [[ $status -eq 0 && -s $draft ]]; then
  draft_sha=$(sha256sum "$draft" | awk '{print $1}')
  printf '{"timestamp":"%s","issue_ref":"%s","model":"deepseek-v4-flash","outcome":"draft_created","snapshot_sha256":"%s","draft":"%s","draft_sha256":"%s","events":"%s","stderr":"%s"}\n' \
    "$timestamp" "$issue_ref" "$snapshot_sha" "$(basename "$run_audit")/$(basename "$draft")" "$draft_sha" "$(basename "$run_audit")/$(basename "$events")" "$(basename "$run_audit")/$(basename "$stderr_log")" >>"$ledger"
  printf 'draft=%s\nledger=%s\n' "$draft" "$ledger"
  exit 0
fi

rm -f "$draft"
# A successful process without a draft is still a failed channel invocation.
if [[ $status -eq 0 ]]; then
  status=1
fi
printf '{"timestamp":"%s","issue_ref":"%s","model":"deepseek-v4-flash","outcome":"channel_error","exit_code":%s,"snapshot_sha256":"%s","events":"%s","stderr":"%s"}\n' \
  "$timestamp" "$issue_ref" "$status" "$snapshot_sha" "$(basename "$run_audit")/$(basename "$events")" "$(basename "$run_audit")/$(basename "$stderr_log")" >>"$ledger"
printf 'shadow draft failed; recorded channel_error in %s\n' "$ledger" >&2
exit "$status"
