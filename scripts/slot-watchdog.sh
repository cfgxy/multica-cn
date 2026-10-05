#!/usr/bin/env bash
# Per-slot memory watchdog (RUYI-333 invariant 4).
#
# Started by `dev-env.sh <slot> up` alongside the slot components. For each
# component with a live launcher (<comp>.pid, pgid==pid by launch_detached's
# `set -m`) the sampled process set is recomputed EVERY tick:
#
#   - the launcher's whole process tree (ppid walk). A descendant that left
#     the launcher's process group is still a tree member, so this covers
#     every launcher-side shape: turbo task groups, next-server re-groupings;
#   - for api/web/desktop, the process actually listening on the slot port
#     plus its own tree — the net for a server that re-parented away from
#     the launcher (ppid 1) and can no longer be proven by ancestry. It is
#     only accounted (and only ever signalled) when it is identifiably ours:
#     it is the pid recorded at `up` time in <comp>.listener.pid, or it
#     descends from the live launcher.
#
# RUYI-333 QA round 2 (2026-10-03) caught the earlier design — a one-shot
# listener snapshot taken when `up` returned — going blind exactly there:
# next-server changed pid/process group after startup, the recorded group
# went stale, and the watchdog saw only the small launcher group while the
# real server grew unbounded (and in co-grouped runs saw everything and
# killed). Sampling is therefore derived live, never replayed from files;
# the recorded listener pid is a fallback identity proof, not the target.
#
# Whenever the sampled set changes shape the watchdog appends an audit line
# to resource-events.log (action "shape": launcher, listener, and the pgids
# inside the sample), so the set is verifiable against /proc by anyone
# holding the slot directory; with MULTICA_SLOT_WATCHDOG_HEARTBEAT set it
# logs a "sample" line every tick. When the combined RSS of the sampled set
# exceeds the component's quota from slots.json resource_budget for
# BREACHES consecutive ticks, the watchdog kills the launcher group, the
# listener's current group and every sampled pid, and records the kill in
# resource-events.log and manifest.env (RESOURCE_KILL_<COMP>=<iso>).
#
# This is a backstop for run-away growth inside a slot; the primary budget
# enforcement is the per-component resource env (GOMEMLIMIT /
# --max-old-space-size / GOMAXPROCS) and the taskset CPU pinning applied at
# launch, plus docker compose caps for the shared PostgreSQL container.
#
# Tunables (env): MULTICA_SLOT_WATCHDOG_INTERVAL (seconds, default 15),
# MULTICA_SLOT_WATCHDOG_BREACHES (consecutive breaches before kill, default 2),
# MULTICA_SLOT_WATCHDOG_HEARTBEAT (log a sample line every tick when set).
# Lifecycle: exits when $SLOT_DIR/watchdog.stop appears, or the slot directory
# or its manifest is gone (destroy); `down`/`destroy` stop it explicitly.
set -uo pipefail

SLOT="${1:?usage: slot-watchdog.sh <slot> <slot-dir> <slots-file>}"
SLOT_DIR="${2:?usage: slot-watchdog.sh <slot> <slot-dir> <slots-file>}"
SLOTS_FILE="${3:?usage: slot-watchdog.sh <slot> <slot-dir> <slots-file>}"

MANIFEST="$SLOT_DIR/manifest.env"
STOP_FILE="$SLOT_DIR/watchdog.stop"
EVENT_LOG="$SLOT_DIR/resource-events.log"
INTERVAL="${MULTICA_SLOT_WATCHDOG_INTERVAL:-15}"
BREACHES="${MULTICA_SLOT_WATCHDOG_BREACHES:-2}"
KILL_GRACE=2

manifest_field() { sed -n "s/^$1=//p" "$MANIFEST" 2>/dev/null | head -1; }

budget_mb() { # $1 = component
  node -e '
    const fs = require("fs");
    const facts = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
    const c = facts.resource_budget && facts.resource_budget.components
      && facts.resource_budget.components[process.argv[2]];
    process.stdout.write(String(c && c.memory_mb ? c.memory_mb : 0));
  ' "$SLOTS_FILE" "$1" 2>/dev/null || printf '0'
}

slot_field() { # $1 = slot name, $2 = key from the slot's fact entry
  node -e '
    const fs = require("fs");
    const facts = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
    const s = (facts.slots || []).find(x => x.name === process.argv[2]);
    if (!s || s[process.argv[3]] === undefined) process.exit(1);
    process.stdout.write(String(s[process.argv[3]]));
  ' "$SLOTS_FILE" "$1" "$2" 2>/dev/null || printf ''
}

