#!/usr/bin/env bash
# Make a local Redis available to the REDIS_TEST_URL-gated Go tests.
#
# usage:
#   ensure-redis.sh url    print a usable REDIS_TEST_URL on stdout; empty
#                          (with a stderr note) when none can be assembled,
#                          so the gated tests keep their visible skips
#   ensure-redis.sh down   remove the test Redis container this script owns
#
# Resolution order for `url`: an explicitly set REDIS_TEST_URL wins as-is
# (CI service containers and operator overrides); otherwise anything already
# listening on 127.0.0.1:6379 is reused; otherwise a throwaway
# `multica-test-redis` container is started (idempotently). Reachability is
# never fatal here — the gated tests verify the URL themselves and skip with
# a named reason when it does not answer.
set -euo pipefail

CONTAINER_NAME="multica-test-redis"
HOST_PORT=6379
DEFAULT_URL="redis://127.0.0.1:${HOST_PORT}/1"

port_open() {
  (exec 3<>"/dev/tcp/127.0.0.1/${HOST_PORT}") 2>/dev/null
}

container_healthy() {
  docker exec "$CONTAINER_NAME" redis-cli ping 2>/dev/null | grep -q PONG
}

ensure_container() {
  if container_healthy; then
    return 0
  fi
  if docker inspect "$CONTAINER_NAME" >/dev/null 2>&1; then
    docker start "$CONTAINER_NAME" >/dev/null
  else
    docker run -d --name "$CONTAINER_NAME" \
      -p "127.0.0.1:${HOST_PORT}:6379" \
      redis:7-alpine --save '' --appendonly no >/dev/null
  fi
  for _ in $(seq 1 30); do
    if container_healthy; then
      return 0
    fi
    sleep 0.5
  done
  echo "ensure-redis: $CONTAINER_NAME did not become healthy" >&2
  return 1
}

cmd_url() {
  if [ -n "${REDIS_TEST_URL:-}" ]; then
    echo "$REDIS_TEST_URL"
    return 0
  fi
  if port_open; then
    echo "$DEFAULT_URL"
    return 0
  fi
  if ! command -v docker >/dev/null 2>&1 || ! docker info >/dev/null 2>&1; then
    echo "ensure-redis: no docker daemon and nothing on 127.0.0.1:${HOST_PORT}; REDIS_TEST_URL-gated tests will be skipped" >&2
    return 0
  fi
  if ensure_container; then
    echo "$DEFAULT_URL"
  else
    echo "ensure-redis: failed to assemble a test Redis; REDIS_TEST_URL-gated tests will be skipped" >&2
  fi
}

cmd_down() {
  if docker inspect "$CONTAINER_NAME" >/dev/null 2>&1; then
    docker rm -f "$CONTAINER_NAME" >/dev/null
    echo "ensure-redis: removed $CONTAINER_NAME"
  else
    echo "ensure-redis: no $CONTAINER_NAME to remove"
  fi
}

case "${1:-}" in
  url) cmd_url ;;
  down) cmd_down ;;
  *)
    echo "usage: $0 {url|down}" >&2
    exit 2
    ;;
esac
