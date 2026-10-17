#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin" "$tmp/codex-home" "$tmp/audit" "$tmp/outside"
printf 'provider-auth' >"$tmp/codex-home/auth.json"
cat >"$tmp/codex-home/config.toml" <<'EOF'
model_provider = "fixture"

[model_providers.fixture]
base_url = "https://provider.invalid/v1"
wire_api = "responses"
env_http_headers = { Authorization = "SHADOW_DRAFT_FIXTURE_HEADER" }
request_max_retries = 99

[model_providers.untrusted]
base_url = "https://untrusted.invalid/v1"
wire_api = "chat"

[mcp_servers.fixture]
command = "must-not-be-copied"
EOF
printf 'must remain outside the Codex view\n' >"$tmp/outside/private.txt"
printf '{"title":"real issue snapshot","mode":"success"}\n' >"$tmp/snapshot.json"

cat >"$tmp/bin/codex" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ ${1:-} == exec && ${2:-} == --help ]]; then
  printf '%s\n' '--ephemeral --ignore-user-config --ignore-rules --sandbox --skip-git-repo-check --cd'
  exit 0
fi
if [[ ${1:-} == --direct-control ]]; then
  cat '__OUTSIDE_SECRET__' >/dev/null && exit 72
  exit 73
fi
all_args=("$@")
if env | grep -q '^MULTICA_'; then
  echo 'platform environment leaked' >&2
  exit 70
fi
test "$(stat -c %a "$CODEX_HOME/auth.json")" = 600
test "$(find "$CODEX_HOME" -type f | wc -l)" -eq 2
test "$(stat -c %a "$CODEX_HOME/config.toml")" = 600
grep -Fq 'base_url = "https://provider.invalid/v1"' "$CODEX_HOME/config.toml"
grep -Fq 'wire_api = "responses"' "$CODEX_HOME/config.toml"
grep -Fq '"Authorization" = "fixture-header-secret"' "$CODEX_HOME/config.toml"
if grep -Eq 'untrusted|mcp_servers|must-not-be-copied|request_max_retries|SHADOW_DRAFT_FIXTURE_HEADER' "$CODEX_HOME/config.toml"; then
  echo 'unapproved Codex configuration reached the child' >&2
  exit 74
fi
test "$PWD" = /run/issue-context
test -f "$PWD/issue-snapshot"
test "$(find "$PWD" -mindepth 1 -maxdepth 1 -type f | wc -l)" -eq 1
if cat '__OUTSIDE_SECRET__' >/dev/null 2>&1; then
  echo 'bubblewrap exposed a file outside the snapshot view' >&2
  exit 71
fi
printf 'outside-file=denied\n'
printf 'codex-home=%s\n' "$CODEX_HOME"
printf 'args=%s\n' "$*"
mode=$(sed -n 's/.*"mode":"\([^"]*\)".*/\1/p' | tail -n 1)
case "$mode" in
  empty) exit 0 ;;
  error) exit 42 ;;
  timeout-status) exit 124 ;;
  hang)
    sleep 60 &
    printf 'descendant=%s\n' "$!"
    wait
    ;;
