#!/usr/bin/env bash
# Registry-level behaviour of scripts/dev-env.sh, with no services started.
#
# Everything here runs against a throwaway MULTICA_DEV_HOME holding hand-written
# manifests, so the verbs are exercised end to end without a database, a
# backend, or a port.
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

export MULTICA_DEV_HOME="$tmp_dir/dev"
export MULTICA_DEV_WORKSPACES_PARENT="$tmp_dir/workspaces-parent"
export MULTICA_DEV_DESKTOP_APP_DATA="$tmp_dir/app-data"
export MULTICA_DEV_PROFILES_HOME="$tmp_dir/profiles"

fake_bin="$tmp_dir/bin"
mkdir -p "$fake_bin"
psql_log="$tmp_dir/psql.log"
: >"$psql_log"
cat > "$fake_bin/psql" <<EOF
#!/usr/bin/env bash
printf '%s\n' "\$*" >>"$psql_log"
case " \$* " in
  *" DROP DATABASE "*) [ "\${FAIL_DROP:-0}" != 1 ] ;;
  *) printf '1\n' ;;
esac
EOF
chmod +x "$fake_bin/psql"
export PATH="$fake_bin:$PATH"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

require_contains() {
  local file=$1 expected=$2
  if ! grep -Fq "$expected" "$file"; then
    echo "Expected output to contain: $expected" >&2
    echo "Observed:" >&2
    sed 's/^/  /' "$file" >&2
    exit 1
  fi
}

dev_env() {
  bash "$root_dir/scripts/dev-env.sh" "$@"
}

write_manifest() {
  local name=$1 dir=$2 offset=$3
  local profile="dev-dev-env-test-$offset"
  mkdir -p "$MULTICA_DEV_HOME/envs/$name/logs"
  cat > "$MULTICA_DEV_HOME/envs/$name/manifest.env" <<EOF
NAME=$name
DIR=$(printf '%q' "$dir")
CREATED_AT=2026-01-01T00:00:00Z
OWNER=agent
TTL_HOURS=0
ENV_FILE=.env.example
OFFSET=$offset
BACKEND_PORT=$((18080 + offset))
FRONTEND_PORT=$((13000 + offset))
	DB_NAME=multica_dev_env_test_$offset
	DATABASE_URL=postgres://multica:multica@localhost:5432/multica_dev_env_test_$offset?sslmode=disable
	PROFILE=$profile
	WORKSPACES_ROOT=$(printf '%q' "$MULTICA_DEV_WORKSPACES_PARENT/multica_workspaces_$profile")
	DESKTOP_RENDERER_PORT=$((5174 + offset))
	DESKTOP_APP_SUFFIX=$name
EOF
}

# Like write_manifest, but records an explicit database name and writes the
# matching .env.worktree into the checkout, so destroy's cross-check between
# the registry and the env file the application runs from has both sources.
write_manifest_consistent() {
  local name=$1 dir=$2 offset=$3
  local db=${4:-multica_dev_env_test_$offset}
  local profile="dev-dev-env-test-$offset"
  mkdir -p "$dir"
  cat > "$dir/.env.worktree" <<EOF
POSTGRES_DB=$db
POSTGRES_USER=multica
DATABASE_URL=postgres://multica:multica@localhost:5432/$db?sslmode=disable
EOF
  mkdir -p "$MULTICA_DEV_HOME/envs/$name/logs"
  cat > "$MULTICA_DEV_HOME/envs/$name/manifest.env" <<EOF
NAME=$name
DIR=$(printf '%q' "$dir")
CREATED_AT=2026-01-01T00:00:00Z
OWNER=agent
TTL_HOURS=0
ENV_FILE=.env.worktree
OFFSET=$offset
BACKEND_PORT=$((18080 + offset))
FRONTEND_PORT=$((13000 + offset))
DB_NAME=$db
DATABASE_URL=postgres://multica:multica@localhost:5432/$db?sslmode=disable
PROFILE=$profile
WORKSPACES_ROOT=$(printf '%q' "$MULTICA_DEV_WORKSPACES_PARENT/multica_workspaces_$profile")
DESKTOP_RENDERER_PORT=$((5174 + offset))
DESKTOP_APP_SUFFIX=$name
EOF
}

out="$tmp_dir/out"

# ---------------------------------------------------------------------------
# An empty registry is a normal state, not an error.
# ---------------------------------------------------------------------------
dev_env list > "$out" 2>&1 || fail "list on an empty registry must succeed"
require_contains "$out" "No environments registered"