# Kernel fields from /proc/<pid>/stat: strip "pid (comm) " first — comm may
# contain spaces and parens, so strip greedily to the last ") ".
stat_field() { # $1 = pid, $2 = field index after the comm (1=state, 2=ppid, 3=pgrp)
  local stat rest
  stat="$(cat "/proc/$1/stat" 2>/dev/null || true)"
  [ -n "$stat" ] || return 0
  rest="${stat##*) }"
  printf '%s' "$(printf '%s\n' "$rest" | awk -v i="$2" '{print $i}')"
}

pgroup_of() { stat_field "$1" 3; }
ppid_of() { stat_field "$1" 2; }

# "ppid pid" for every live process, in one pass over /proc (no forks).
proc_pairs() {
  awk '{ sub(/^[0-9]+ \(.*\) /, ""); print $2, substr(FILENAME, 7, length(FILENAME) - 11) }' \
    /proc/[0-9]*/stat 2>/dev/null
}

tree_of() { # $1 = root pid; prints the root plus every live descendant (BFS)
  local root=$1 pairs next child p frontier
  pairs="$(proc_pairs)"
  printf '%s\n' "$root"
  frontier="$root"
  while [ -n "$frontier" ]; do
    next=""
    for p in $frontier; do
      while IFS= read -r child; do
        [ -n "$child" ] || continue
        printf '%s\n' "$child"
        next="$next $child"
      done < <(awk -v p="$p" '$1 == p { print $2 }' <<< "$pairs")
    done
    frontier="${next# }"
  done
}

port_listener_pid() { # same discovery as dev-env.sh: ss first, lsof fallback
  local pid
  pid="$(ss -lntp "sport = :$1" 2>/dev/null | sed -n 's/.*pid=\([0-9]\{1,\}\).*/\1/p' | head -1 || true)"
  [ -n "$pid" ] || pid="$(lsof -nP -iTCP:"$1" -sTCP:LISTEN -t 2>/dev/null | head -1 || true)"
  printf '%s' "$pid"
}

pid_has_ancestor() { # $1 = pid, $2 = candidate ancestor; bounded ppid walk
  local pid=$1 ancestor=$2 hops=0
  [ -n "$pid" ] && [ -n "$ancestor" ] || return 1
  while [ -n "$pid" ] && [ "$pid" != 0 ] && [ "$pid" != 1 ] && [ "$hops" -lt 32 ]; do
    [ "$pid" = "$ancestor" ] && return 0
    pid="$(ppid_of "$pid")"
    hops=$((hops + 1))
  done
  return 1
}

pids_rss_kb() { # $1 = comma-joined pids -> summed RSS in KB
  local list=$1
  [ -n "$list" ] || { printf 0; return; }
  ps -o rss= -p "$list" 2>/dev/null | awk '{ s += $1 } END { print s + 0 }'
}

kill_group() { # $1 = signal name, $2 = pgid
  kill "-$1" -- "-$2" 2>/dev/null || true
}

log_event() { # issue component rss_kb quota_mb action listener_pid tree_pgids
  printf '{"time":"%s","slot":"%s","issue":"%s","component":"%s","action":"%s","rss_mb":%d,"quota_mb":%s,"listener_pid":%s,"tree_pgids":"%s"}\n' \
    "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$SLOT" "$1" "$2" "$5" "$(( $3 / 1024 ))" "$4" "${6:-0}" "${7:-}" \
    >> "$EVENT_LOG"
}

component_port() { # $1 = component -> the slot port it serves ("" for none)
  case "$1" in
    api)     slot_field "$SLOT" backend_port ;;
    web)     slot_field "$SLOT" frontend_port ;;
    desktop) slot_field "$SLOT" desktop_renderer_port ;;
    mcp)     slot_field "$SLOT" mcp_port ;;
    *)       printf '' ;;
  esac
}

