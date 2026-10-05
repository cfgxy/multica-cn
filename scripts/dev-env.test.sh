#!/usr/bin/env bash
# Registry-level behaviour of the fixed-slot model in scripts/dev-env.sh, with
# no services started.
#
# Everything runs against a throwaway MULTICA_SLOTS_HOME and a throwaway git
# repo: scripts/dev-env.sh is reached through a symlink inside that repo, so
# its REPO_ROOT — the fact source scripts/slots.json and every git operation —
# resolves inside the sandbox while the real repository stays untouched. The
# docker/psql/multica/make/go binaries on PATH are fakes; the psql fake keeps
# role/database state files so CREATE/DROP round-trips, and every statement is
# logged so the guard assertions can prove no SQL ever names the main database.
#
# Negative assertions here are written as "remove the guard and it must fail":
# each one pins an error message the guard prints, so deleting the guard fails
# the test instead of silently passing.
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp_dir="$(mktemp -d)"
tagged_pid=""
trap 'if [ -n "$tagged_pid" ]; then kill "$tagged_pid" 2>/dev/null || true; fi; rm -rf "$tmp_dir"' EXIT

# HOME is sandboxed too: slots.json's ~/-style worktree/workspaces roots are
# expanded against it, so the real ~/.multica must never be in reach.
mkdir -p "$tmp_dir/home"
export HOME="$tmp_dir/home"

# The agent runtime exports MULTICA_* values pointing at production; strip the
# ones that could leak into the script under test before anything runs.
unset MULTICA_SERVER_URL MULTICA_TOKEN MULTICA_WORKSPACE_ID MULTICA_DAEMON_PORT \
  MULTICA_AGENT_ID MULTICA_AGENT_NAME MULTICA_TASK_ID MULTICA_TASK_SLOT \
  MULTICA_CALLER_OWNER MULTICA_TASK_CONFIG_ROOT MULTICA_TASK_WORKSPACES_ROOT \
  MULTICA_WORKSPACES_ROOT 2>/dev/null || true

export MULTICA_SLOTS_HOME="$tmp_dir/slots"
export MULTICA_DEV_PROFILES_HOME="$tmp_dir/profiles"
export MULTICA_DEV_DESKTOP_APP_DATA="$tmp_dir/app-data"
export MULTICA_DEV_TMPDIR="$tmp_dir/dev-tmp"

fake_bin="$tmp_dir/bin"
state="$tmp_dir/state"
mkdir -p "$fake_bin" "$state"
psql_log="$tmp_dir/psql.log"
: >"$psql_log"
docker_log="$tmp_dir/docker.log"
: >"$docker_log"
multica_log="$tmp_dir/multica.log"
: >"$multica_log"

# --- fake psql: stateful admin endpoint of the shared postgres container -----
cat > "$fake_bin/psql" <<PSQLEOF
#!/usr/bin/env bash
db=""
query=""
stmt=""
while [ \$# -gt 0 ]; do
  case "\$1" in
    -d) db="\$2"; shift 2 ;;
    -tAc) query="\$2"; shift 2 ;;
    -c) stmt="\$2"; shift 2 ;;
    *) shift ;;
  esac
done
printf '%s\n' "psql -d \$db \${query:+[query] \$query}\${stmt:+[stmt] \$stmt}" >>"$psql_log"
roles="$state/roles"
dbs="$state/dbs"
touch "\$roles" "\$dbs"
if [ -n "\$query" ]; then
  name="\$(printf '%s' "\$query" | sed -n "s/^SELECT 1 FROM pg_roles WHERE rolname='\\(.*\\)'\$/\\1/p")"
  if [ -n "\$name" ]; then grep -Fqx "\$name" "\$roles" && echo 1 || echo 0; exit 0; fi
  name="\$(printf '%s' "\$query" | sed -n "s/^SELECT 1 FROM pg_database WHERE datname='\\(.*\\)'\$/\\1/p")"
  if [ -n "\$name" ]; then grep -Fqx "\$name" "\$dbs" && echo 1 || echo 0; exit 0; fi
  name="\$(printf '%s' "\$query" | sed -n "s/^SELECT datname FROM pg_database WHERE pg_get_userbyid(datdba)='\\(.*\\)' AND datname <> '.*'\$/\\1/p")"
  if [ -n "\$name" ]; then cat "$state/scratch-\$name" 2>/dev/null; exit 0; fi
  case "\$query" in *"LIKE 'multica%'"*) cat "$state/audit-dbs" 2>/dev/null; exit 0 ;; esac
  [ "\$query" = "SELECT 1" ] && { echo 1; exit 0; }
  exit 0
fi
if [ -n "\$stmt" ]; then
  case "\$stmt" in
    "CREATE ROLE"*|*"CREATE ROLE "*)
      name="\$(printf '%s' "\$stmt" | sed -n 's/^CREATE ROLE "\\(.*\\)".*/\\1/p')"
      [ -n "\$name" ] && ! grep -Fqx "\$name" "\$roles" && printf '%s\n' "\$name" >>"\$roles"
      [ "\${FAIL_CREATE_ROLE:-0}" = 1 ] && exit 1
      ;;
    "ALTER ROLE"*) ;;
    "CREATE DATABASE"*)
      name="\$(printf '%s' "\$stmt" | sed -n 's/^CREATE DATABASE "\\(.*\\)".*/\\1/p')"
      [ -n "\$name" ] && ! grep -Fqx "\$name" "\$dbs" && printf '%s\n' "\$name" >>"\$dbs"
      ;;
    "DROP DATABASE IF EXISTS"*)
      [ "\${FAIL_DROP:-0}" = 1 ] && exit 1
      name="\$(printf '%s' "\$stmt" | sed -n 's/^DROP DATABASE IF EXISTS "\\(.*\\)".*/\\1/p')"
      grep -Fvx "\$name" "\$dbs" >"\$dbs.tmp" 2>/dev/null && mv "\$dbs.tmp" "\$dbs" || : >"\$dbs"
      ;;
    "DROP ROLE IF EXISTS"*)
      name="\$(printf '%s' "\$stmt" | sed -n 's/^DROP ROLE IF EXISTS "\\(.*\\)"\$/\\1/p')"
      grep -Fvx "\$name" "\$roles" >"\$roles.tmp" 2>/dev/null && mv "\$roles.tmp" "\$roles" || : >"\$roles"
      ;;
    "REVOKE CONNECT"*) ;;
    "SELECT datname"*) printf 'multica\nmultica_dev1\npostgres\n' ;;
    *) ;;
  esac
  exit 0
fi
exit 0
PSQLEOF
chmod +x "$fake_bin/psql"

# --- fake docker: container is always running; exec reaches the psql fake ----
cat > "$fake_bin/docker" <<DOCKEOF
#!/usr/bin/env bash
printf '%s\n' "\$*" >>"$docker_log"
case "\$1" in
  inspect) printf '%s\n' "\${FAKE_DOCKER_RUNNING:-true}" ;;
  exec)
    shift 3   # exec -i multica-postgres-1
    exec "$fake_bin/psql" "\$@"
    ;;
  compose) exit 0 ;;
  *) exit 0 ;;
