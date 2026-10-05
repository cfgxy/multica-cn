#!/usr/bin/env bash
# Static + stub acceptance for the slot resource budget (RUYI-333 invariant 4).
#
# Rewritten per Owner directive 2026-10-02 23:53 「禁止做性能压力测试」: no
# memory hogs, no full-load or parallel real-startup stress, no memory-growth
# verification — nothing here puts real load on the shared host. An earlier
# revision drove the watchdog with a real memory-growing process; that form
# is banned because a run killed mid-flight could leak the load (it did).
# What replaces it:
#
#   1. Static config assertions — the canonical slots.json fact source, the
#      per-component env caps derived from it, and the docker compose mirror
#      must all agree with the documented budget.
#   2. launch_detached stub assertions — a `sleep` stub proves CPU pinning,
#      slot tagging and group leadership.
#   3. Watchdog sampling correctness — the sampled set must track the real
#      web processes whatever shape they take. QA round 2 (2026-10-03) caught
#      the earlier one-shot listener snapshot going blind when next-server
#      re-grouped after startup, so these scenarios pin the fix down:
#        a. a descendant that left the launcher's process group (setsid) is
#           still sampled (tree walk) and killed;
#        b. an orphaned listener that no longer descends from the launcher is
#           still sampled via the port (recorded-pid proof) and killed;
#        c. a listener owned by nobody is never sampled nor signalled;
#        d. healthy components produce no breach, heartbeat/shape events keep
#           the sampled set auditable against /proc.
#   4. Watchdog kill-path logic — a stub `ps` fabricates the RSS reading for
#      chosen pids, so the real watchdog walks its real breach → TERM → KILL
#      → stamp → cleanup branch against `sleep` stubs with zero real memory;
#      inversions (real ps, no fabricated breach → no kill) pin causality to
#      the guardrail.
#
# Everything runs in a throwaway MULTICA_SLOTS_HOME and a throwaway git repo
# (scripts/dev-env.sh via symlink, canonical slots.json rendered on first
# use). Every process spawned here is a `sleep` stub: killed in-line, backed
# by an EXIT trap, and self-expiring within 60 seconds — worst case, a run
# killed hard leaks a process that carries no load and dies on its own.
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp_dir="$(mktemp -d)"
stub_pids=""
watchdog_pids=""
cleanup() {
  local p dir
  for p in $stub_pids; do
    kill -KILL -- "-$p" 2>/dev/null || kill -KILL "$p" 2>/dev/null || true
  done
  for dir in "$tmp_dir"/wd-*/; do
    [ -d "$dir" ] && : > "$dir/watchdog.stop" 2>/dev/null || true
  done
  for p in $watchdog_pids; do
    kill -KILL "$p" 2>/dev/null || true
  done
  rm -rf "$tmp_dir"
}
trap cleanup EXIT

mkdir -p "$tmp_dir/home"
export HOME="$tmp_dir/home"
unset MULTICA_SERVER_URL MULTICA_TOKEN MULTICA_WORKSPACE_ID MULTICA_DAEMON_PORT \
  MULTICA_AGENT_ID MULTICA_AGENT_NAME MULTICA_TASK_ID MULTICA_TASK_SLOT \
  MULTICA_CALLER_OWNER MULTICA_TASK_CONFIG_ROOT MULTICA_TASK_WORKSPACES_ROOT \
  MULTICA_WORKSPACES_ROOT 2>/dev/null || true
export MULTICA_SLOTS_HOME="$tmp_dir/slots"
export MULTICA_DEV_PROFILES_HOME="$tmp_dir/profiles"
export MULTICA_DEV_DESKTOP_APP_DATA="$tmp_dir/app-data"
export MULTICA_DEV_TMPDIR="$tmp_dir/dev-tmp"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