esac
set -- "${all_args[@]}"
while [[ $# -gt 0 ]]; do
  if [[ $1 == --output-last-message ]]; then
    printf 'human-reviewed draft %s\n' "$CODEX_HOME" >"$2"
    exit 0
  fi
  shift
done
exit 64
EOF
sed -i "s|__OUTSIDE_SECRET__|$tmp/outside/private.txt|" "$tmp/bin/codex"
chmod +x "$tmp/bin/codex"
if "$tmp/bin/codex" --direct-control; then
  echo 'direct-control returned an unexpected success status' >&2
  exit 1
else
  test "$?" -eq 72
fi

# Freeze the timestamp to reproduce collisions deterministically.
cat >"$tmp/bin/date" <<'EOF'
#!/usr/bin/env bash
printf '20261017T043200Z\n'
EOF
chmod +x "$tmp/bin/date"

run() {
  PATH="$tmp/bin:$PATH" CODEX_HOME="$tmp/codex-home" SHADOW_DRAFT_TIMEOUT_SECONDS=2 SHADOW_DRAFT_FIXTURE_HEADER=fixture-header-secret MULTICA_TOKEN=must-not-leak \
    MULTICA_SERVER_URL=http://must-not-leak MULTICA_TEST_SENTINEL=must-not-leak \
    "$root/scripts/shadow-draft.sh" --issue-ref RUYI-746 --snapshot "$tmp/snapshot.json" --audit-dir "$tmp/audit"
}

run
run

grep -q 'outside-file=denied' "$tmp/audit"/*/events.jsonl
grep -q -- '--sandbox read-only' "$tmp/audit"/*/events.jsonl
grep -q -- '--ephemeral' "$tmp/audit"/*/events.jsonl
if grep -q -- '--ignore-user-config' "$tmp/audit"/*/events.jsonl; then
  echo 'runner unexpectedly ignored its generated provider config' >&2
  exit 1
fi
grep -q -- '--ignore-rules' "$tmp/audit"/*/events.jsonl
grep -q -- '--model deepseek-v4-flash' "$tmp/audit"/*/events.jsonl
grep -q '/run/codex-home' "$tmp/audit"/*/events.jsonl
test -r "$tmp/outside/private.txt"
if PATH="$tmp/bin:$PATH" CODEX_HOME="$tmp/codex-home" SHADOW_DRAFT_TIMEOUT_SECONDS=601 \
  "$root/scripts/shadow-draft.sh" --issue-ref RUYI-746 --snapshot "$tmp/snapshot.json" --audit-dir "$tmp/audit"; then
  echo 'accepted an unbounded timeout' >&2
  exit 1
else
  test "$?" -eq 2
fi

mkdir -p "$tmp/missing-config-home"
printf 'provider-auth' >"$tmp/missing-config-home/auth.json"
if PATH="$tmp/bin:$PATH" CODEX_HOME="$tmp/missing-config-home" \
  "$root/scripts/shadow-draft.sh" --issue-ref RUYI-746 --snapshot "$tmp/snapshot.json" --audit-dir "$tmp/audit" 2>"$tmp/missing-config.log"; then
  echo 'accepted missing provider configuration' >&2
  exit 1
else
  test "$?" -eq 2
fi
grep -q 'CODEX_HOME/config.toml with a supported provider is required' "$tmp/missing-config.log"

mkdir -p "$tmp/platform-header-home"
printf 'provider-auth' >"$tmp/platform-header-home/auth.json"
cat >"$tmp/platform-header-home/config.toml" <<'EOF'
model_provider = "fixture"

[model_providers.fixture]
base_url = "https://provider.invalid/v1"
wire_api = "responses"
env_http_headers = { Authorization = "MULTICA_TOKEN" }
EOF
if PATH="$tmp/bin:$PATH" CODEX_HOME="$tmp/platform-header-home" MULTICA_TOKEN=platform-secret \
  "$root/scripts/shadow-draft.sh" --issue-ref RUYI-746 --snapshot "$tmp/snapshot.json" --audit-dir "$tmp/audit" 2>"$tmp/platform-header.log"; then
  echo 'accepted a platform environment variable as a provider credential' >&2
  exit 1
else
  test "$?" -eq 2
fi
grep -q 'platform environment variables cannot supply provider headers' "$tmp/platform-header.log"

for mode in empty error timeout-status hang; do
  printf '{"title":"real issue snapshot","mode":"%s"}\n' "$mode" >"$tmp/snapshot.json"
  if run; then
    echo "unexpected success for $mode" >&2
    exit 1
  else
    status=$?
  fi
  case "$mode" in
    empty) test "$status" -eq 1 ;;
    error) test "$status" -eq 42 ;;
    timeout-status) test "$status" -eq 124 ;;
    hang) test "$status" -eq 124 ;;
  esac
done
descendant_pid=$(sed -n 's/^descendant=//p' "$tmp/audit"/*/events.jsonl | tail -n 1)
test -n "$descendant_pid"
if kill -0 "$descendant_pid" 2>/dev/null; then
  state=$(awk '{print $3}' "/proc/$descendant_pid/stat" 2>/dev/null || true)
  test "$state" = Z
fi

python3 - "$tmp" <<'PY'
import hashlib
import json
import pathlib
import sys

fixture = pathlib.Path(sys.argv[1])
audit = fixture / 'audit'
rows = [json.loads(line) for line in (audit / 'ledger.jsonl').read_text().splitlines()]
snapshot_hashes = {
    mode: hashlib.sha256(
        ('{"title":"real issue snapshot","mode":"' + mode + '"}\n').encode()
    ).hexdigest()
    for mode in ('success', 'empty', 'error', 'timeout-status', 'hang')
}
expected_modes = ['success', 'success', 'empty', 'error', 'timeout-status', 'hang']
assert len(rows) == 6
assert len({row['timestamp'] for row in rows}) == 1
assert [row['outcome'] for row in rows] == ['draft_created'] * 2 + ['channel_error'] * 4
assert [row['exit_code'] for row in rows[2:]] == [1, 42, 124, 124]
paths = []
for row, mode in zip(rows, expected_modes):
    assert row['snapshot_sha256'] == snapshot_hashes[mode]
    for key in ('events', 'stderr'):
        paths.append(row[key])
        assert (audit / row[key]).is_file()
    if row['outcome'] == 'draft_created':
        paths.append(row['draft'])
        assert row['draft_sha256'] == hashlib.sha256((audit / row['draft']).read_bytes()).hexdigest()
    else:
        assert not (audit / row['events']).with_name('shadow-draft.md').exists()
assert len(paths) == len(set(paths)), 'invocations share audit artifacts'
assert len(list(audit.rglob('shadow-draft.md'))) == 2
PY
echo 'shadow-draft test: PASS'