esac
DOCKEOF
chmod +x "$fake_bin/docker"

# --- fake multica CLI: issue status from state files -------------------------
cat > "$fake_bin/multica" <<MCEOF
#!/usr/bin/env bash
printf '%s\n' "\$*" >>"$multica_log"
if [ "\${1:-}" = issue ] && [ "\${2:-}" = get ]; then
  f="$state/issue-\${3:-}"
  if [ -f "\$f" ]; then printf '{"status":"%s"}\n' "\$(cat "\$f")"; exit 0; fi
  exit 1
fi
exit 0
MCEOF
chmod +x "$fake_bin/multica"

# --- fake make/go: enough for `up` to walk provisioning and migrations -------
printf '#!/usr/bin/env bash\nexit 0\n' > "$fake_bin/make"
printf '#!/usr/bin/env bash\nexit 0\n' > "$fake_bin/go"
chmod +x "$fake_bin/make" "$fake_bin/go"

# --- fake ss/lsof/curl: the sandbox never sees the real host's listeners ----
# dev-env's port checks (start_api/up, audit) must be decided by sandbox state,
# not by whichever slots happen to be running on the test machine — otherwise
# the suite is red whenever dev1's api is live. Listeners are seeded as
# "port pid" lines in $state/ss-listeners; curl never reaches a server.
ss_listeners="$state/ss-listeners"
: >"$ss_listeners"
cat > "$fake_bin/ss" <<SSEOF
#!/usr/bin/env bash
args="\$*"
case "\$args" in
  *"sport = :"*)
    want="\${args##*sport = :}"
    want="\${want%%[!0-9]*}"
    ;;
  *)
    want=""
    ;;
esac
while IFS= read -r entry; do
  [ -n "\$entry" ] || continue
  p="\${entry%% *}"; pid="\${entry#* }"
  if [ -n "\$want" ]; then
    [ "\$p" = "\$want" ] || continue
  fi
  printf 'LISTEN 0 128 127.0.0.1:%s 0.0.0.0:* users:(("stub",pid=%s,fd=5))\n' "\$p" "\$pid"
done < "$ss_listeners"
exit 0
SSEOF
chmod +x "$fake_bin/ss"
printf '#!/usr/bin/env bash\nexit 0\n' > "$fake_bin/lsof"
printf '#!/usr/bin/env bash\nexit 7\n' > "$fake_bin/curl"
chmod +x "$fake_bin/lsof" "$fake_bin/curl"

# Absolute path to the real ss, captured BEFORE the fakes take over PATH —
# the audit stubs use it to find the true socket-owning pid.
real_ss="$(command -v ss || true)"
[ -n "$real_ss" ] || { echo "FAIL: audit stubs need a real ss binary"; exit 1; }

export PATH="$fake_bin:$PATH"

fail() {
  echo "FAIL: $*" >&2
  if [ -n "${out:-}" ] && [ -f "$out" ]; then
    echo "Observed output:" >&2
    sed 's/^/  /' "$out" >&2
  fi
  exit 1
}

require_contains() {
  local file=$1 expected=$2
  if ! grep -Fq -- "$expected" "$file"; then
    echo "Expected output to contain: $expected" >&2
    echo "Observed:" >&2
    sed 's/^/  /' "$file" >&2
    exit 1
  fi
}

require_absent() {
  local file=$1 unwanted=$2
  if grep -Fq -- "$unwanted" "$file"; then
    echo "Expected output NOT to contain: $unwanted" >&2
    echo "Observed:" >&2
    sed 's/^/  /' "$file" >&2
    exit 1
  fi
}

# ---------------------------------------------------------------------------
# Sandbox repository: dev-env.sh runs through a symlink so REPO_ROOT, the
# slots.json fact source and every git operation stay inside $tmp_dir.
# ---------------------------------------------------------------------------
repo="$tmp_dir/repo"
mkdir -p "$repo/scripts" "$repo/server/cmd/migrate"
ln -s "$root_dir/scripts/dev-env.sh" "$repo/scripts/dev-env.sh"
ln -s "$root_dir/scripts/qa-clean.sh" "$repo/scripts/qa-clean.sh"
git -C "$repo" init -q
git -C "$repo" config user.email test@localhost
git -C "$repo" config user.name test
git -C "$repo" commit --allow-empty -q -m init
sha1="$(git -C "$repo" rev-parse HEAD)"
git -C "$repo" commit --allow-empty -q -m second
sha2="$(git -C "$repo" rev-parse HEAD)"

dev_env() {
  bash "$repo/scripts/dev-env.sh" "$@"
}

issue_a=RUYI-333
issue_b=RUYI-334

out="$tmp_dir/out"

# Write a slot manifest + env by hand (the shape save_manifest/generate_slot_env
# produce), so gc/destroy can be exercised without a full up. The lease phase
# is a manifest fact: dev (development) or qa (verification).
register_slot() { # slot issue dir ttl_hours expires_at [phase]
  local slot=$1 issue=$2 dir=$3 ttl=$4 expires=$5 phase=${6:-dev}
  local sdir="$MULTICA_SLOTS_HOME/$slot"
  mkdir -p "$sdir/logs"
  {
    printf 'NAME=%s\n' "$slot"
    printf 'PHASE=%s\n' "$phase"
    printf 'ISSUE=%s\n' "$issue"
    printf 'DIR=%q\n' "$dir"
    printf 'CODE_SOURCE=bind\n'
    printf 'CREATED_AT=2026-01-01T00:00:00Z\n'
    printf 'OWNER=agent\n'
    printf 'TTL_HOURS=%s\n' "$ttl"
    printf 'EXPIRES_AT=%s\n' "$expires"
    printf 'ENV_FILE=%s/env\n' "$sdir"
    printf 'BACKEND_PORT=20000\nFRONTEND_PORT=20001\nDESKTOP_RENDERER_PORT=20002\n'
    printf 'DB_NAME=multica_%s\nDB_ACCOUNT=%s_app\n' "$slot" "$slot"
    printf 'PROFILE=%s\n' "$slot"
    printf 'WORKSPACES_ROOT=%q\n' "$HOME/.multica/slots/$slot/workspaces"
    printf 'DESKTOP_APP_SUFFIX=%s\n' "$slot"
    printf "DESKTOP_USER_DATA_DIR='%s/Multica Canary %s'\n" "$MULTICA_DEV_DESKTOP_APP_DATA" "$slot"
  } > "$sdir/manifest.env"
  cat > "$sdir/env" <<ENVEOF
POSTGRES_DB=multica_$slot
POSTGRES_USER=${slot}_app
POSTGRES_PASSWORD=slotpw
DATABASE_URL=postgres://${slot}_app:slotpw@localhost:5432/multica_$slot?sslmode=disable
ENVEOF
}

past_iso="2020-01-01T00:00:00Z"
future_iso="2099-01-01T00:00:00Z"