# Sandbox repo with the canonical fact source rendered by one list call.
repo="$tmp_dir/repo"
mkdir -p "$repo/scripts"
ln -s "$root_dir/scripts/dev-env.sh" "$repo/scripts/dev-env.sh"
git -C "$repo" init -q
git -C "$repo" config user.email test@localhost
git -C "$repo" config user.name test
git -C "$repo" commit --allow-empty -q -m init
dev_env() { bash "$repo/scripts/dev-env.sh" "$@"; }
dev_env list > /dev/null 2>&1 || fail "bootstrapping the sandbox fact source must succeed"

# ---------------------------------------------------------------------------
# Static: the checked-in fact source, the env caps derived from it and the
# docker compose mirror must agree with the documented budget — no process
# is started to know this. web's 8192MB is the RUYI-333 second-rework
# calibration: two independent idle-steady-state soaks peaked at 6356MB
# (QA round 2 kill) and 6434MB (up+idle probe), so the quota keeps ≥25%
# headroom over the higher peak, not just over the cold-compile peak.
# ---------------------------------------------------------------------------
diff -q "$repo/scripts/slots.json" "$root_dir/scripts/slots.json" > /dev/null \
  || fail "scripts/slots.json has drifted from the canonical template dev-env.sh renders"

node -e '
  const f = JSON.parse(require("fs").readFileSync(process.argv[1], "utf8"));
  const die = (m) => { console.error(m); process.exit(1); };
  const b = f.resource_budget;
  if (b.slot_cpus !== 4) die("slot_cpus must stay 4");
  const want = { api: 768, web: 8192, daemon: 256, desktop: 256, mcp: 256 };
  for (const [c, mb] of Object.entries(want))
    if (b.components[c]?.memory_mb !== mb) die(`components.${c}.memory_mb must stay ${mb}`);
  if (b.components.api.memory_mb + b.components.web.memory_mb
      + b.components.daemon.memory_mb + b.components.desktop.memory_mb
      + b.components.mcp.memory_mb !== 9728)
    die("per-slot component budget must stay 9728MB (768+8192+256+256+256)");
  if (b.shared_postgres.memory !== "2g" || b.shared_postgres.cpus !== 4)
    die("shared_postgres cap must stay 2g / 4 cpus");
  if (f.slots.length !== 2 || f.slots[0].name !== "dev1" || f.slots[1].name !== "dev2")
    die("exactly the fixed slots dev1 and dev2");
  f.slots.forEach((s, i) => {
    if (s.cpuset !== `${i * 4}-${i * 4 + 3}`) die(`${s.name} cpuset must pin slot_cpus cores (${i * 4}-${i * 4 + 3})`);
  });
  const ports = f.slots.flatMap(s => [s.backend_port, s.frontend_port, s.desktop_renderer_port, s.mcp_port]);
  if (new Set(ports).size !== ports.length) die("slot ports must be disjoint");
' "$repo/scripts/slots.json" || fail "slots.json budget accounting does not match the documented budget"

source "$repo/scripts/dev-env.sh"
# require_slot dies by exiting, so run the negative probe in a subshell.
if (require_slot dev3) > /dev/null 2>&1; then
  fail "a slot outside the fact source must be refused"
fi

# component_resource_env: the caps travel as env on the start commands, so
# their derived form must match the fact source exactly.
env_args="$(component_resource_env api)"
grep -q 'GOMEMLIMIT=768MiB' <<< "$env_args" || fail "api must get GOMEMLIMIT=768MiB, got: $env_args"
grep -q 'GOMAXPROCS=4' <<< "$env_args" || fail "api must get GOMAXPROCS=4 (slot_cpus), got: $env_args"
env_args="$(component_resource_env web)"
grep -q -- '--max-old-space-size=8192' <<< "$env_args" || fail "web must get --max-old-space-size=8192, got: $env_args"
env_args="$(component_resource_env daemon)"
grep -q 'GOMEMLIMIT=256MiB' <<< "$env_args" || fail "daemon must get GOMEMLIMIT=256MiB, got: $env_args"
[ -z "$(component_resource_env desktop)" ] || fail "desktop must get no env caps (enforced by pin + watchdog)"

