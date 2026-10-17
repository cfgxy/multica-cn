#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin" "$tmp/codex-home" "$tmp/audit"
printf 'provider-auth' >"$tmp/codex-home/auth.json"
printf '{"title":"real issue snapshot"}\n' >"$tmp/snapshot.json"

cat >"$tmp/bin/codex" <<'EOF'
#!/usr/bin/env bash
printf 'token=%s\n' "${MULTICA_TOKEN:-absent}" >"$FAKE_CAPTURE"
printf 'server=%s\n' "${MULTICA_SERVER_URL:-absent}" >>"$FAKE_CAPTURE"
printf '%s\n' "$*" >>"$FAKE_CAPTURE"
while [[ $# -gt 0 ]]; do
  if [[ $1 == --output-last-message ]]; then
    printf 'human-reviewed draft\n' >"$2"
    exit 0
  fi
  shift
done
exit 64
EOF
chmod +x "$tmp/bin/codex"

PATH="$tmp/bin:$PATH" CODEX_HOME="$tmp/codex-home" MULTICA_TOKEN=must-not-leak \
  MULTICA_SERVER_URL=http://must-not-leak FAKE_CAPTURE="$tmp/capture" \
  "$root/scripts/shadow-draft.sh" --issue-ref RUYI-746 --snapshot "$tmp/snapshot.json" --audit-dir "$tmp/audit"

grep -qx 'token=absent' "$tmp/capture"
grep -qx 'server=absent' "$tmp/capture"
grep -q -- '--sandbox read-only' "$tmp/capture"
grep -q -- '--ephemeral' "$tmp/capture"
grep -q -- '--ignore-user-config' "$tmp/capture"
grep -q '"outcome":"draft_created"' "$tmp/audit/ledger.jsonl"
test "$(find "$tmp/audit" -name '*shadow-draft.md' | wc -l)" -eq 1
echo 'shadow-draft test: PASS'
