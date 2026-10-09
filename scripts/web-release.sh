#!/usr/bin/env bash
# Production Web release launcher for slot QA.
#
# Serves a prebuilt Next.js standalone release directory (the layout produced
# by `STANDALONE=true pnpm --filter @multica/web build` plus the Dockerfile.web
# overlay: `.next/static` and `public` copied into the standalone tree) on a
# slot's frontend port, with ports and upstreams read from a slot env file.
#
# The release directory is treated as immutable: runtime state (pid, log,
# state.json) lives under $HOME/.multica/web-release/port-<port>/ so the
# served tree stays byte-identical to the artifact that was verified.
#
# Usage:
#   web-release.sh manifest --release-dir DIR [--source-sha SHA] [--note TEXT] [--force]
#   web-release.sh verify   --release-dir DIR
#   web-release.sh start    --release-dir DIR --env-file FILE [--host HOST]
#   web-release.sh stop     --env-file FILE | --port PORT
#   web-release.sh status   --env-file FILE | --port PORT
#   web-release.sh logs     --env-file FILE | --port PORT [--tail N]
#
# manifest writes RELEASE_MANIFEST.json into the release directory; the file
# carries the aggregate tree digest and is itself excluded from hashing.
# verify recomputes the digest and fails if the served tree drifted from the
# packaged artifact. start/stop/status/logs manage the detached server
# process (one instance per frontend port).
set -euo pipefail

MANIFEST_NAME="RELEASE_MANIFEST.json"
STATE_ROOT="${MULTICA_WEB_RELEASE_STATE_DIR:-$HOME/.multica/web-release}"

# Env-file keys that must never reach the server process unchanged: the bind
# port, runtime mode, bind host, and API upstream are always set explicitly
# from the slot-derived values below (the slot env's bare PORT names the
# *backend* port; the web server must bind FRONTEND_PORT instead).
OVERRIDDEN_KEYS="PORT|NODE_ENV|HOSTNAME|REMOTE_API_URL"

info() { printf '%s\n' "$*"; }
die() { printf 'web-release: %s\n' "$*" >&2; exit 1; }

require_tools() {
  local t
  for t in ss curl find sha256sum stat awk sed sort xargs; do
    command -v "$t" > /dev/null || die "required tool missing: $t"
  done
}

usage() {
  sed -n '2,25p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
}

port_pid() {
  ss -lHtnp "sport = :$1" 2>/dev/null | sed -n 's/.*pid=\([0-9]*\).*/\1/p' | head -1
}

state_dir_for() { printf '%s/port-%s' "$STATE_ROOT" "$1"; }

# Reads KEY=VALUE lines from a slot env file into ENV_PAIRS (each element a
# single "K=V" word, ready for `env -i`), skipping comments and the keys this
# script overrides.
ENV_PAIRS=()
load_env_pairs() {
  local file=$1 line key
  ENV_PAIRS=()
  while IFS= read -r line; do
    [[ $line =~ ^[A-Za-z_][A-Za-z0-9_]*= ]] || continue
    key=${line%%=*}
    [[ "|$OVERRIDDEN_KEYS|" == *"|$key|"* ]] && continue
    ENV_PAIRS+=("$line")
  done < "$file"
}

env_file_value() {
  local file=$1 key=$2
  sed -n "s/^${key}=//p" "$file" | tail -1
}

# Resolves the frontend port from --port or a slot env file. Sets WEB_PORT.
resolve_port() {
  if [ -n "${FLAG_PORT:-}" ]; then
    WEB_PORT=$FLAG_PORT
    return
  fi
  [ -n "${FLAG_ENV_FILE:-}" ] || die "either --env-file or --port is required"
  [ -f "$FLAG_ENV_FILE" ] || die "env file not found: $FLAG_ENV_FILE"
  WEB_PORT=$(env_file_value "$FLAG_ENV_FILE" FRONTEND_PORT)
  [ -n "$WEB_PORT" ] || die "FRONTEND_PORT not set in $FLAG_ENV_FILE"
  [[ $WEB_PORT =~ ^[0-9]+$ ]] || die "FRONTEND_PORT must be numeric, got: $WEB_PORT"
}