dev_env list --json > "$out" 2>&1 || fail "list --json on an empty registry must succeed"
if [ "$(cat "$out")" != "[]" ]; then
  fail "list --json on an empty registry = $(cat "$out"), want []"
fi

# ---------------------------------------------------------------------------
# Manifest serialization and user-provided names are safe. A manifest is
# sourced by Bash, so values must be shell-escaped and a name must never be
# able to walk outside envs/ before destroy eventually runs rm -rf.
# ---------------------------------------------------------------------------
quoted="$tmp_dir/quoted.env"
dangerous='a path with spaces;$(touch should-not-exist)'
bash -c 'source "$1"; write_manifest_value DIR "$2"' _ "$root_dir/scripts/dev-env.sh" "$dangerous" > "$quoted"
loaded="$(bash -c 'source "$1"; printf %s "$DIR"' _ "$quoted")"
[ "$loaded" = "$dangerous" ] || fail "manifest value did not round-trip safely"
[ ! -e "$root_dir/should-not-exist" ] || fail "loading a manifest executed its value"

status=0
dev_env up --name ../../escape > "$out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail "up accepted a path-traversing environment name"
require_contains "$out" "Invalid environment name"

status=0
dev_env up --ttl nope > "$out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail "up accepted a non-numeric TTL"
require_contains "$out" "TTL must be a positive integer"

# Reading the database name back out of a connection string must preserve the
# endpoint, credentials and query semantics — this is the value destroy
# cross-checks the registry and the env file against, so decoding subtleties
# (URL-encoded credentials, query strings) must not leak into the name.
extracted="$(bash -c 'source "$1"; database_name_from_url "$2"' _ \
  "$root_dir/scripts/dev-env.sh" \
  'postgres://dev:p%40ss@127.0.0.1:55432/old_db?sslmode=require&application_name=dev')"
[ "$extracted" = "old_db" ] \
  || fail "database name extraction got '$extracted', want old_db"

if bash -c 'source "$1"; database_name_from_url "mysql://dev@127.0.0.1:3306/nope" >/dev/null' \
  _ "$root_dir/scripts/dev-env.sh" 2>/dev/null; then
  fail "database name extraction accepted a non-PostgreSQL URL"
fi

# Falling back to Docker is valid only for the repository's canonical shared
# PostgreSQL endpoint. A local database on another port is an independent
# target; if psql cannot reach it, starting the shared Compose service would
# create the database in the wrong server (RUYI-90).
bash -c '
  source "$1"
  DATABASE_URL="postgres://multica:pw@localhost:5432/multica?sslmode=disable"
  database_url_uses_shared_postgres
' _ "$root_dir/scripts/dev-env.sh" \
  || fail "canonical shared PostgreSQL URL was not recognized"

bash -c '
  source "$1"
  DATABASE_URL="postgres://multica:pw@[::1]:5432/multica?sslmode=disable"
  database_url_uses_shared_postgres
' _ "$root_dir/scripts/dev-env.sh" \
  || fail "canonical IPv6 shared PostgreSQL URL was not recognized"

if bash -c '
  source "$1"
  DATABASE_URL="not-a-postgres-url"
  database_url_uses_shared_postgres
' _ "$root_dir/scripts/dev-env.sh"; then
  fail "invalid DATABASE_URL was mistaken for the shared service"
fi

if bash -c '
  source "$1"
  DATABASE_URL="postgres://multica:pw@127.0.0.1:15432/multica_test?sslmode=disable"
  database_url_uses_shared_postgres
' _ "$root_dir/scripts/dev-env.sh"; then
  fail "independent local PostgreSQL URL was mistaken for the shared service"
fi

status=0
bash -c '
  source "$1"
  psql() { return 1; }
  DATABASE_URL="postgres://multica:pw@127.0.0.1:15432/multica_test?sslmode=disable"
  POSTGRES_DB=multica_test
  POSTGRES_PORT=15432
  ENV_FILE=.env.worktree
  ensure_database
' _ "$root_dir/scripts/dev-env.sh" > "$out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail "unreachable independent PostgreSQL silently fell back to shared Docker"
require_contains "$out" "refusing to operate the shared Docker PostgreSQL instance"
require_contains "$out" "127.0.0.1:15432"

status=0
bash -c '
  source "$1"
  psql() { return 1; }
  fallback_log="$2"
  bash() { printf "%s\n" "$*" >"$fallback_log"; }
  DATABASE_URL="postgres://multica:secret@localhost:5432/multica?sslmode=disable"
  POSTGRES_DB=multica
  POSTGRES_PORT=5432
  ENV_FILE=.env.worktree
  ensure_database
