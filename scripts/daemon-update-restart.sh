#!/usr/bin/env bash
set -euo pipefail

profile="${1:-default}"
binary="${2:-$HOME/.local/bin/multica}"
systemctl_cmd="${DAEMON_SYSTEMCTL_CMD:-systemctl}"
kill_cmd="${DAEMON_KILL_CMD:-kill}"
sleep_cmd="${DAEMON_SLEEP_CMD:-sleep}"

if [[ ! -x "$binary" ]]; then
  echo "ERROR: daemon binary is not executable: $binary" >&2
  exit 1
fi

profile_args=()
if [[ "$profile" != "default" ]]; then
  profile_args=(--profile "$profile")
fi
unit="multica-daemon@${profile}.service"

daemon_status() {
  "$binary" daemon status "${profile_args[@]}" --output json 2>/dev/null
}

status_json="$(daemon_status || true)"
if [[ -z "$status_json" ]]; then
  status_json='{}'
fi
status="$(jq -r '.status // "stopped"' <<<"$status_json")"
daemon_pid="$(jq -r '.pid // 0' <<<"$status_json")"
active_tasks="$(jq -r '(.active_task_count // 0) + (.running_task_count // 0) + (.resource_wait_task_count // 0)' <<<"$status_json")"

if [[ "$active_tasks" != "0" ]]; then
  echo "ERROR: daemon profile $profile still has $active_tasks active/running/waiting task(s); binary updated, restart skipped" >&2
  exit 1
fi

wait_for_systemd_restart() {
  local old_pid="$1"
  local new_pid state
  for _ in $(seq 1 30); do
    new_pid="$($systemctl_cmd show "$unit" -p MainPID --value 2>/dev/null || true)"
    state="$($systemctl_cmd show "$unit" -p ActiveState --value 2>/dev/null || true)"
    if [[ "$state" == "active" && "$new_pid" =~ ^[1-9][0-9]*$ && "$new_pid" != "$old_pid" ]]; then
      status_json="$(daemon_status || true)"
      if [[ -n "$status_json" && "$(jq -r '.status // empty' <<<"$status_json")" == "running" ]]; then
        echo "daemon profile $profile restarted by systemd: $old_pid -> $new_pid"
        return 0
      fi
    fi
    "$sleep_cmd" 1
  done
  echo "ERROR: timed out waiting for $unit to restart" >&2
  exit 1
}

if "$systemctl_cmd" is-active --quiet "$unit" 2>/dev/null; then
  main_pid="$($systemctl_cmd show "$unit" -p MainPID --value)"
  if [[ ! "$main_pid" =~ ^[1-9][0-9]*$ ]]; then
    echo "ERROR: $unit is active but has no valid MainPID" >&2
    exit 1
  fi
  if [[ "$status" != "running" || "$daemon_pid" != "$main_pid" ]]; then
    echo "ERROR: daemon status PID $daemon_pid does not match $unit MainPID $main_pid" >&2
    exit 1
  fi

  if [[ -e "/proc/$main_pid/exe" && "/proc/$main_pid/exe" -ef "$binary" ]]; then
    echo "daemon profile $profile already runs the updated binary (PID $main_pid)"
    exit 0
  fi

  if "$systemctl_cmd" --no-ask-password restart "$unit" >/dev/null 2>&1; then
    wait_for_systemd_restart "$main_pid"
    exit 0
  fi

  restart_policy="$($systemctl_cmd show "$unit" -p Restart --value)"
  case "$restart_policy" in
    always|on-failure|on-abnormal|on-abort|on-watchdog) ;;
    *)
      echo "ERROR: cannot restart $unit without privilege and Restart=$restart_policy will not recover from SIGKILL" >&2
      exit 1
      ;;
  esac

  echo "WARN: no passwordless sudo/polkit restart; terminating exact MainPID $main_pid so $unit can auto-restart"
  "$kill_cmd" -KILL "$main_pid"
  wait_for_systemd_restart "$main_pid"
  exit 0
fi

if [[ "$status" == "running" ]]; then
  echo "systemd unit $unit is not active; restarting profile $profile through the daemon CLI"
  "$binary" daemon restart "${profile_args[@]}"
else
  echo "systemd unit $unit is not active and the daemon is stopped; starting profile $profile through the daemon CLI"
  "$binary" daemon start "${profile_args[@]}"
fi