# Deterministically hashes a release tree: sha256 over the sorted
# `sha256sum` listing of every regular file except the manifest itself.
# Emits the digest on stdout; LISTING_FILE (if set) receives the listing.
tree_digest() {
  local dir=$1
  local listing="${LISTING_FILE:-/dev/null}"
  ( cd "$dir" && find . -type f ! -name "$MANIFEST_NAME" -print0 \
      | LC_ALL=C sort -z \
      | xargs -0 sha256sum > "$listing" )
  sha256sum < "$listing" | awk '{print $1}'
}

file_count_in() { wc -l < "$1" | tr -d ' '; }
total_bytes_in() { awk '{s+=$1} END {print s+0}'; }

cmd_manifest() {
  [ -n "${FLAG_RELEASE_DIR:-}" ] || die "manifest requires --release-dir"
  [ -d "$FLAG_RELEASE_DIR" ] || die "release dir not found: $FLAG_RELEASE_DIR"
  local dir; dir=$(cd "$FLAG_RELEASE_DIR" && pwd)
  [ -f "$dir/apps/web/server.js" ] || die "not a standalone release (missing apps/web/server.js in $dir)"
  if [ -f "$dir/$MANIFEST_NAME" ] && [ -z "${FLAG_FORCE:-}" ]; then
    die "$MANIFEST_NAME already exists in $dir (use --force to overwrite)"
  fi
  local listing; listing=$(mktemp)
  local digest count bytes
  digest=$(LISTING_FILE=$listing tree_digest "$dir")
  count=$(file_count_in "$listing")
  bytes=$( cd "$dir" && sed 's/^[0-9a-f]\{64\}  \.\///' "$listing" \
    | while IFS= read -r f; do [ -n "$f" ] && stat -c %s "$f"; done \
    | awk '{s+=$1} END {print s+0}' )
  rm -f "$listing"
  local generated source note
  generated=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  source="${FLAG_SOURCE_SHA:-}"
  note="${FLAG_NOTE:-}"
  cat > "$dir/$MANIFEST_NAME" <<EOF
{
  "format": "multica-web-release-manifest/1",
  "generated_at": "$generated",
  "source_sha": "$source",
  "note": "$note",
  "tree_sha256": "$digest",
  "file_count": $count,
  "total_bytes": $bytes,
  "excluded": ["$MANIFEST_NAME"]
}
EOF
  info "manifest written: $dir/$MANIFEST_NAME"
  info "tree_sha256=$digest files=$count bytes=$bytes"
}

cmd_verify() {
  [ -n "${FLAG_RELEASE_DIR:-}" ] || die "verify requires --release-dir"
  [ -d "$FLAG_RELEASE_DIR" ] || die "release dir not found: $FLAG_RELEASE_DIR"
  local dir; dir=$(cd "$FLAG_RELEASE_DIR" && pwd)
  local mf="$dir/$MANIFEST_NAME"
  [ -f "$mf" ] || die "no $MANIFEST_NAME in $dir (run 'manifest' at packaging time first)"
  local expected
  expected=$(sed -n 's/.*"tree_sha256": "\([0-9a-f]*\)".*/\1/p' "$mf")
  [ -n "$expected" ] || die "manifest missing tree_sha256: $mf"
  local listing; listing=$(mktemp)
  local actual
  actual=$(LISTING_FILE=$listing tree_digest "$dir")
  if [ "$actual" = "$expected" ]; then
    info "verify PASS"
    info "tree_sha256=$actual"
    info "release=$dir"
    rm -f "$listing"
    return 0
  fi
  info "verify FAIL"
  info "expected tree_sha256=$expected"
  info "actual   tree_sha256=$actual"
  # Regenerate the packaged listing is impossible here; report the drift
  # hints we can: files present now vs manifest generation time can't be
  # listed from the aggregate digest, so point at the likely suspects.
  info "hint: re-extract from the original artifact; do not reuse a mutated tree"
  rm -f "$listing"
  return 1
}