check_component() { # $1 = component, $2 = issue
  local comp=$1 issue=$2
  local pid_file="$SLOT_DIR/$comp.pid"
  local pid pgid quota_kb rss_kb breach_file count
  local port listener="" listener_pgid="" recorded tree pid_list pgid_list shape state_file p remain

  pid="$(cat "$pid_file" 2>/dev/null || true)"
  if [ -z "$pid" ] || ! kill -0 "$pid" 2>/dev/null; then
    rm -f "$SLOT_DIR/.wd-breach-$comp"
    return 0
  fi
  pgid="$(pgroup_of "$pid")"
  if [ -z "$pgid" ] || [ "$pgid" != "$pid" ]; then
    # launch_detached guarantees the launcher leads its group; a mismatch means
    # the pid was recycled or the file is stale — never signal an unproven
    # group, just note it and skip this tick.
    printf '%s watchdog: %s pid %s is not a group leader (pgid %s); skipping tick\n' \
      "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$comp" "$pid" "${pgid:-?}"
    return 0
  fi

  quota_kb=$(( $(budget_mb "$comp") * 1024 ))
  [ "$quota_kb" -gt 0 ] 2>/dev/null || return 0

  # Sampled set, derived live: launcher tree + (when provably ours) the port
  # listener and its tree.
  tree="$(tree_of "$pid")"
  port="$(component_port "$comp")"
  if [ -n "$port" ]; then
    listener="$(port_listener_pid "$port")"
    if [ "$listener" = "$pid" ]; then
      listener="" # the launcher itself serves the port; already in the tree
    elif [ -n "$listener" ]; then
      recorded="$(cat "$SLOT_DIR/$comp.listener.pid" 2>/dev/null || true)"
      if [ "$listener" != "$recorded" ] && ! pid_has_ancestor "$listener" "$pid"; then
        listener="" # serving process is not ours; never sample nor signal it
      fi
    fi
  fi
  if [ -n "$listener" ]; then
    tree="$( { printf '%s\n' "$tree"; tree_of "$listener"; } | sort -u)"
    listener_pgid="$(pgroup_of "$listener")"
  fi

  pid_list="$(printf '%s\n' $tree | paste -sd, -)"
  # pgroup_of prints without a trailing newline (its scalar callers rely on
  # command substitution to strip it), so add the newline explicitly here.
  pgid_list="$(for p in $tree; do pgroup_of "$p"; echo; done | sort -u | paste -sd, -)"
  rss_kb="$(pids_rss_kb "$pid_list")"

  # Audit trail: append a line whenever the sampled shape changes (launcher
  # or listener identity, or the pgids inside the set), and every tick in
  # heartbeat mode — so enforcement is checkable against /proc after the fact.
  state_file="$SLOT_DIR/.wd-shape-$comp"
  shape="launcher=${pid}:${pgid}|listener=${listener:-none}:${listener_pgid:-none}|tree=${pgid_list}"
  if [ ! -f "$state_file" ] || [ "$(cat "$state_file" 2>/dev/null)" != "$shape" ]; then
    printf '%s\n' "$shape" > "$state_file"
    log_event "$issue" "$comp" "$rss_kb" "$(( quota_kb / 1024 ))" shape "${listener:-0}" "$pgid_list"
  elif [ -n "${MULTICA_SLOT_WATCHDOG_HEARTBEAT:-}" ]; then
    log_event "$issue" "$comp" "$rss_kb" "$(( quota_kb / 1024 ))" sample "${listener:-0}" "$pgid_list"
  fi

  breach_file="$SLOT_DIR/.wd-breach-$comp"
  if [ "$rss_kb" -le "$quota_kb" ]; then
    rm -f "$breach_file"
    return 0
  fi

  count="$(cat "$breach_file" 2>/dev/null || printf 0)"
  count=$(( count + 1 ))
  printf '%s\n' "$count" > "$breach_file"
  if [ "$count" -lt "$BREACHES" ]; then
    log_event "$issue" "$comp" "$rss_kb" "$(( quota_kb / 1024 ))" breach "${listener:-0}" "$pgid_list"
    return 0
  fi

  log_event "$issue" "$comp" "$rss_kb" "$(( quota_kb / 1024 ))" kill "${listener:-0}" "$pgid_list"
  # TERM the launcher group, the listener's current group, and every sampled
  # pid directly — tree members that re-grouped out of both groups (the QA
  # round-2 shape) still get the signal.
  kill_group TERM "$pgid"
  if [ -n "$listener_pgid" ] && [ "$listener_pgid" != "$pgid" ]; then
    kill_group TERM "$listener_pgid"
  fi
  for p in $tree; do
    if [ "$p" != "$pgid" ]; then
      kill -TERM "$p" 2>/dev/null || true
    fi
  done
  sleep "$KILL_GRACE"
  kill_group KILL "$pgid"
  if [ -n "$listener_pgid" ] && [ "$listener_pgid" != "$pgid" ]; then
    kill_group KILL "$listener_pgid"
  fi
  for p in $tree; do
    if [ "$p" != "$pgid" ]; then
      kill -KILL "$p" 2>/dev/null || true
    fi
  done
  if [ -n "$port" ]; then
    sleep 1
    remain="$(port_listener_pid "$port" || true)"
    if [ -n "$remain" ]; then
      log_event "$issue" "$comp" 0 0 port-still-held "$remain" ""
    fi
  fi
  printf '\nRESOURCE_KILL_%s=%s\n' \
    "$(printf '%s' "$comp" | tr '[:lower:]' '[:upper:]')" \
    "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "$MANIFEST"
  rm -f "$pid_file" "$SLOT_DIR/$comp.listener.pid" "$SLOT_DIR/$comp.listener.pgid" \
    "$breach_file" "$state_file"
}

issue="$(manifest_field ISSUE)"
while :; do
  [ -f "$STOP_FILE" ] && exit 0
  [ -d "$SLOT_DIR" ] || exit 0
  [ -f "$MANIFEST" ] || exit 0
  for comp in api web mcp daemon desktop; do
    check_component "$comp" "$issue"
  done
  sleep "$INTERVAL"
done