# ---------------------------------------------------------------------------
# The resource budget is part of the canonical fact source: exactly two
# general slots, per-component memory quotas, pinned CPU sets, a capped
# shared postgres.
# ---------------------------------------------------------------------------
dev_env list > /dev/null 2>&1 || fail "bootstrapping the sandbox fact source must succeed"
node -e '
  const f = JSON.parse(require("fs").readFileSync(process.argv[1], "utf8"));
  if (f.slots.length !== 2) process.exit(1);
  if (f.slots.map(s => s.name).join(",") !== "dev1,dev2") process.exit(1);
  if (f.slots[0].cpuset !== "0-3" || f.slots[1].cpuset !== "4-7") process.exit(1);
  const b = f.resource_budget;
  if (b.slot_cpus !== 4) process.exit(1);
  const m = b.components;
  if (m.api.memory_mb !== 768 || m.web.memory_mb !== 8192 ||
      m.daemon.memory_mb !== 256 || m.desktop.memory_mb !== 256) process.exit(1);
  if (b.shared_postgres.memory !== "2g" || b.shared_postgres.cpus !== 4) process.exit(1);
' "$repo/scripts/slots.json" || fail "slots.json must carry the two-slot resource budget"

# component_resource_env translates the budget into per-component env so the
# quota travels with the process even before the watchdog is up.
for check in \
  'api|GOMEMLIMIT=768MiB' \
  'api|GOMAXPROCS=4' \
  'web|--max-old-space-size=8192' \
  'daemon|GOMEMLIMIT=256MiB'; do
  comp="${check%%|*}"; want="${check#*|}"
  bash -c 'source "$1"; require_slot dev2; component_resource_env "$2"' _ "$repo/scripts/dev-env.sh" "$comp" > "$out" 2>&1 \
    || fail "component_resource_env $comp must work"
  require_contains "$out" "$want"
done
bash -c 'source "$1"; require_slot dev2; component_resource_env desktop' _ "$repo/scripts/dev-env.sh" > "$out" 2>&1
[ -s "$out" ] && fail "desktop has no env-level quota (watchdog+pin enforce it); got: $(cat "$out")"

# ---------------------------------------------------------------------------
# A fresh machine lists both fixed slots, all free.
# ---------------------------------------------------------------------------
dev_env list > "$out" 2>&1 || fail "list on a fresh machine must succeed"
for s in dev1 dev2; do
  require_contains "$out" "$s"
done
require_absent "$out" "dev3"
require_absent "$out" "qa1"
require_contains "$out" "free"

dev_env list --json > "$out" 2>&1 || fail "list --json on a fresh machine must succeed"
node -e '
  const a = JSON.parse(require("fs").readFileSync(0, "utf8"));
  if (a.length !== 2) process.exit(1);
  if (a[0].slot !== "dev1" || a[1].slot !== "dev2") process.exit(1);
  if (a[0].phase !== "dev" || a[1].phase !== "dev") process.exit(1);
' < "$out" || fail "list --json must report two slots in dev phase"

# ---------------------------------------------------------------------------
# scripts/slots.json is tool-owned: a hand-edited fact source is restored, not
# obeyed — a tampered port or database name must never steer SQL or starts.
# ---------------------------------------------------------------------------
canonical="$(cat "$repo/scripts/slots.json")"
printf '{"slots": [{"name": "evil", "database": "multica", "account": "multica"}]}\n' > "$repo/scripts/slots.json"
dev_env list > "$out" 2>&1 || fail "list must survive a tampered slots.json"
[ "$(cat "$repo/scripts/slots.json")" = "$canonical" ] || fail "tampered slots.json was not restored to the canonical fact source"

# ---------------------------------------------------------------------------
# Slot names are closed over the fixed set — the old 8-slot names are gone,
# so asking for one must fail (delete valid_slot_name and this test fails).
# ---------------------------------------------------------------------------
for bad in dev3 dev4 qa1 qa2 dev5 evil ../../escape; do
  status=0
  MULTICA_CALLER_OWNER=$issue_a dev_env "$bad" up > "$out" 2>&1 || status=$?
  [ "$status" -ne 0 ] || fail "up accepted invalid slot name '$bad'"
done
status=0
MULTICA_CALLER_OWNER=$issue_a dev_env dev3 up > "$out" 2>&1 || status=$?
require_contains "$out" "Valid slots: dev1, dev2"

# ---------------------------------------------------------------------------
# Write verbs are lease-gated fail-closed: no MULTICA_CALLER_OWNER, no write.
# Reads (list/status/orphans on an unregistered slot) stay open.
# ---------------------------------------------------------------------------
for verb in "up" "use" "down" "destroy" "handoff --to qa"; do
  status=0
  dev_env dev1 $verb > "$out" 2>&1 || status=$?
  [ "$status" -ne 0 ] || fail "$verb without MULTICA_CALLER_OWNER must be refused"
  require_contains "$out" "MULTICA_CALLER_OWNER"
done

dev_env dev1 status > "$out" 2>&1 || fail "status without owner must work (read verb)"
require_contains "$out" "not registered"

# orphans is a read verb and must run against an unregistered slot; its exit
# code reflects the whole host (the sandbox cannot isolate /proc), so only
# the command's ability to run is pinned here.
dev_env dev1 orphans > "$out" 2>&1 || true
require_contains "$out" "Slot dev1"

# ---------------------------------------------------------------------------
# use binds code: no sha binds the calling checkout, a sha loads a revision
# into a detached worktree under the slot's own worktree root.
# ---------------------------------------------------------------------------
MULTICA_CALLER_OWNER=$issue_a dev_env dev1 use > "$out" 2>&1 || fail "use (bind) must succeed"
manifest="$MULTICA_SLOTS_HOME/dev1/manifest.env"
[ -f "$manifest" ] || fail "use must register the slot manifest"
manifest_dir="$(bash -c 'source "$1"; printf %s "$DIR"' _ "$manifest")"
[ "$manifest_dir" = "$repo" ] || fail "use without a sha must bind the calling checkout (got $manifest_dir)"
grep -q '^CODE_SOURCE=bind' "$manifest" || fail "bind use must record CODE_SOURCE=bind"
grep -q '^PHASE=dev' "$manifest" || fail "a fresh binding must record the dev phase"

status=0
MULTICA_CALLER_OWNER=$issue_a dev_env dev1 use 0000000000000000000000000000000000000000 > "$out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail "use with an unknown sha must be refused"

MULTICA_CALLER_OWNER=$issue_a dev_env dev1 use "$sha2" > "$out" 2>&1 || fail "use <sha> must succeed"
wt="$MULTICA_SLOTS_HOME/dev1/worktrees/ruyi-333"
[ -d "$wt" ] || fail "use <sha> must create a worktree under the slot worktree root"
[ "$(git -C "$wt" rev-parse HEAD)" = "$sha2" ] || fail "worktree must be detached at the requested sha"
[ -z "$(git -C "$wt" symbolic-ref -q HEAD || true)" ] || fail "worktree must be detached, not on a branch"
grep -q "^CODE_SHA=$sha2" "$manifest" || fail "manifest must record the loaded revision"

# The lease lives next to the manifest and names the caller.
[ -f "$MULTICA_SLOTS_HOME/dev1/.slot-lock" ] || fail "use must write a slot lease"
grep -q "OWNER_ISSUE=$issue_a" "$MULTICA_SLOTS_HOME/dev1/.slot-lock" || fail "lease must name the calling issue"