# docker compose mirrors the fact source's shared_postgres cap.
mem_lines="$(grep -c '^    mem_limit:' "$root_dir/docker-compose.yml" || true)"
[ "$mem_lines" = 1 ] || fail "docker-compose.yml must declare exactly one mem_limit, got $mem_lines"
grep -xq "    mem_limit: $(budget_field shared_postgres.memory)" "$root_dir/docker-compose.yml" \
  || fail "compose mem_limit must mirror slots.json shared_postgres.memory ($(budget_field shared_postgres.memory))"
cpu_lines="$(grep -c '^    cpus:' "$root_dir/docker-compose.yml" || true)"
[ "$cpu_lines" = 1 ] || fail "docker-compose.yml must declare exactly one cpus, got $cpu_lines"
grep -xq "    cpus: $(budget_field shared_postgres.cpus)" "$root_dir/docker-compose.yml" \
  || fail "compose cpus must mirror slots.json shared_postgres.cpus ($(budget_field shared_postgres.cpus))"

# ---------------------------------------------------------------------------
# launch_detached, against a sleep stub: the component tree is CPU-pinned to
# the slot's cpuset and tagged MULTICA_SLOT/MULTICA_ISSUE so /proc attribution
# works without any bookkeeping file; the launcher leads its own process
# group (pgid == pid).
# ---------------------------------------------------------------------------
require_slot dev1
DIR="$repo"
bind_paths
CLEAN_ENV=(env -u MULTICA_SERVER_URL -u MULTICA_TOKEN)
launch_detached api sleep 60
api_pid="$(cat "$SLOT_DIR/api.pid")"
stub_pids="$api_pid"
kill -0 "$api_pid" 2>/dev/null || fail "launch_detached must leave a live launcher"
affinity="$(taskset -pc "$api_pid" 2>/dev/null | awk -F'[:：]' '{print $NF}' | tr -d ' ')"
[ "$affinity" = "$(slot_field dev1 cpuset)" ] || fail "api launcher affinity is '$affinity', want the slot cpuset $(slot_field dev1 cpuset)"
tr '\0' '\n' < "/proc/$api_pid/environ" | grep -qx "MULTICA_SLOT=dev1" || fail "the launcher must carry MULTICA_SLOT=dev1"
tr '\0' '\n' < "/proc/$api_pid/environ" | grep -qx "MULTICA_ISSUE=" || true # empty issue is fine before a manifest exists
pgid="$(ps -o pgid= -p "$api_pid" | tr -d ' ')"
[ "$pgid" = "$api_pid" ] || fail "the launcher must lead its own process group (pgid $pgid, pid $api_pid)"

require_slot dev2
DIR="$repo"
bind_paths
launch_detached web sleep 60
web_pid="$(cat "$SLOT_DIR/web.pid")"
stub_pids="$stub_pids $web_pid"
affinity2="$(taskset -pc "$web_pid" 2>/dev/null | awk -F'[:：]' '{print $NF}' | tr -d ' ')"
[ "$affinity2" = "$(slot_field dev2 cpuset)" ] || fail "dev2 web launcher affinity is '$affinity2', want $(slot_field dev2 cpuset)"

kill -KILL -- "-$api_pid" 2>/dev/null || true
kill -KILL -- "-$web_pid" 2>/dev/null || true
wait "$api_pid" 2>/dev/null || true
wait "$web_pid" 2>/dev/null || true
stub_pids=""