' _ "$root_dir/scripts/dev-env.sh" "$tmp_dir/shared-fallback.log" > "$out" 2>&1 || status=$?
[ "$status" -eq 0 ] || fail "canonical shared PostgreSQL endpoint did not use the Docker fallback"
require_contains "$tmp_dir/shared-fallback.log" "$root_dir/scripts/ensure-postgres.sh .env.worktree"

DATABASE_URL="postgres://multica:secret@127.0.0.1:15432/multica_test?sslmode=disable" \
POSTGRES_PORT=15432 \
bash -c 'source "$1"; diagnose_database' _ "$root_dir/scripts/dev-env.sh" > "$out" 2>&1
require_contains "$out" "DATABASE_URL endpoint: 127.0.0.1:15432"
if grep -Fq "secret" "$out"; then
  fail "database diagnostics leaked DATABASE_URL credentials"
fi

# Slot reallocation moves ports and nothing else: under the shared-database
# model the env file keeps naming the database it has always named, so the
# registry, the env file and the running backend cannot drift apart.
mkdir -p "$tmp_dir/realloc"
cat > "$tmp_dir/realloc/.env.worktree" <<'EOF'
POSTGRES_DB=multica
POSTGRES_USER=multica
POSTGRES_PASSWORD=pw
POSTGRES_PORT=5432
DATABASE_URL=postgres://multica:pw@localhost:5432/multica?sslmode=disable
PORT=18123
FRONTEND_PORT=13123
FRONTEND_ORIGIN=http://localhost:13123
MULTICA_SERVER_URL=ws://localhost:18123/ws
MULTICA_PUBLIC_URL=http://localhost:18123
MULTICA_APP_URL=http://localhost:13123
NEXT_PUBLIC_API_URL=http://localhost:18123
NEXT_PUBLIC_WS_URL=ws://localhost:18123/ws
EOF
bash -c 'source "$1"; rewrite_env_ports "$2" 512 18512 13512' _ \
  "$root_dir/scripts/dev-env.sh" "$tmp_dir/realloc/.env.worktree"
grep -Fq 'PORT=18512' "$tmp_dir/realloc/.env.worktree" || fail "reallocation did not move the backend port"
grep -Fq 'FRONTEND_PORT=13512' "$tmp_dir/realloc/.env.worktree" || fail "reallocation did not move the frontend port"
grep -Fxq 'POSTGRES_DB=multica' "$tmp_dir/realloc/.env.worktree" \
  || fail "reallocation renamed POSTGRES_DB; the shared database name must stay stable"
grep -Fxq 'DATABASE_URL=postgres://multica:pw@localhost:5432/multica?sslmode=disable' "$tmp_dir/realloc/.env.worktree" \
  || fail "reallocation rewrote DATABASE_URL; only ports may move"

# ---------------------------------------------------------------------------
# A registered environment is visible to both renderings, and the JSON one
# parses — agents read it, so a stray log line in it is a broken contract.
# ---------------------------------------------------------------------------
write_manifest_consistent "probe-901" "$tmp_dir/checkout" 901

dev_env list > "$out" 2>&1 || fail "list must succeed with one environment"
require_contains "$out" "probe-901"
require_contains "$out" "18981"

dev_env status probe-901 --json > "$out" 2>&1 || fail "status --json must succeed"
node -e '
  const fs = require("fs");
  const payload = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
  if (payload.name !== "probe-901") throw new Error("name = " + payload.name);
  if (payload.backend_port !== 18981) throw new Error("backend_port = " + payload.backend_port);
  for (const key of ["api", "web", "daemon", "desktop"]) {
    if (!payload.components[key]) throw new Error("missing component " + key);
    if (payload.components[key].state !== "stopped") {
      throw new Error(key + " state = " + payload.components[key].state);
    }
  }
' "$out" || fail "status --json is not machine-readable"

# ---------------------------------------------------------------------------
# Stopping an environment that is not running is a no-op that SUCCEEDS.
#
# This is the regression that made `make down` exit 1 after reporting success:
# on bash 3.2 a command substitution whose function ends in a failing command
# aborts the whole script under `set -e`, and "no process is listening on this
# port" is that function's normal answer.
# ---------------------------------------------------------------------------
status=0
dev_env down probe-901 --components api,web > "$out" 2>&1 || status=$?
if [ "$status" -ne 0 ]; then
  echo "Observed:" >&2
  sed 's/^/  /' "$out" >&2
  fail "down on a stopped environment exited $status, want 0"