# Releasing a held lease still names the calling issue first.
status=0
dev_env dev1 lock-release > "$out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail "lock-release without MULTICA_CALLER_OWNER must be refused"
require_contains "$out" "MULTICA_CALLER_OWNER"

# lock-status reports the holder without needing an owner.
dev_env dev1 lock-status > "$out" 2>&1 || fail "lock-status must work"
require_contains "$out" "$issue_a"
require_contains "$out" "phase dev"

# ---------------------------------------------------------------------------
# A slot held by one issue is off limits to another while it is in_progress.
# ---------------------------------------------------------------------------
status=0
MULTICA_CALLER_OWNER=$issue_b dev_env dev1 use > "$out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail "a second issue must not be able to use a held slot"
require_contains "$out" "held by issue $issue_a"

printf 'in_progress' > "$state/issue-$issue_a"
status=0
MULTICA_CALLER_OWNER=$issue_b dev_env dev1 lock-recover > "$out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail "takeover while the holding issue is in_progress must be refused"
require_contains "$out" "in_progress"

status=0
MULTICA_CALLER_OWNER=$issue_b dev_env dev1 lock-release > "$out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail "lock-release by a foreign issue must be refused"

status=0
MULTICA_CALLER_OWNER=$issue_a dev_env dev1 lock-release > "$out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail "lock-release while the issue is in_progress must be refused"
require_contains "$out" "still in_progress"

# --force is the escape hatch, and it really clears the lease.
MULTICA_CALLER_OWNER=$issue_a dev_env dev1 lock-release --force > "$out" 2>&1 || fail "lock-release --force must succeed"
[ ! -f "$MULTICA_SLOTS_HOME/dev1/.slot-lock" ] || fail "lock-release --force must clear the lease file"

# Once the issue left in_progress, the next claimant may take over — and the
# takeover rewrites the manifest's issue too, so the original claimant is now
# the foreign one.
printf 'done' > "$state/issue-$issue_a"
MULTICA_CALLER_OWNER=$issue_b dev_env dev1 lock-recover > "$out" 2>&1 || fail "lock-recover after the issue left in_progress must succeed"
grep -q "OWNER_ISSUE=$issue_b" "$MULTICA_SLOTS_HOME/dev1/.slot-lock" || fail "after takeover the lease must name the new issue"
grep -q "^ISSUE=$issue_b" "$MULTICA_SLOTS_HOME/dev1/manifest.env" || fail "after takeover the manifest must adopt the new issue"

status=0
MULTICA_CALLER_OWNER=$issue_a dev_env dev1 lock-recover > "$out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail "takeover of an in_progress foreign lease must be refused"
printf 'done' > "$state/issue-$issue_b"
MULTICA_CALLER_OWNER=$issue_a dev_env dev1 lock-recover > "$out" 2>&1 || fail "taking the slot back must succeed once the foreign issue closed"
grep -q "^ISSUE=$issue_a" "$MULTICA_SLOTS_HOME/dev1/manifest.env" || fail "the manifest must record the current claimant"

# ---------------------------------------------------------------------------
# Role handover: the lease phase moves dev -> qa -> dev atomically inside one
# issue. The qa leg arms the only TTL in the system; the dev leg clears it.
# The handoff is owner-gated (delete the owner check and the foreign-issue
# case fails) and refuses no-op moves (delete the same-phase check and that
# case fails).
# ---------------------------------------------------------------------------
status=0
MULTICA_CALLER_OWNER=$issue_a dev_env dev2 handoff --to qa > "$out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail "handoff of a free slot must be refused"
require_contains "$out" "is free"

status=0
MULTICA_CALLER_OWNER=$issue_a dev_env dev1 handoff > "$out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail "handoff without --to must be refused"
require_contains "$out" "must be dev or qa"

status=0
MULTICA_CALLER_OWNER=$issue_a dev_env dev1 handoff --to staging > "$out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail "handoff to an unknown phase must be refused"

status=0
MULTICA_CALLER_OWNER=$issue_b dev_env dev1 handoff --to qa > "$out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail "a foreign issue must not hand the slot off"
require_contains "$out" "Only the holding issue may hand"

MULTICA_CALLER_OWNER=$issue_a dev_env dev1 handoff --to qa --note "dev complete" > "$out" 2>&1 || fail "dev->qa handoff must succeed"
grep -q '^PHASE=qa' "$MULTICA_SLOTS_HOME/dev1/.slot-lock" || fail "the lease must carry the qa phase after handoff"
grep -q '^PHASE=qa' "$MULTICA_SLOTS_HOME/dev1/manifest.env" || fail "the manifest must mirror the qa phase"
ttl="$(sed -n 's/^TTL_HOURS=//p' "$MULTICA_SLOTS_HOME/dev1/manifest.env" | head -1)"
[ "$ttl" = 24 ] || fail "the qa leg must arm the 24h TTL (got '$ttl')"
grep -q '^EXPIRES_AT=20' "$MULTICA_SLOTS_HOME/dev1/manifest.env" || fail "the qa leg must set EXPIRES_AT"
[ -f "$MULTICA_SLOTS_HOME/dev1/lease-history.log" ] || fail "handoff must append to the lease history"
require_contains "$MULTICA_SLOTS_HOME/dev1/lease-history.log" "$issue_a dev->qa dev complete"

dev_env dev1 lock-status > "$out" 2>&1 || fail "lock-status must work"
require_contains "$out" "phase qa"

status=0
MULTICA_CALLER_OWNER=$issue_a dev_env dev1 handoff --to qa > "$out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail "handoff to the current phase must be refused"
require_contains "$out" "already in phase qa"

MULTICA_CALLER_OWNER=$issue_a dev_env dev1 handoff --to dev > "$out" 2>&1 || fail "qa->dev handoff must succeed"
grep -q '^PHASE=dev' "$MULTICA_SLOTS_HOME/dev1/.slot-lock" || fail "the lease must carry the dev phase after handoff back"
ttl="$(sed -n 's/^TTL_HOURS=//p' "$MULTICA_SLOTS_HOME/dev1/manifest.env" | head -1)"
[ "$ttl" = 0 ] || fail "returning to dev must clear the TTL (got '$ttl')"
[ "$(sed -n 's/^EXPIRES_AT=//p' "$MULTICA_SLOTS_HOME/dev1/manifest.env" | head -1)" = "''" ] || fail "returning to dev must clear EXPIRES_AT"
require_contains "$MULTICA_SLOTS_HOME/dev1/lease-history.log" "$issue_a qa->dev"

# ---------------------------------------------------------------------------
# Preflight, negative: the env file the components would run from is checked
# against the slot facts before anything starts. Each case rewrites the slot
# env with a poisoned identity and expects up to refuse with the guard's own
# message — delete the guard and the test fails.
# ---------------------------------------------------------------------------
slot_env="$MULTICA_SLOTS_HOME/dev1/env"

write_dev1_env() { # database user endpoint [declared_db] [declared_user]
  cat > "$slot_env" <<ENVEOF
POSTGRES_DB=${4:-multica_dev1}
POSTGRES_USER=${5:-dev1_app}
POSTGRES_PASSWORD=x
DATABASE_URL=postgres://${2}:${2}@${3}/${1}?sslmode=disable
ENVEOF
}