# ---------------------------------------------------------------------------
# Watchdog scenarios. The stub `ps` fabricates RSS rows for chosen pids (only
# when the watchdog actually queries them), so the real watchdog walks its
# real breach → TERM → KILL → stamp → cleanup branch against sleep stubs —
# real signals, zero real memory. The stub `ss` fabricates the port listener
# for the orphaned-listener scenarios.
# ---------------------------------------------------------------------------
stub_bin="$tmp_dir/stubbin"
mkdir -p "$stub_bin"
real_ps="$(command -v ps)"
real_ss="$(command -v ss)"
cat > "$stub_bin/ps" <<STUB
#!/usr/bin/env bash
if [ "\$1" = "-o" ] && [ "\$2" = "rss=" ] && [ "\$3" = "-p" ]; then
  "$real_ps" -o rss= -p "\$4"
  for p in \${FAKE_RSS_PIDS:-}; do
    case ",\$4," in *",\$p,"*) [ -n "\${FAKE_RSS_KB:-}" ] && printf '%s\n' "\$FAKE_RSS_KB" ;; esac
  done
  exit 0
fi
exec "$real_ps" "\$@"
STUB
chmod +x "$stub_bin/ps"
cat > "$stub_bin/ss" <<STUB
#!/usr/bin/env bash
if [ -n "\${FAKE_LISTEN_PID:-}" ] && [ -n "\${FAKE_LISTEN_PORT:-}" ]; then
  case "\${2:-}" in
    *"= :\$FAKE_LISTEN_PORT")
      printf 'LISTEN 0 511 *:%s *:* users:(("node",pid=%s,fd=28))\n' "\$FAKE_LISTEN_PORT" "\$FAKE_LISTEN_PID"
      exit 0 ;;
  esac
fi
exec "$real_ss" "\$@"
STUB
chmod +x "$stub_bin/ss"

spawn_stub() { # dir comp -> prints stub pid (its own group leader, no load, self-expiring)
  local dir=$1 comp=$2
  (
    set -m
    sleep 60 >/dev/null 2>&1 &
    printf '%s\n' "$!" > "$dir/$comp.pid"
    printf '%s\n' "$!"
  )
}

write_manifest() { # dir issue
  mkdir -p "$1"
  printf 'NAME=%s\nPHASE=dev\nISSUE=%s\n' "$(basename "$1")" "$2" > "$1/manifest.env"
}

# A definitely-dead pid for stale-bookkeeping probes: spawn, reap, reuse the number.
dead_pid() {
  local p
  p="$(sleep 0.05 & echo $!)"
  wait "$p" 2>/dev/null || true
  printf '%s' "$p"
}

wait_dead() { # pid [seconds]
  local i
  for _ in $(seq 1 $(( ${2:-20} * 2 ))); do
    kill -0 "$1" 2>/dev/null || return 0
    sleep 0.5
  done
  return 1
}

stop_watchdog_dir() { # dir wd_pid
  : > "$1/watchdog.stop" 2>/dev/null || true
  local i
  for _ in $(seq 1 10); do
    kill -0 "$2" 2>/dev/null || break
    sleep 0.5
  done
  kill -0 "$2" 2>/dev/null && fail "watchdog.stop must end the watchdog loop ($1)"
  wait "$2" 2>/dev/null || true
}

# ---------------------------------------------------------------------------
# Scenario K (kill path): fabricated RSS above the real 768MB api quota —
# the quota path is unmodified, only the reading is simulated. A healthy
# stub in ANOTHER slot's directory is untouched.
# ---------------------------------------------------------------------------
slot1="$MULTICA_SLOTS_HOME/dev1"
slot2="$MULTICA_SLOTS_HOME/dev2"
write_manifest "$slot1" RUYI-333-RES
write_manifest "$slot2" RUYI-333-RES

stub1="$(spawn_stub "$slot1" api)"
stub2="$(spawn_stub "$slot2" api)"
stub_pids="$stub1 $stub2"
kill -0 "$stub1" 2>/dev/null || fail "the stub must start"

PATH="$stub_bin:$PATH" FAKE_RSS_PIDS="$stub1" FAKE_RSS_KB=999999 \
  MULTICA_SLOT_WATCHDOG_INTERVAL=1 MULTICA_SLOT_WATCHDOG_BREACHES=2 \
  bash "$root_dir/scripts/slot-watchdog.sh" dev1 "$slot1" "$repo/scripts/slots.json" &
