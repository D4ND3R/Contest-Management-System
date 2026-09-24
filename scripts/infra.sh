#!/usr/bin/env bash
# Starts (idempotently) a throwaway PostgreSQL and Redis/Valkey pair for
# tests or native development and prints the matching environment exports.
#
#   scripts/infra.sh start <name> <pg_port> <redis_port>
#   scripts/infra.sh stop  <name>
#
# Native binaries are preferred (fast, no daemon needed); when they are
# missing the script falls back to docker containers.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
action=${1:?action}
name=${2:?name}
DIR="$ROOT/.cache/$name"
PGPORT=${3:-55432}
REDISPORT=${4:-56379}

pgbin() {
  if command -v pg_ctl >/dev/null 2>&1; then dirname "$(command -v pg_ctl)"; return; fi
  ls -d /usr/lib/postgresql/*/bin 2>/dev/null | sort -V | tail -1
}

as_pg() {
  # PostgreSQL refuses to run as root; use the postgres system user then.
  if [ "$(id -u)" = 0 ]; then runuser -u postgres -- "$@"; else "$@"; fi
}

redis_bin() { command -v valkey-server || command -v redis-server || true; }
redis_cli() { command -v valkey-cli || command -v redis-cli || true; }

start_native() {
  local bin; bin=$(pgbin)
  mkdir -p "$DIR"
  if [ "$(id -u)" = 0 ]; then chown postgres "$DIR" 2>/dev/null || true; fi
  if [ ! -f "$DIR/pg/PG_VERSION" ]; then
    as_pg mkdir -p "$DIR/pg"
    as_pg "$bin/initdb" -D "$DIR/pg" -U postgres --auth=trust -E UTF8 --no-locale >/dev/null
    cat >> "$DIR/pg/postgresql.conf" <<CONF
listen_addresses = '127.0.0.1'
port = $PGPORT
unix_socket_directories = '$DIR'
max_connections = 400
shared_buffers = 256MB
fsync = off
synchronous_commit = off
full_page_writes = off
CONF
  fi
  if ! as_pg "$bin/pg_ctl" -D "$DIR/pg" status >/dev/null 2>&1; then
    as_pg "$bin/pg_ctl" -D "$DIR/pg" -l "$DIR/pg.log" -w start >/dev/null
  fi
  local rcli; rcli=$(redis_cli)
  if ! "$rcli" -p "$REDISPORT" ping >/dev/null 2>&1; then
    "$(redis_bin)" --port "$REDISPORT" --bind 127.0.0.1 --daemonize yes --save "" --appendonly no \
      --dir "$DIR" --pidfile "$DIR/redis.pid" --logfile "$DIR/redis.log" >/dev/null
    for _ in $(seq 1 50); do "$rcli" -p "$REDISPORT" ping >/dev/null 2>&1 && break; sleep 0.1; done
  fi
}

start_docker() {
  docker run -d --rm --name "cms-$name-pg" -p "127.0.0.1:$PGPORT:5432" \
    -e POSTGRES_HOST_AUTH_METHOD=trust postgres:16-alpine \
    -c fsync=off -c synchronous_commit=off -c full_page_writes=off -c max_connections=400 >/dev/null 2>&1 || true
  docker run -d --rm --name "cms-$name-redis" -p "127.0.0.1:$REDISPORT:6379" valkey/valkey:8-alpine >/dev/null 2>&1 || true
  for _ in $(seq 1 100); do
    docker exec "cms-$name-pg" pg_isready -U postgres >/dev/null 2>&1 && break; sleep 0.2
  done
}

# S3 (MinIO) is optional: only started when docker is usable.
S3PORT=${5:-59000}
start_s3() {
  command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1 || return 1
  if ! docker ps --format '{{.Names}}' | grep -qx "cms-$name-s3"; then
    docker run -d --rm --name "cms-$name-s3" -p "127.0.0.1:$S3PORT:9000" \
      -e MINIO_ROOT_USER=minioadmin -e MINIO_ROOT_PASSWORD=minioadmin pgsty/minio:latest server /data >/dev/null 2>&1 || return 1
  fi
  for _ in $(seq 1 100); do
    curl -fsS "http://127.0.0.1:$S3PORT/minio/health/live" >/dev/null 2>&1 && return 0; sleep 0.2
  done
  return 1
}

case "$action" in
  start)
    if [ -n "$(pgbin)" ] && [ -n "$(redis_bin)" ]; then start_native; else start_docker; fi
    echo "export CMS_TEST_DATABASE_URL='postgres://postgres@127.0.0.1:$PGPORT/postgres?sslmode=disable'"
    echo "export CMS_TEST_REDIS_URL='redis://127.0.0.1:$REDISPORT/0'"
    if [ "${CMS_TEST_S3:-auto}" != "off" ] && start_s3; then
      echo "export CMS_TEST_S3_ENDPOINT='127.0.0.1:$S3PORT' CMS_TEST_S3_ACCESS_KEY=minioadmin CMS_TEST_S3_SECRET_KEY=minioadmin"
    fi
    ;;
  stop)
    bin=$(pgbin)
    if [ -n "$bin" ] && [ -f "$DIR/pg/PG_VERSION" ]; then as_pg "$bin/pg_ctl" -D "$DIR/pg" -m fast stop >/dev/null 2>&1 || true; fi
    if [ -f "$DIR/redis.pid" ]; then kill "$(cat "$DIR/redis.pid")" 2>/dev/null || true; fi
    docker rm -f "cms-$name-pg" "cms-$name-redis" "cms-$name-s3" >/dev/null 2>&1 || true
    ;;
  *) echo "unknown action $action" >&2; exit 2 ;;
esac