# 1. pointing at the main database `multica`
write_dev1_env multica dev1_app localhost:5432
status=0
MULTICA_CALLER_OWNER=$issue_a dev_env dev1 up --components api > "$out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail "up must refuse a slot env pointing at the main database"
require_contains "$out" "Refusing to start against another database"

# 2. connecting as the main/superuser role `multica`
write_dev1_env multica_dev1 multica localhost:5432
status=0
MULTICA_CALLER_OWNER=$issue_a dev_env dev1 up --components api > "$out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail "up must refuse a slot env connecting as the main role"
require_contains "$out" "not a slot identity"

# 3. a remote endpoint
write_dev1_env multica_dev1 dev1_app 10.1.2.3:5432
status=0
MULTICA_CALLER_OWNER=$issue_a dev_env dev1 up --components api > "$out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail "up must refuse a non-localhost endpoint"
require_contains "$out" "never talk to other hosts"

# 4. declared POSTGRES_DB disagreeing with the URL
write_dev1_env multica_dev1 dev1_app localhost:5432 multica
status=0
MULTICA_CALLER_OWNER=$issue_a dev_env dev1 up --components api > "$out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail "up must refuse a POSTGRES_DB/URL disagreement"
require_contains "$out" "POSTGRES_DB=multica in $slot_env disagrees"

# 5. manifest drifted from the fact source (simulated old slot)
sed -i 's/^DB_NAME=multica_dev1/DB_NAME=multica_old/' "$manifest"
status=0
MULTICA_CALLER_OWNER=$issue_a dev_env dev1 up --components api > "$out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail "up must refuse a manifest that disagrees with slots.json"
require_contains "$out" "disagrees with scripts/slots.json"
sed -i 's/^DB_NAME=multica_old/DB_NAME=multica_dev1/' "$manifest"

# ---------------------------------------------------------------------------
# Preflight, positive: a generated env names exactly the slot identity, and up
# walks provisioning + migrations before the (stubbed) component start fails.
# ---------------------------------------------------------------------------
rm -f "$slot_env"
MULTICA_CALLER_OWNER=$issue_a dev_env dev1 use > "$out" 2>&1 || fail "regenerating the slot env via use must succeed"
[ "$(stat -c %a "$slot_env")" = 600 ] || fail "slot env file must be chmod 600 (it holds the slot account password)"
grep -q '^POSTGRES_DB=multica_dev1$' "$slot_env" || fail "slot env must name the slot database"
grep -q '^POSTGRES_USER=dev1_app$' "$slot_env" || fail "slot env must name the slot account"
grep -Eq '^DATABASE_URL=postgres://dev1_app:[^@]+@localhost:5432/multica_dev1\?' "$slot_env" || fail "slot env DATABASE_URL must be slot-account@localhost:5432/slot-db"
if grep -Eq '^DATABASE_URL=postgres://[^/]+/multica\?' "$slot_env"; then
  fail "slot env must never point at the main database multica"
fi
if grep -q '^POSTGRES_USER=multica$' "$slot_env"; then
  fail "slot env must never use the main role as its identity"
fi

lines_before="$(wc -l < "$psql_log")"
status=0
MULTICA_CALLER_OWNER=$issue_a dev_env dev1 up --components api > "$out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail "up with stubbed components is expected to stop at component start in this suite"
require_contains "$out" "reachable through the slot account and migrated"
require_contains "$out" "api exited during startup"

sql="$(tail -n +"$((lines_before + 1))" "$psql_log")"
printf '%s' "$sql" | grep -qF 'CREATE ROLE "dev1_app" WITH LOGIN CREATEDB PASSWORD' || fail "provisioning must create the slot account with CREATEDB (integration tests mint scratch databases)"
printf '%s' "$sql" | grep -qF 'CREATE DATABASE "multica_dev1" OWNER "dev1_app"' || fail "provisioning must create the slot database owned by the slot account"
printf '%s' "$sql" | grep -qF 'REVOKE CONNECT ON DATABASE "multica_dev1" FROM PUBLIC' || fail "provisioning must revoke PUBLIC CONNECT on the slot database"
if printf '%s' "$sql" | grep -qF 'psql -d multica '; then
  fail "no provisioning statement may target the main database as psql -d multica"
fi
if printf '%s' "$sql" | grep -Eq 'DROP DATABASE|DROP ROLE'; then
  fail "up must never drop anything"
fi

# ---------------------------------------------------------------------------
# exec: the slot's own variables, without the runtime's production MULTICA_*.
# ---------------------------------------------------------------------------
MULTICA_TASK_ID=prod-task-1 dev_env dev1 exec -- env > "$out" 2>&1 || fail "exec must run the command"
require_contains "$out" "MULTICA_DEV_PROFILE=dev1"
require_contains "$out" "DATABASE_URL=postgres://dev1_app:"
require_contains "$out" "TMPDIR=$MULTICA_DEV_TMPDIR"
require_absent "$out" "prod-task-1"

status=0
MULTICA_CALLER_OWNER=RUYI-999 dev_env dev1 exec -- env > "$out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail "exec as a foreign issue must be refused"
require_contains "$out" "held by $issue_a"

# ---------------------------------------------------------------------------
# status --json reports the registered slot with its lease phase.
# ---------------------------------------------------------------------------
dev_env dev1 status --json > "$out" 2>&1 || fail "status --json must work"
node -e '
  const s = JSON.parse(require("fs").readFileSync(0, "utf8"));
  if (s.slot !== "dev1") process.exit(1);
  if (s.phase !== "dev") process.exit(1);
  if (s.lease_issue !== "RUYI-333") process.exit(1);
  if (!s.dir.endsWith("/repo")) process.exit(1);
  if (s.database !== "multica_dev1" || s.account !== "dev1_app") process.exit(1);
  if (s.components.api.state !== "stopped") process.exit(1);
' < "$out" || fail "status --json content mismatch"

# ---------------------------------------------------------------------------
# destroy: two-source drop guard, then the real teardown.
# ---------------------------------------------------------------------------
lines_before="$(wc -l < "$psql_log")"
status=0
MULTICA_CALLER_OWNER=$issue_a dev_env dev1 destroy --yes > "$out" 2>&1 || status=$?
[ "$status" -eq 0 ] || { cat "$out" >&2; fail "destroy of a consistent slot must succeed"; }
sql="$(tail -n +"$((lines_before + 1))" "$psql_log")"
printf '%s' "$sql" | grep -qF 'DROP DATABASE IF EXISTS "multica_dev1" WITH (FORCE)' || fail "destroy must drop the slot database"
printf '%s' "$sql" | grep -qF 'DROP ROLE IF EXISTS "dev1_app"' || fail "destroy must drop the slot account"
[ ! -d "$MULTICA_SLOTS_HOME/dev1" ] || fail "destroy must remove the slot directory"

dev_env dev1 status > "$out" 2>&1 || fail "status after destroy must work"
require_contains "$out" "not registered"