wd=$!
watchdog_pids="$wd"

wait_dead "$stub1" || fail "the watchdog must kill the breaching api group within ~20s"
kill -0 "$stub2" 2>/dev/null || fail "the watchdog of dev1 must never touch another slot's component"
# The stamp lands after the TERM→KILL grace; poll rather than race it.
stamped=0
for _ in $(seq 1 16); do
  grep -q '^RESOURCE_KILL_API=' "$slot1/manifest.env" && { stamped=1; break; }
  sleep 0.5
done
[ "$stamped" = 1 ] || fail "the kill must be stamped into the manifest"
grep -q '"action":"breach"' "$slot1/resource-events.log" || fail "resource-events.log must record the pre-kill breach"
grep -q '"action":"kill"' "$slot1/resource-events.log" || fail "resource-events.log must record the kill"
node -e '
  const fs = require("fs");
  const lines = fs.readFileSync(process.argv[1], "utf8").trim().split("\n").map(l => JSON.parse(l));
  const k = lines.find(l => l.action === "kill");
  if (!k || k.slot !== "dev1" || k.issue !== "RUYI-333-RES" || k.component !== "api") process.exit(1);
  if (!(k.rss_mb > k.quota_mb)) process.exit(1);
' "$slot1/resource-events.log" || fail "the kill event must attribute slot/issue/component and show rss above quota"
[ ! -f "$slot1/api.pid" ] || fail "the watchdog must clear the killed component's pid file"
[ -f "$slot2/api.pid" ] || fail "another slot's pid bookkeeping must be untouched"
stop_watchdog_dir "$slot1" "$wd"
watchdog_pids=""

# ---------------------------------------------------------------------------
# Scenario R (regrouped descendant — the QA round-2 blind shape): launcher →
# mid (co-group) → leaf (setsid, own process group, still a tree child). Only
# the leaf reads as breaching, so a watchdog that still samples by process
# group alone (or trusts the stale listener snapshot) never sees it. The
# stale sidecar proves the old bookkeeping is not what drives sampling.
# ---------------------------------------------------------------------------
w1="$tmp_dir/wd-regroup"
write_manifest "$w1" RUYI-333-RES
cat > "$tmp_dir/tree-launcher" <<EOF
#!/usr/bin/env bash
sleep 60 >/dev/null 2>&1 &
printf '%s\n' "\$!" >> "$tmp_dir/regroup.pids"
setsid sleep 60 >/dev/null 2>&1 &
printf '%s\n' "\$!" >> "$tmp_dir/regroup.pids"
wait
EOF
chmod +x "$tmp_dir/tree-launcher"
rm -f "$tmp_dir/regroup.pids"
(
  set -m
  "$tmp_dir/tree-launcher" >/dev/null 2>&1 &
  printf '%s\n' "$!" > "$w1/web.pid"
)
launcher_pid="$(cat "$w1/web.pid")"
for _ in $(seq 1 10); do
  [ -s "$tmp_dir/regroup.pids" ] && [ "$(wc -l < "$tmp_dir/regroup.pids")" -ge 2 ] && break
  sleep 0.2
done
mid_pid="$(sed -n 1p "$tmp_dir/regroup.pids")"
leaf_pid="$(sed -n 2p "$tmp_dir/regroup.pids")"
[ -n "$leaf_pid" ] || fail "the regrouped leaf must spawn"
stub_pids="$stub_pids $launcher_pid $mid_pid $leaf_pid"
leaf_pgid="$(ps -o pgid= -p "$leaf_pid" | tr -d ' ')"
launcher_pgid="$(ps -o pgid= -p "$launcher_pid" | tr -d ' ')"
[ -n "$leaf_pgid" ] && [ "$leaf_pgid" != "$launcher_pgid" ] || fail "the leaf must sit outside the launcher's process group"
printf '%s\n' "$(dead_pid)" > "$w1/web.listener.pid" # stale one-shot snapshot from an earlier run

