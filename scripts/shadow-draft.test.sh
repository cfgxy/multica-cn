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
set -euo pipefail
if [[ ${1:-} == exec && ${2:-} == --help ]]; then
  printf '%s\n' '--ephemeral --ignore-user-config --ignore-rules --sandbox --skip-git-repo-check --cd'
  exit 0
fi
# The fixture path is part of the fake executable, not the cleared environment.
fixture=$(cd "$(dirname "$0")/.." && pwd)
capture="$fixture/capture"
all_args=("$@")
env >"$fixture/child-env"
printf 'token=%s\n' "${MULTICA_TOKEN:-absent}" >"$capture"
printf 'server=%s\n' "${MULTICA_SERVER_URL:-absent}" >>"$capture"
printf '%s\n' "$*" >>"$capture"
printf '%s\n' "$CODEX_HOME" >>"$fixture/homes"
test "$(stat -c %a "$CODEX_HOME/auth.json")" = 600
test "$(find "$CODEX_HOME" -type f | wc -l)" -eq 1
while [[ $# -gt 0 ]]; do
  if [[ $1 == --cd ]]; then
    cd "$2"
    break
  fi
  shift
done
printf '%s\n' "$PWD" >>"$fixture/workdirs"
test -f "$PWD/issue-snapshot"
test "$(find "$PWD" -mindepth 1 -maxdepth 1 -type f | wc -l)" -eq 1
printf 'snapshot-only\n' >"$fixture/context-check"
mode=$(cat "$fixture/mode")
case "$mode" in
  empty) exit 0 ;;
  error) exit 42 ;;
  timeout-status) exit 124 ;;
    hang)
      sleep 60 &
      echo "$!" >"$fixture/descendant.pid"
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
chmod +x "$tmp/bin/codex"

# Freeze the timestamp to reproduce collisions deterministically.
cat >"$tmp/bin/date" <<'EOF'
#!/usr/bin/env bash
printf '20261017T043200Z\n'
EOF
chmod +x "$tmp/bin/date"

run() {
  PATH="$tmp/bin:$PATH" CODEX_HOME="$tmp/codex-home" SHADOW_DRAFT_TIMEOUT_SECONDS=2 MULTICA_TOKEN=must-not-leak \
    MULTICA_SERVER_URL=http://must-not-leak MULTICA_TEST_SENTINEL=must-not-leak \
    "$root/scripts/shadow-draft.sh" --issue-ref RUYI-746 --snapshot "$tmp/snapshot.json" --audit-dir "$tmp/audit"
}

printf 'success' >"$tmp/mode"
run
run

grep -qx 'token=absent' "$tmp/capture"
grep -qx 'server=absent' "$tmp/capture"
grep -q -- '--sandbox read-only' "$tmp/capture"
grep -q -- '--ephemeral' "$tmp/capture"
grep -q -- '--ignore-user-config' "$tmp/capture"
grep -q -- '--ignore-rules' "$tmp/capture"
grep -q -- '--model deepseek-v4-flash' "$tmp/capture"
if grep -q '^MULTICA_' "$tmp/child-env"; then
  echo 'platform environment leaked' >&2
  exit 1
fi
test -s "$tmp/context-check"
if PATH="$tmp/bin:$PATH" CODEX_HOME="$tmp/codex-home" SHADOW_DRAFT_TIMEOUT_SECONDS=601 \
  "$root/scripts/shadow-draft.sh" --issue-ref RUYI-746 --snapshot "$tmp/snapshot.json" --audit-dir "$tmp/audit"; then
  echo 'accepted an unbounded timeout' >&2
  exit 1
else
  test "$?" -eq 2
fi

for mode in empty error timeout-status hang; do
  printf '%s' "$mode" >"$tmp/mode"
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
descendant_pid=$(cat "$tmp/descendant.pid")
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
assert len(rows) == 6
assert len({row['timestamp'] for row in rows}) == 1
assert [row['outcome'] for row in rows] == ['draft_created'] * 2 + ['channel_error'] * 4
assert [row['exit_code'] for row in rows[2:]] == [1, 42, 124, 124]
paths = []
for row in rows:
    assert row['snapshot_sha256'] == hashlib.sha256((fixture / 'snapshot.json').read_bytes()).hexdigest()
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
assert all(not pathlib.Path(home).exists() for home in (fixture / 'homes').read_text().splitlines())
PY
echo 'shadow-draft test: PASS'