# Two-source guard: the manifest AND the env file must both name the slot
# database before a drop is built. An env renamed to the main database must
# keep the slot alive and drop nothing.
MULTICA_CALLER_OWNER=$issue_a dev_env dev2 use > "$out" 2>&1 || fail "setup: bind dev2"
printf 'POSTGRES_DB=multica\nPOSTGRES_USER=multica\nPOSTGRES_PASSWORD=x\nDATABASE_URL=postgres://multica:x@localhost:5432/multica?sslmode=disable\n' > "$MULTICA_SLOTS_HOME/dev2/env"
lines_before="$(wc -l < "$psql_log")"
status=0
MULTICA_CALLER_OWNER=$issue_a dev_env dev2 destroy --yes > "$out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail "destroy must refuse when the env file names a different database"
require_contains "$out" "must both name it before a drop"
[ -f "$MULTICA_SLOTS_HOME/dev2/manifest.env" ] || fail "a refused destroy must keep the manifest for retry"
[ "$(wc -l < "$psql_log")" -eq "$lines_before" ] || fail "a refused destroy must issue no SQL"

# Repairing the env lets the destroy finish.
cat > "$MULTICA_SLOTS_HOME/dev2/env" <<'ENVEOF'
POSTGRES_DB=multica_dev2
POSTGRES_USER=dev2_app
POSTGRES_PASSWORD=x
DATABASE_URL=postgres://dev2_app:x@localhost:5432/multica_dev2?sslmode=disable
ENVEOF
# Scratch databases minted at runtime by the slot account (integration tests)
# must be collected before DROP ROLE, but a protected name must never be
# dropped even if the ownership catalog somehow lists it.
printf 'multica_v034_upgrade_123\nmultica\n' > "$state/scratch-dev2_app"
lines_before="$(wc -l < "$psql_log")"
MULTICA_CALLER_OWNER=$issue_a dev_env dev2 destroy --yes > "$out" 2>&1 || fail "destroy after repairing the env must succeed"
[ ! -d "$MULTICA_SLOTS_HOME/dev2" ] || fail "destroy must remove the slot directory"
sql="$(tail -n +"$((lines_before + 1))" "$psql_log")"
printf '%s' "$sql" | grep -qF 'DROP DATABASE IF EXISTS "multica_v034_upgrade_123" WITH (FORCE)' || fail "destroy must collect scratch databases owned by the slot account"
require_contains "$out" "refusing to drop suspect database multica"
if printf '%s' "$sql" | grep -qF 'DROP DATABASE IF EXISTS "multica" WITH'; then
  fail "destroy must never drop the main database, not even as a scratch candidate"
fi

# ---------------------------------------------------------------------------
# Worktree safety: a branch checkout or dirty worktree blocks destroy; a clean
# detached worktree is the only removable shape.
# ---------------------------------------------------------------------------
MULTICA_CALLER_OWNER=$issue_a dev_env dev1 use "$sha1" > "$out" 2>&1 || fail "setup: load dev1"
wt1="$MULTICA_SLOTS_HOME/dev1/worktrees/ruyi-333"
git -C "$wt1" checkout -q -b feature-under-test
status=0
MULTICA_CALLER_OWNER=$issue_a dev_env dev1 destroy --yes > "$out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail "destroy must refuse to remove a branch worktree"
require_contains "$out" "on a branch"
[ -f "$MULTICA_SLOTS_HOME/dev1/manifest.env" ] || fail "a blocked destroy must keep the manifest"

git -C "$wt1" checkout -q --detach "$sha1"
MULTICA_CALLER_OWNER=$issue_a dev_env dev1 destroy --yes > "$out" 2>&1 || fail "destroy of a detached clean worktree slot must succeed"
[ ! -d "$MULTICA_SLOTS_HOME/dev1" ] || fail "destroy must remove the slot directory"
[ ! -d "$wt1" ] || fail "destroy must remove the slot worktree"

# ---------------------------------------------------------------------------
# Partial failure keeps the slot: a failed drop leaves the manifest + lease so
# destroy can retry instead of stranding the database.
# ---------------------------------------------------------------------------
MULTICA_CALLER_OWNER=$issue_a dev_env dev2 use > "$out" 2>&1 || fail "setup: bind dev2"
status=0
FAIL_DROP=1 MULTICA_CALLER_OWNER=$issue_a dev_env dev2 destroy --yes > "$out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail "destroy with a failing drop must report failure"
require_contains "$out" "partially destroyed"
[ -f "$MULTICA_SLOTS_HOME/dev2/manifest.env" ] || fail "a partial destroy must keep the manifest"
MULTICA_CALLER_OWNER=$issue_a dev_env dev2 destroy --yes > "$out" 2>&1 || fail "retry after the drop failure must succeed"
[ ! -d "$MULTICA_SLOTS_HOME/dev2" ] || fail "retry destroy must remove the slot directory"

# ---------------------------------------------------------------------------
# orphans: the acceptance verb for the no-survivors invariant. It scans live
# /proc, ports, git worktrees and docker — no manifest needed, so it stays
# meaningful after destroy removed the bookkeeping. The sandbox cannot
# isolate /proc, so assertions pin membership (our tagged pid appears, then
# disappears) rather than global cleanliness.
# ---------------------------------------------------------------------------
mkdir -p "$MULTICA_SLOTS_HOME/dev1"
( export MULTICA_SLOT=dev1; cd "$MULTICA_SLOTS_HOME/dev1" && exec sleep 300 ) &
tagged_pid=$!

dev_env dev1 orphans > "$out" 2>&1 && fail "orphans must exit 1 while a MULTICA_SLOT=dev1 process lives"
require_contains "$out" "$tagged_pid"
require_contains "$out" "MULTICA_SLOT=dev1"

dev_env dev1 orphans --json > "$out" 2>&1 || fail "orphans --json must work"
node -e '
  const a = JSON.parse(require("fs").readFileSync(0, "utf8"));
  if (!a.some(o => o.class === "process-env" && o.detail.includes(process.argv[1]))) process.exit(1);
' "$tagged_pid" < "$out" || fail "orphans --json must classify the tagged process"

kill "$tagged_pid" 2>/dev/null || true
wait "$tagged_pid" 2>/dev/null || true
dev_env dev1 orphans > "$out" 2>&1 || true
if grep -Fq "	$tagged_pid (" "$out"; then
  fail "the killed process must no longer be reported"
fi

# Worktree leftovers under the slot root are orphans too: a registered git
# worktree and a bare directory both count.
mkdir -p "$MULTICA_SLOTS_HOME/dev1/worktrees/leftover-dir"
git -C "$repo" worktree add -q --detach "$MULTICA_SLOTS_HOME/dev1/worktrees/wt-reg" HEAD
dev_env dev1 orphans > "$out" 2>&1 || status=$?
[ "${status:-0}" = 1 ] || fail "orphans must exit 1 while slot worktree entries exist"
require_contains "$out" "leftover-dir"
require_contains "$out" "wt-reg"
rm -rf "$MULTICA_SLOTS_HOME/dev1/worktrees"
git -C "$repo" worktree prune

# ---------------------------------------------------------------------------
# gc collects expired qa-phase slots, slides over live ones, never touches
# dev-phase slots (no TTL — the slot is handed back at issue closure), and
# takes an expired qa-phase slot even from a dead lease.
# ---------------------------------------------------------------------------
register_slot dev1 RUYI-401 "$repo" 24 "$past_iso" qa
register_slot dev2 RUYI-402 "$repo" 24 "$future_iso" qa