PATH="$stub_bin:$PATH" FAKE_RSS_PIDS="$leaf_pid" FAKE_RSS_KB=9999999 \
  MULTICA_SLOT_WATCHDOG_INTERVAL=1 MULTICA_SLOT_WATCHDOG_BREACHES=2 \
  bash "$root_dir/scripts/slot-watchdog.sh" dev1 "$w1" "$repo/scripts/slots.json" &
wd=$!
watchdog_pids="$wd"

wait_dead "$leaf_pid" || fail "the watchdog must sample the re-grouped leaf (tree walk) and kill it"
wait_dead "$launcher_pid" 5 || fail "the kill must take down the launcher group too"
wait_dead "$mid_pid" 5 || fail "the kill must take down the whole tree"
stamped=0
for _ in $(seq 1 16); do
  grep -q '^RESOURCE_KILL_WEB=' "$w1/manifest.env" && { stamped=1; break; }
  sleep 0.5
done
[ "$stamped" = 1 ] || fail "the web kill must be stamped into the manifest"
node -e '
  const fs = require("fs");
  const lines = fs.readFileSync(process.argv[1], "utf8").trim().split("\n").map(l => JSON.parse(l));
  const k = lines.find(l => l.action === "kill" && l.component === "web");
  if (!k) process.exit(1);
  const leafPgids = process.argv[2].split(",").map(s => s.trim());
  if (!k.tree_pgids.split(",").some(g => leafPgids.includes(g))) process.exit(2);
' "$w1/resource-events.log" "$leaf_pgid" || fail "the kill event must show the leaf's (re-grouped) pgid inside the sampled set"
stop_watchdog_dir "$w1" "$wd"
watchdog_pids=""

# ---------------------------------------------------------------------------
# Scenario O (orphaned listener): the web.pid launcher is alive but the real
# server re-parented away (ppid 1) — ancestry cannot prove it, the port can
# (recorded listener pid). Heartbeat on, so the sample line itself is the
# audit evidence.
# ---------------------------------------------------------------------------
w2="$tmp_dir/wd-orphan"
write_manifest "$w2" RUYI-333-RES
stub3="$(spawn_stub "$w2" web)"
stub_pids="$stub_pids $stub3"
(
  set -m
  sleep 60 >/dev/null 2>&1 &
  printf '%s\n' "$!" > "$tmp_dir/orphan.pid"
)
orphan_pid="$(cat "$tmp_dir/orphan.pid")"
stub_pids="$stub_pids $orphan_pid"
printf '%s\n' "$orphan_pid" > "$w2/web.listener.pid"

PATH="$stub_bin:$PATH" FAKE_LISTEN_PORT="$(slot_field dev1 frontend_port)" FAKE_LISTEN_PID="$orphan_pid" \
  FAKE_RSS_PIDS="$orphan_pid" FAKE_RSS_KB=9999999 \
  MULTICA_SLOT_WATCHDOG_INTERVAL=1 MULTICA_SLOT_WATCHDOG_BREACHES=2 MULTICA_SLOT_WATCHDOG_HEARTBEAT=1 \
  bash "$root_dir/scripts/slot-watchdog.sh" dev1 "$w2" "$repo/scripts/slots.json" &
wd=$!
watchdog_pids="$wd"

wait_dead "$orphan_pid" || fail "the watchdog must sample the orphaned-but-recorded listener via the port and kill it"
grep -q '"action":"sample"' "$w2/resource-events.log" || fail "heartbeat mode must leave sample lines as audit evidence"
node -e '
  const fs = require("fs");
  const lines = fs.readFileSync(process.argv[1], "utf8").trim().split("\n").map(l => JSON.parse(l));
  const s = lines.find(l => l.action === "sample" && l.component === "web");
  if (!s || String(s.listener_pid) !== process.argv[2]) process.exit(1);