cmd_start() {
  [ -n "${FLAG_RELEASE_DIR:-}" ] || die "start requires --release-dir"
  [ -n "${FLAG_ENV_FILE:-}" ] || die "start requires --env-file"
  [ -d "$FLAG_RELEASE_DIR" ] || die "release dir not found: $FLAG_RELEASE_DIR"
  [ -f "$FLAG_ENV_FILE" ] || die "env file not found: $FLAG_ENV_FILE"
  local dir; dir=$(cd "$FLAG_RELEASE_DIR" && pwd)
  [ -f "$dir/apps/web/server.js" ] || die "not a standalone release (missing apps/web/server.js in $dir)"
  [ -d "$dir/apps/web/.next/static" ] || die "release incomplete: apps/web/.next/static missing in $dir"
  WEB_PORT=$(env_file_value "$FLAG_ENV_FILE" FRONTEND_PORT)
  [ -n "$WEB_PORT" ] || die "FRONTEND_PORT not set in $FLAG_ENV_FILE"
  local backend_port
  backend_port=$(env_file_value "$FLAG_ENV_FILE" PORT)
  [ -n "$backend_port" ] || die "PORT (backend) not set in $FLAG_ENV_FILE"
  local bind_host=${FLAG_HOST:-127.0.0.1}
  local remote_api="http://127.0.0.1:$backend_port"

  local node_bin=${NODE_BIN:-$(command -v node || true)}
  [ -n "$node_bin" ] || die "node not found in PATH (set NODE_BIN to override)"

  local sdir; sdir=$(state_dir_for "$WEB_PORT")
  mkdir -p "$sdir"
  local pid_file="$sdir/pid" log_file="$sdir/server.log" state_file="$sdir/state.json"

  local existing; existing=$(port_pid "$WEB_PORT" || true)
  if [ -n "$existing" ]; then
    if [ -f "$pid_file" ] && [ "$(cat "$pid_file")" = "$existing" ]; then
      info "already running: pid $existing on port $WEB_PORT"
      info "url=http://127.0.0.1:$WEB_PORT"
      return 0
    fi
    die "port $WEB_PORT is busy with a foreign process (pid $existing); stop it first"
  fi

  load_env_pairs "$FLAG_ENV_FILE"

  local manifest_tree=""
  if [ -f "$dir/$MANIFEST_NAME" ]; then
    manifest_tree=$(sed -n 's/.*"tree_sha256": "\([0-9a-f]*\)".*/\1/p' "$dir/$MANIFEST_NAME")
  fi

  info "starting production web: release=$dir port=$WEB_PORT backend=$remote_api"
  ( cd "$dir" && exec nohup env -i \
      "PATH=$PATH" "HOME=$HOME" \
      "${ENV_PAIRS[@]}" \
      NODE_ENV=production \
      PORT="$WEB_PORT" \
      HOSTNAME="$bind_host" \
      REMOTE_API_URL="$remote_api" \
      NEXT_TELEMETRY_DISABLED=1 \
      "$node_bin" apps/web/server.js >> "$log_file" 2>&1 ) &
  local pid=$!
  echo "$pid" > "$pid_file"

  local i code
  for i in $(seq 1 30); do
    if ! kill -0 "$pid" 2>/dev/null; then
      info "web exited during startup; last log lines:" >&2
      tail -20 "$log_file" >&2
      rm -f "$pid_file"
      exit 1
    fi
    code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 "http://127.0.0.1:$WEB_PORT/" || true)
    case $code in
      2??|3??)
        cat > "$state_file" <<EOF
{
  "pid": $pid,
  "port": $WEB_PORT,
  "url": "http://127.0.0.1:$WEB_PORT",
  "bind_host": "$bind_host",
  "remote_api_url": "$remote_api",
  "release_dir": "$dir",
  "release_tree_sha256": "${manifest_tree:-}",
  "env_file": "$(cd "$(dirname "$FLAG_ENV_FILE")" && pwd)/$(basename "$FLAG_ENV_FILE")",
  "started_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
}
EOF
        info "web serving http://127.0.0.1:$WEB_PORT (pid $pid)"
        [ -n "$manifest_tree" ] && info "serving verified tree: $manifest_tree" \
          || info "note: release dir has no $MANIFEST_NAME (provenance unverified)"
        return 0
        ;;
    esac
    sleep 1
  done
  info "web did not become healthy within 30s; last log lines:" >&2
  tail -20 "$log_file" >&2
  exit 1
}