dev_env gc --dry-run > "$out" 2>&1 || fail "gc --dry-run must succeed"
require_contains "$out" "dev1 would be collected"
require_absent "$out" "dev2"
[ -d "$MULTICA_SLOTS_HOME/dev1" ] || fail "dry run must not destroy anything"

lines_before="$(wc -l < "$psql_log")"
dev_env gc > "$out" 2>&1 || fail "gc must succeed"
require_contains "$out" "dev1"
sql="$(tail -n +"$((lines_before + 1))" "$psql_log")"
printf '%s' "$sql" | grep -qF 'DROP DATABASE IF EXISTS "multica_dev1"' || fail "gc must drop the expired qa-phase slot database"
[ ! -d "$MULTICA_SLOTS_HOME/dev1" ] || fail "gc must free the expired qa-phase slot"
[ -d "$MULTICA_SLOTS_HOME/dev2" ] || fail "gc must not collect a live qa-phase slot"

# A dev-phase slot is never TTL-collected, even with a long-past stamp.
register_slot dev1 RUYI-403 "$repo" 0 "$past_iso" dev
dev_env gc > "$out" 2>&1 || fail "gc must succeed"
[ -d "$MULTICA_SLOTS_HOME/dev1" ] || fail "gc must never collect a dev-phase slot (no TTL)"

# A qa-phase slot whose code directory vanished is collected too.
register_slot dev1 RUYI-404 "$tmp_dir/vanished-checkout" 24 "$future_iso" qa
dev_env gc > "$out" 2>&1 || fail "gc must succeed"
[ ! -d "$MULTICA_SLOTS_HOME/dev1" ] || fail "gc must collect a qa-phase slot whose code directory is gone"

# An expired qa-phase slot with a stale lease is still collected — the lease
# of a forgotten slot must not outlive its TTL.
register_slot dev1 RUYI-405 "$repo" 24 "$past_iso" qa
printf 'OWNER_ISSUE=RUYI-405\nOWNER_ROLE=agent\nACQUIRED_AT=%s\nPHASE=qa\n' "$past_iso" > "$MULTICA_SLOTS_HOME/dev1/.slot-lock"
dev_env gc > "$out" 2>&1 || fail "gc must succeed against a stale lease"
[ ! -d "$MULTICA_SLOTS_HOME/dev1" ] || fail "gc must collect an expired qa-phase slot even under a stale lease"

# ---------------------------------------------------------------------------
# main-db: the shared instance window is opt-in and read-only.
# ---------------------------------------------------------------------------
status=0
dev_env main-db status > "$out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail "main-db status must refuse without --allow"
require_contains "$out" "--allow"

lines_before="$(wc -l < "$psql_log")"
dev_env main-db status --allow > "$out" 2>&1 || fail "main-db status --allow must work"
sql="$(tail -n +"$((lines_before + 1))" "$psql_log")"
printf '%s' "$sql" | grep -qF 'SELECT datname' || fail "main-db status must run its inventory query"
if printf '%s' "$sql" | grep -Eq 'DROP |ALTER |CREATE |INSERT |UPDATE |DELETE '; then
  fail "main-db status must be read-only"
fi

# ---------------------------------------------------------------------------
# Manifest values are shell-escaped: sourcing a manifest must never execute a
# value, and every value must round-trip.
# ---------------------------------------------------------------------------
quoted="$tmp_dir/quoted.env"
dangerous='a path with spaces;$(touch should-not-exist)'
bash -c 'source "$1"; write_manifest_value DIR "$2"' _ "$repo/scripts/dev-env.sh" "$dangerous" > "$quoted"
loaded="$(bash -c 'source "$1"; printf %s "$DIR"' _ "$quoted")"
[ "$loaded" = "$dangerous" ] || fail "manifest value did not round-trip safely"
[ ! -e "$repo/should-not-exist" ] || fail "loading a manifest executed its value"

# ---------------------------------------------------------------------------
# qa-clean: scoped dry run reports the issue's slot and touches nothing. The
# issue-scoped sweep covers any phase — QA reuses the issue's dev slot, so
# the lease may be back in dev when the sweep runs.
# ---------------------------------------------------------------------------
register_slot dev1 RUYI-401 "$repo" 24 "$future_iso" dev
status=0
bash "$repo/scripts/qa-clean.sh" --issue RUYI-401 > "$out" 2>&1 || status=$?
[ "$status" -eq 0 ] || fail "qa-clean dry run must succeed"
require_contains "$out" "would destroy slot dev1"
require_contains "$out" "RUYI-401"
[ -d "$MULTICA_SLOTS_HOME/dev1" ] || fail "qa-clean dry run must not destroy"

# Scoped --yes destroys the matching slot whatever its phase and leaves
# other issues' slots alone.
register_slot dev2 RUYI-402 "$repo" 24 "$future_iso" qa
bash "$repo/scripts/qa-clean.sh" --issue RUYI-401 --yes > "$out" 2>&1 || fail "qa-clean --yes must succeed"
[ ! -d "$MULTICA_SLOTS_HOME/dev1" ] || fail "qa-clean --issue must destroy the matching slot (any phase)"
[ -d "$MULTICA_SLOTS_HOME/dev2" ] || fail "qa-clean --issue must not touch another issue's slot"

# Unscoped --yes without an owner must not destroy anyone else's slot — a
# qa-phase slot held by a foreign issue is skipped, not destroyed.
bash "$repo/scripts/qa-clean.sh" --yes > "$out" 2>&1 || fail "qa-clean unscoped --yes must succeed"
[ -d "$MULTICA_SLOTS_HOME/dev2" ] || fail "qa-clean unscoped --yes must not destroy a slot it has no owner for"
require_contains "$out" "skipped slot dev2"

# ---------------------------------------------------------------------------
# RUYI-431: dual-server test windows on the two fixed slots. One issue may
# hold BOTH slots at once — two independent servers on two fixed fact sets,
# never a second slot model — and a qa-phase holder closes its window with
# lock-release while its issue is still in_progress. The dev phase keeps the
# closure-only rule.
# ---------------------------------------------------------------------------
rm -rf "$MULTICA_SLOTS_HOME/dev1" "$MULTICA_SLOTS_HOME/dev2"
printf 'in_progress' > "$state/issue-$issue_a"
MULTICA_CALLER_OWNER=$issue_a dev_env dev1 use --phase qa > "$out" 2>&1 || fail "dual-window: use dev1 --phase qa must succeed"
MULTICA_CALLER_OWNER=$issue_a dev_env dev2 use --phase qa > "$out" 2>&1 || fail "dual-window: the same issue must be able to hold the second slot"
grep -q '^PHASE=qa' "$MULTICA_SLOTS_HOME/dev1/manifest.env" || fail "dual-window: dev1 must be armed in the qa phase"
grep -q "OWNER_ISSUE=$issue_a" "$MULTICA_SLOTS_HOME/dev2/.slot-lock" || fail "dual-window: dev2's lease must name the same issue"