fi
require_contains "$out" "stopped"

# Commands launched through env-exec must not inherit the daemon-task identity
# hints that make human/profile CLI commands reject --profile.
write_manifest "clean-env-903" "$root_dir" 903
MULTICA_TASK_CONFIG_ROOT=/task/config \
MULTICA_TASK_WORKSPACES_ROOT=/task/workspaces \
MULTICA_WORKSPACES_ROOT=/owner/workspaces \
  dev_env exec clean-env-903 -- sh -c '
    test -z "${MULTICA_TASK_CONFIG_ROOT:-}" &&
    test -z "${MULTICA_TASK_WORKSPACES_ROOT:-}" &&
    test "$MULTICA_WORKSPACES_ROOT" = "$1"
  ' _ "$MULTICA_DEV_WORKSPACES_PARENT/multica_workspaces_dev-dev-env-test-903" \
  > "$out" 2>&1 || fail "env-exec leaked daemon task identity or owner workspaces root"

# A health response without process identity is never proof that the process is
# this checkout's freshly launched API.
if bash -c 'source "$1"; api_started_after '\''{"status":"ok"}'\'' 1' _ "$root_dir/scripts/dev-env.sh"; then
  fail "legacy /health without started_at was accepted as current"
fi

# ---------------------------------------------------------------------------
# Unknown names and components fail loudly instead of doing something else.
# ---------------------------------------------------------------------------
status=0
dev_env status no-such-env > "$out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail "status on an unknown environment must fail"
require_contains "$out" "Unknown environment"

status=0
dev_env up --components nope > "$out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail "up with an unknown component must fail"
require_contains "$out" "Unknown component"

# ---------------------------------------------------------------------------
# gc reports what it would collect and touches nothing in --dry-run. An
# environment whose checkout is gone has no owner left to stop it, which is how
# 152 databases accumulated with nothing on the machine able to list them.
# ---------------------------------------------------------------------------
write_manifest "orphan-902" "$tmp_dir/deleted-checkout" 902

dev_env gc --dry-run > "$out" 2>&1 || fail "gc --dry-run must succeed"
require_contains "$out" "orphan-902 would be collected"
if grep -Fq "probe-901 would be collected" "$out"; then
  fail "gc must not collect an environment whose directory still exists"
fi
[ -f "$MULTICA_DEV_HOME/envs/orphan-902/manifest.env" ] || fail "gc --dry-run deleted a manifest"

# A failed database drop keeps the manifest and slot so cleanup can be retried;
# destroy must never print success and forget the only deletion recipe.
write_manifest_consistent "drop-fails-904" "$tmp_dir/drop-fails-checkout" 904
status=0
FAIL_DROP=1 dev_env destroy drop-fails-904 --yes > "$out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail "destroy succeeded after DROP DATABASE failed"
[ -f "$MULTICA_DEV_HOME/envs/drop-fails-904/manifest.env" ] \
  || fail "destroy discarded the manifest after DROP DATABASE failed"
require_contains "$out" "manifest and slot were kept"
dev_env destroy drop-fails-904 --yes > "$out" 2>&1 || fail "retrying destroy after database recovery failed"

# ---------------------------------------------------------------------------
# destroy consumes the manifest: the slot is free afterwards, which is what
# makes the registry an allocator rather than a second place to leak.
# ---------------------------------------------------------------------------
dev_env destroy probe-901 --yes > "$out" 2>&1 || fail "destroy must succeed"
[ ! -d "$MULTICA_DEV_HOME/envs/probe-901" ] || fail "destroy left the environment directory behind"

dev_env list > "$out" 2>&1 || fail "list must succeed after destroy"
if grep -Fq "probe-901" "$out"; then
  fail "destroyed environment is still listed"
fi

# Declining the confirmation is a successful no-op, not a failure.
printf 'n\n' | dev_env destroy orphan-902 > "$out" 2>&1 || fail "declining destroy must exit 0"
require_contains "$out" "Cancelled."
[ -d "$MULTICA_DEV_HOME/envs/orphan-902" ] || fail "declined destroy removed the environment anyway"

# ---------------------------------------------------------------------------
# destroy's database drop is guarded (RUYI-66). It may only drop a database
# that the registry records AND the checkout's own env file names, and never
# the platform's shared main database `multica`: worktree environments
# legitimately record it (the shared-database model), and a stale registry
# once offered to drop the live platform database on cleanup.
# ---------------------------------------------------------------------------