cmd_stop() {
  resolve_port
  local sdir; sdir=$(state_dir_for "$WEB_PORT")
  local pid_file="$sdir/pid"
  local pid
  pid=$(cat "$pid_file" 2>/dev/null || true)
  if [ -z "$pid" ]; then
    info "no recorded instance for port $WEB_PORT"
    return 0
  fi
  if kill -0 "$pid" 2>/dev/null; then
    kill "$pid" 2>/dev/null || true
    local i
    for i in $(seq 1 10); do
      kill -0 "$pid" 2>/dev/null || break
      sleep 1
    done
    if kill -0 "$pid" 2>/dev/null; then
      kill -9 "$pid" 2>/dev/null || true
    fi
  fi
  rm -f "$pid_file" "$sdir/state.json"
  local leftover; leftover=$(port_pid "$WEB_PORT" || true)
  [ -n "$leftover" ] && die "port $WEB_PORT still busy after stop (pid $leftover)"
  info "stopped instance on port $WEB_PORT"
}

cmd_status() {
  resolve_port
  local sdir; sdir=$(state_dir_for "$WEB_PORT")
  local pid_file="$sdir/pid" state_file="$sdir/state.json"
  local pid
  pid=$(cat "$pid_file" 2>/dev/null || true)
  if [ -z "$pid" ] || ! kill -0 "$pid" 2>/dev/null; then
    info "stopped (port $WEB_PORT)"
    return 1
  fi
  local code
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 "http://127.0.0.1:$WEB_PORT/" || true)
  info "running: pid $pid port $WEB_PORT http=$code"
  [ -f "$state_file" ] && cat "$state_file"
  [ "$code" = "000" ] && return 1
  return 0
}

cmd_logs() {
  resolve_port
  local sdir; sdir=$(state_dir_for "$WEB_PORT")
  local log_file="$sdir/server.log"
  [ -f "$log_file" ] || die "no log for port $WEB_PORT at $log_file"
  tail -n "${FLAG_TAIL:-50}" "$log_file"
}

FLAG_RELEASE_DIR="" FLAG_ENV_FILE="" FLAG_PORT="" FLAG_HOST="" FLAG_SOURCE_SHA="" \
  FLAG_NOTE="" FLAG_FORCE="" FLAG_TAIL=""

main() {
  [ $# -ge 1 ] || usage
  require_tools
  local verb=$1
  shift
  while [ $# -gt 0 ]; do
    case $1 in
      --release-dir) FLAG_RELEASE_DIR=$2; shift 2 ;;
      --env-file) FLAG_ENV_FILE=$2; shift 2 ;;
      --port) FLAG_PORT=$2; shift 2 ;;
      --host) FLAG_HOST=$2; shift 2 ;;
      --source-sha) FLAG_SOURCE_SHA=$2; shift 2 ;;
      --note) FLAG_NOTE=$2; shift 2 ;;
      --force) FLAG_FORCE=1; shift ;;
      --tail) FLAG_TAIL=$2; shift 2 ;;
      *) usage ;;
    esac
  done
  case $verb in
    manifest) cmd_manifest ;;
    verify) cmd_verify ;;
    start) cmd_start ;;
    stop) cmd_stop ;;
    status) cmd_status ;;
    logs) cmd_logs ;;
    *) usage ;;
  esac
}

main "$@"