' "$w2/resource-events.log" "$orphan_pid" || fail "the sample line must name the port listener pid"
stop_watchdog_dir "$w2" "$wd"
watchdog_pids=""

# ---------------------------------------------------------------------------
# Scenario F (foreign listener inversion): the port answers with a process
# this slot never launched and never recorded — the watchdog must not sample
# it, not signal it, and not fabricate a breach out of it.
# ---------------------------------------------------------------------------
w3="$tmp_dir/wd-foreign"
write_manifest "$w3" RUYI-333-RES
stub4="$(spawn_stub "$w3" web)"
stub_pids="$stub_pids $stub4"
(
  set -m
  sleep 60 >/dev/null 2>&1 &
  printf '%s\n' "$!" > "$tmp_dir/foreign.pid"
)
foreign_pid="$(cat "$tmp_dir/foreign.pid")"
stub_pids="$stub_pids $foreign_pid"

PATH="$stub_bin:$PATH" FAKE_LISTEN_PORT="$(slot_field dev1 frontend_port)" FAKE_LISTEN_PID="$foreign_pid" \
  FAKE_RSS_PIDS="$foreign_pid" FAKE_RSS_KB=9999999 \
  MULTICA_SLOT_WATCHDOG_INTERVAL=1 MULTICA_SLOT_WATCHDOG_BREACHES=2 \
  bash "$root_dir/scripts/slot-watchdog.sh" dev1 "$w3" "$repo/scripts/slots.json" &
wd=$!
watchdog_pids="$wd"

sleep 4
kill -0 "$foreign_pid" 2>/dev/null || fail "an unproven port listener must never be signalled"
kill -0 "$stub4" 2>/dev/null || fail "the launcher of a slot with a foreign port listener stays untouched"
[ -f "$w3/resource-events.log" ] && grep -q "$foreign_pid" "$w3/resource-events.log" \
  && fail "the foreign listener must not appear as this slot's sample"
grep -q '"action":"breach"' "$w3/resource-events.log" 2>/dev/null \
  && fail "a fabricated RSS for a foreign pid must not cause a breach"
stop_watchdog_dir "$w3" "$wd"
watchdog_pids=""

# ---------------------------------------------------------------------------
# Inversion, still with zero load: the same healthy stub read by the REAL ps
# sits far below its quota and the watchdog must leave it alone — the kills
# above are caused by the guardrail acting on a (simulated) breach, not by
# the watchdog killing whatever it sees.
# ---------------------------------------------------------------------------
rm -f "$slot2/resource-events.log"
MULTICA_SLOT_WATCHDOG_INTERVAL=1 MULTICA_SLOT_WATCHDOG_BREACHES=2 \
  bash "$root_dir/scripts/slot-watchdog.sh" dev2 "$slot2" "$repo/scripts/slots.json" &
wd=$!
watchdog_pids="$wd"
sleep 4
kill -0 "$stub2" 2>/dev/null || fail "without a fabricated breach the watchdog must not kill a healthy stub"
# Shape/sample audit lines are expected; breaches and kills are not.
grep -q '"action":"breach"' "$slot2/resource-events.log" 2>/dev/null \
  && fail "a healthy stub must produce no breach events"
grep -q '"action":"kill"' "$slot2/resource-events.log" 2>/dev/null \
  && fail "a healthy stub must produce no kill events"
stop_watchdog_dir "$slot2" "$wd"
watchdog_pids=""

# A missing manifest ends the watchdog immediately (slot destroyed).
rm -f "$slot2/watchdog.stop" "$slot2/manifest.env"
bash "$root_dir/scripts/slot-watchdog.sh" dev2 "$slot2" "$repo/scripts/slots.json" &
wd2=$!
for _ in $(seq 1 10); do
  kill -0 "$wd2" 2>/dev/null || break
  sleep 0.3
done
kill -0 "$wd2" 2>/dev/null && fail "a watchdog without a manifest must exit"
wait "$wd2" 2>/dev/null || true

echo ""
echo "All dev-env resource tests passed."