# Sharing the main database is explicit: destroy releases slot, profile and
# manifest, reports clearly, and no DROP statement is ever issued.
write_manifest_consistent "shared-main-905" "$tmp_dir/shared-main-checkout" 905 "multica"
printf 'n\n' | dev_env destroy shared-main-905 > "$out" 2>&1 || fail "取消共享环境销毁失败"
require_contains "$out" "保留共享主库 multica"
if grep -Fq 'This drops database multica' "$out"; then
  fail "确认提示仍声称会删除受保护主库"
fi
: >"$psql_log"
dev_env destroy shared-main-905 --yes > "$out" 2>&1 || fail "destroying a shared-main environment must succeed"
if grep -Fq "DROP DATABASE" "$psql_log"; then
  fail "destroy issued a DROP for the shared main database: $(cat "$psql_log")"
fi
require_contains "$out" "shared main database"
[ ! -d "$MULTICA_DEV_HOME/envs/shared-main-905" ] \
  || fail "destroying a shared-main environment must still release the slot (self-healing)"

# The RUYI-59 QA incident: a stale registry names `multica` while the checkout
# was re-pointed at an isolated database by hand. The registry's name alone
# must never reach a drop statement.
write_manifest_consistent "stale-main-906" "$tmp_dir/stale-main-checkout" 906 "multica"
cat > "$tmp_dir/stale-main-checkout/.env.worktree" <<'EOF'
POSTGRES_DB=multica_stale_906
POSTGRES_USER=multica
DATABASE_URL=postgres://multica:multica@localhost:5432/multica_stale_906?sslmode=disable
EOF
: >"$psql_log"
dev_env destroy stale-main-906 --yes > "$out" 2>&1 || fail "destroying a stale-main registry entry must succeed"
if grep -Fq "DROP DATABASE" "$psql_log"; then
  fail "destroy dropped a database on a stale registry record: $(cat "$psql_log")"
fi
[ ! -d "$MULTICA_DEV_HOME/envs/stale-main-906" ] || fail "stale registry entry was not cleaned up"

# An isolated database is dropped only when the registry, the env file's
# POSTGRES_DB and its DATABASE_URL all name the same database.
write_manifest_consistent "isolated-907" "$tmp_dir/isolated-checkout" 907 "multica_isolated_907"
: >"$psql_log"
dev_env destroy isolated-907 --yes > "$out" 2>&1 || fail "destroying a consistent isolated environment must succeed"
require_contains "$psql_log" 'DROP DATABASE IF EXISTS "multica_isolated_907" WITH (FORCE)'
[ ! -d "$MULTICA_DEV_HOME/envs/isolated-907" ] || fail "consistent isolated destroy left the manifest behind"

# Registry and env file disagree: the drop is refused and the manifest kept,
# because with the checkout's config gone or diverged nothing on the machine
# can prove which database the application actually used.
write_manifest_consistent "mismatch-908" "$tmp_dir/mismatch-checkout" 908 "multica_manifest_908"
cat > "$tmp_dir/mismatch-checkout/.env.worktree" <<'EOF'
POSTGRES_DB=multica_envfile_908
POSTGRES_USER=multica
DATABASE_URL=postgres://multica:multica@localhost:5432/multica_envfile_908?sslmode=disable
EOF
: >"$psql_log"
status=0
dev_env destroy mismatch-908 --yes > "$out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail "destroy ignored a registry/env-file database mismatch"
if grep -Fq "DROP DATABASE" "$psql_log"; then
  fail "destroy dropped a database without registry/env-file agreement: $(cat "$psql_log")"
fi
[ -f "$MULTICA_DEV_HOME/envs/mismatch-908/manifest.env" ] \
  || fail "refused destroy discarded the only deletion recipe (manifest)"

# A checkout whose env file is gone has no second source at all.
write_manifest_consistent "no-envfile-909" "$tmp_dir/vanished-checkout" 909 "multica_vanished_909"
rm "$tmp_dir/vanished-checkout/.env.worktree"
: >"$psql_log"
status=0
dev_env destroy no-envfile-909 --yes > "$out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail "destroy dropped a database with no env file to confirm it"
if grep -Fq "DROP DATABASE" "$psql_log"; then
  fail "destroy dropped a database the env file could not confirm: $(cat "$psql_log")"
fi
[ -f "$MULTICA_DEV_HOME/envs/no-envfile-909/manifest.env" ] \
  || fail "unconfirmed destroy discarded the manifest"

echo "✓ dev-env.sh registry behaviour verified"
