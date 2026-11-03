#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
script="$repo_root/scripts/daemon-update-restart.sh"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_contains() {
  local file="$1" text="$2"
  grep -F -- "$text" "$file" >/dev/null || fail "$file does not contain: $text"
}

make_fakes() {
  local dir="$1"
  mkdir -p "$dir"
  printf '111\n' >"$dir/pid"
  : >"$dir/systemctl.log"
  : >"$dir/multica.log"
  : >"$dir/kill.log"

  cat >"$dir/systemctl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
echo "$*" >>"$FAKE_DIR/systemctl.log"
if [[ "$1" == "is-active" ]]; then
  [[ "${FAKE_SYSTEMD_ACTIVE:-1}" == "1" ]]
  exit
fi
if [[ "$1" == "--no-ask-password" ]]; then
  exit "${FAKE_POLKIT_RESTART_EXIT:-1}"
fi
if [[ "$1" == "show" ]]; then
  prop=""
  while (($#)); do
    if [[ "$1" == "-p" ]]; then prop="$2"; shift 2; continue; fi
    shift
  done
  case "$prop" in
    MainPID) cat "$FAKE_DIR/pid" ;;
    ActiveState) echo active ;;
    Restart) echo "${FAKE_RESTART_POLICY:-on-failure}" ;;
    *) exit 1 ;;
  esac
fi
EOF

  cat >"$dir/multica" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
echo "$*" >>"$FAKE_DIR/multica.log"
if [[ "$*" == *"daemon status"* ]]; then
  pid=$(cat "$FAKE_DIR/pid")
  busy="${FAKE_BUSY_TASKS:-0}"
  status=running
  if [[ "${FAKE_DELAY_AFTER_RESTART:-0}" == "1" && "$pid" == "222" && ! -f "$FAKE_DIR/delay-observed" ]]; then
    status=starting
    touch "$FAKE_DIR/delay-observed"
  fi
  printf '{"status":"%s","pid":%s,"active_task_count":%s,"running_task_count":0,"resource_wait_task_count":0}\n' "$status" "$pid" "$busy"
fi
EOF

  cat >"$dir/kill" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
echo "$*" >>"$FAKE_DIR/kill.log"
printf '222\n' >"$FAKE_DIR/pid"
EOF

  cat >"$dir/sleep" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
  chmod +x "$dir/systemctl" "$dir/multica" "$dir/kill" "$dir/sleep"
}

run_helper() {
  local dir="$1" profile="$2"
  FAKE_DIR="$dir" \
  DAEMON_SYSTEMCTL_CMD="$dir/systemctl" \
  DAEMON_KILL_CMD="$dir/kill" \
  DAEMON_SLEEP_CMD="$dir/sleep" \
    "$script" "$profile" "$dir/multica"
}

default_dir="$tmp/default"
make_fakes "$default_dir"
run_helper "$default_dir" default >"$default_dir/output"
assert_contains "$default_dir/systemctl.log" "is-active --quiet multica-daemon@default.service"
assert_contains "$default_dir/kill.log" "-KILL 111"
assert_contains "$default_dir/output" "default restarted by systemd: 111 -> 222"
if grep -F -- '--profile' "$default_dir/multica.log" >/dev/null; then
  fail "default profile must not pass --profile"
fi

delayed_dir="$tmp/delayed"
make_fakes "$delayed_dir"
FAKE_DELAY_AFTER_RESTART=1 run_helper "$delayed_dir" default >"$delayed_dir/output"
assert_contains "$delayed_dir/output" "default restarted by systemd: 111 -> 222"
[[ -f "$delayed_dir/delay-observed" ]] || fail "delayed daemon readiness was not exercised"

named_dir="$tmp/named"
make_fakes "$named_dir"
run_helper "$named_dir" idata >"$named_dir/output"
assert_contains "$named_dir/systemctl.log" "is-active --quiet multica-daemon@idata.service"
assert_contains "$named_dir/multica.log" "daemon status --profile idata --output json"

manual_dir="$tmp/manual"
make_fakes "$manual_dir"
FAKE_SYSTEMD_ACTIVE=0 run_helper "$manual_dir" idata >"$manual_dir/output"
assert_contains "$manual_dir/multica.log" "daemon restart --profile idata"

busy_dir="$tmp/busy"
make_fakes "$busy_dir"
if FAKE_BUSY_TASKS=1 run_helper "$busy_dir" default >"$busy_dir/output" 2>"$busy_dir/error"; then
  fail "busy daemon restart should fail"
fi
assert_contains "$busy_dir/error" "still has 1 active/running/waiting task"
if [[ -s "$busy_dir/kill.log" ]]; then
  fail "busy daemon must not be killed"
fi

update_block="$(awk '/^daemon-update:/{on=1; next} on && /^[A-Za-z0-9_.-]+:/{exit} on' "$repo_root/Makefile")"
systemd_block="$(awk '/^daemon-update-systemd:/{on=1; next} on && /^[A-Za-z0-9_.-]+:/{exit} on' "$repo_root/Makefile")"
grep -F 'scripts/daemon-update-restart.sh "$(PROFILE)"' <<<"$update_block" >/dev/null || fail "daemon-update does not pass the resolved PROFILE to fallback"
grep -F 'multica-daemon@$(PROFILE).service' <<<"$systemd_block" >/dev/null || fail "systemd update does not target PROFILE"
if grep -F 'list-unit-files' <<<"$systemd_block" >/dev/null || grep -F 'ALL' <<<"$systemd_block" >/dev/null; then
  fail "daemon-update-systemd still fans out to every enabled profile"
fi

echo "PASS: daemon-update restart fallback"