dev_env list --json > "$out" 2>&1 || fail "dual-window: list --json must work"
node -e '
  const a = JSON.parse(require("fs").readFileSync(0, "utf8"));
  const held = a.filter(s => s.lease_issue === process.argv[1]);
  if (held.length !== 2 || !held.every(s => s.phase === "qa")) process.exit(1);
' "$issue_a" < "$out" || fail "dual-window: list --json must show both slots held by one issue in qa phase"

# The window closes the moment the test ends: the qa-phase holder releases
# both slots without waiting for the issue to leave in_progress and without
# --force.
MULTICA_CALLER_OWNER=$issue_a dev_env dev1 lock-release > "$out" 2>&1 || fail "qa-window: lock-release of dev1 must succeed while the issue is in_progress"
[ ! -f "$MULTICA_SLOTS_HOME/dev1/.slot-lock" ] || fail "qa-window: dev1's lease must be gone"
MULTICA_CALLER_OWNER=$issue_a dev_env dev2 lock-release > "$out" 2>&1 || fail "qa-window: lock-release of dev2 must succeed"
[ ! -f "$MULTICA_SLOTS_HOME/dev2/.slot-lock" ] || fail "qa-window: dev2's lease must be gone"

# The dev phase keeps the closure-only rule: an in_progress issue cannot
# release its development slot, owner or not.
MULTICA_CALLER_OWNER=$issue_a dev_env dev1 use > "$out" 2>&1 || fail "qa-window: rebinding dev1 in dev phase must succeed"
status=0
MULTICA_CALLER_OWNER=$issue_a dev_env dev1 lock-release > "$out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail "qa-window: dev-phase lock-release while in_progress must stay refused"
require_contains "$out" "still in_progress"
MULTICA_CALLER_OWNER=$issue_a dev_env dev1 lock-release --force > "$out" 2>&1 || fail "qa-window: cleanup release must succeed"

# ---------------------------------------------------------------------------
# audit (RUYI-431): report-only bypass detection. Unregistered multica_%
# databases on the shared instance and Multica-shaped listeners outside the
# slot model are listed; registered facts never are. Exit 1 = findings,
# and the verb never kills or drops anything.
# ---------------------------------------------------------------------------
printf 'multica|1 GB\nmultica_dev1|120 MB\nmultica_dev2|130 MB\nmultica_qa415ra|16 MB\nmultica_c1|16 MB\n' > "$state/audit-dbs"
status=0
dev_env audit > "$out" 2>&1 || status=$?
[ "$status" = 1 ] || fail "audit must exit 1 while unregistered multica_% databases exist"
require_contains "$out" "unregistered-database"
require_contains "$out" "multica_qa415ra"
require_contains "$out" "multica_c1"
require_absent "$out" "unregistered-database	multica_dev1"
require_absent "$out" "unregistered-database	multica_dev2"
require_absent "$out" "unregistered-database	multica	"

dev_env audit --json > "$out" 2>&1 || true
node -e '
  const a = JSON.parse(require("fs").readFileSync(0, "utf8"));
  if (!Array.isArray(a)) process.exit(1);
  const dbs = a.filter(f => f.class === "unregistered-database").map(f => f.detail);
  if (!dbs.some(d => d.startsWith("multica_qa415ra"))) process.exit(1);
  if (dbs.some(d => d.startsWith("multica_dev1") || d.startsWith("multica ("))) process.exit(1);
' < "$out" || fail "audit --json must classify unregistered databases only"

# A listener whose DATABASE_URL names an unregistered multica_% database is a
# bypass server even on a port the registry never mentions. The stub is
# bounded three ways: a memory ulimit, a timeout suicide timer, and an
# explicit kill verified by the port actually closing. The real ss (absolute
# path — PATH carries the sandbox ss) reports the socket-owning pid, exactly
# what production discovery would see; the stub is then registered into the
# sandbox listener table so the tool under test finds it there.
audit_stub() { # port db-url [extra env K=V...] -> echoes the listening pid
  local port=$1 url=$2; shift 2
  # stdio detached: a background child holding this function's stdout pipe
  # would keep the caller's command substitution open until timeout kills it.
  ( ulimit -v 2097152; cd "$tmp_dir" 2>/dev/null || cd /
    exec timeout 60 env "$@" DATABASE_URL="$url" \
      node -e "require('net').createServer(()=>{}).listen($port, '127.0.0.1')" ) \
    </dev/null >/dev/null 2>&1 &
  local waited=0 listener=""
  while [ "$waited" -lt 50 ]; do
    listener="$("$real_ss" -ltnp "sport = :$port" 2>/dev/null | sed -n 's/.*pid=\([0-9]\{1,\}\).*/\1/p' | head -1)"
    [ -n "$listener" ] && break
    waited=$((waited + 1)); sleep 0.1
  done
  [ -n "$listener" ] || fail "audit stub on :$port never came up"
  printf '%s %s\n' "$port" "$listener" >> "$state/ss-listeners"
  printf '%s' "$listener"
}
audit_stub_down() { # listening-pid port
  kill "$1" 2>/dev/null || true
  local waited=0
  while "$real_ss" -ltn "sport = :$2" 2>/dev/null | grep -q LISTEN; do
    waited=$((waited + 1)); [ "$waited" -lt 100 ] || break
    sleep 0.1
  done
  grep -v "^$2 " "$state/ss-listeners" > "$state/ss-listeners.next" \
    && mv "$state/ss-listeners.next" "$state/ss-listeners" || : > "$state/ss-listeners"
}

stub_bypass="$(audit_stub 21999 "postgres://qa:pw@localhost:5432/multica_qa415ra?sslmode=disable")"
status=0
dev_env audit > "$out" 2>&1 || status=$?
[ "$status" = 1 ] || fail "audit must flag the DATABASE_URL bypass stub"
require_contains "$out" "bypass-listener"
require_contains "$out" "$stub_bypass"
require_contains "$out" "multica_qa415ra"
audit_stub_down "$stub_bypass" 21999

# Slot-tagged processes and registered-database URLs are the slot model's own:
# neither may be reported, whatever port they sit on. The JSON must also PARSE —
# a half-finished scan that crashed partway would otherwise sneak past the
# pid-absence greps.
stub_tagged="$(audit_stub 21998 "postgres://qa:pw@localhost:5432/multica_qa415ra?sslmode=disable" MULTICA_SLOT=dev1)"
stub_registered="$(audit_stub 21997 "postgres://dev1_app:pw@localhost:5432/multica_dev1?sslmode=disable")"
dev_env audit --json > "$out" 2>&1 || true
node -e '
  const a = JSON.parse(require("fs").readFileSync(0, "utf8"));
  if (!Array.isArray(a)) process.exit(1);
  const hits = a.filter(f => JSON.stringify(f).includes(process.argv[1])
                             || JSON.stringify(f).includes(process.argv[2]));
  process.exit(hits.length ? 1 : 0);
' "$stub_tagged" "$stub_registered" < "$out" \
  || fail "audit must skip MULTICA_SLOT-tagged and registered-database listeners"
audit_stub_down "$stub_tagged" 21998
audit_stub_down "$stub_registered" 21997

echo ""
echo "All dev-env slot tests passed."
