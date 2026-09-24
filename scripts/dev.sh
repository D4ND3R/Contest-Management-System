#!/usr/bin/env bash
# Native development stack without docker: PostgreSQL + Redis from
# scripts/infra.sh and every CMS service as a background process.
# Logs go to .cache/dev/logs; Ctrl-C stops everything.
#
# The worker needs root and isolate; it is skipped otherwise.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"
eval "$(scripts/infra.sh start dev 55433 56380)"
export CMS_CONFIG="$ROOT/config/dev.yaml"
mkdir -p .cache/dev/logs
go build -o bin/ ./cmd/cms ./cmd/cmsctl

psql "$CMS_TEST_DATABASE_URL" -tAc "SELECT 1 FROM pg_database WHERE datname='cms'" | grep -q 1 \
  || psql "$CMS_TEST_DATABASE_URL" -qc "CREATE DATABASE cms"
bin/cmsctl bootstrap -admin-password admin

services=(contest-web admin-web ranking-web dispatcher monitor printing)
if [ "$(id -u)" = 0 ] && command -v isolate >/dev/null 2>&1; then
  services+=(worker)
else
  echo "dev.sh: skipping worker (needs root and isolate)" >&2
fi

pids=()
cleanup() { kill "${pids[@]}" 2>/dev/null || true; wait 2>/dev/null || true; }
trap cleanup EXIT INT TERM
for s in "${services[@]}"; do
  bin/cms "$s" >".cache/dev/logs/$s.log" 2>&1 &
  pids+=($!)
done

declare -A health=(
  [contest-web]=8888 [admin-web]=8889 [ranking-web]=8890
  [dispatcher]=9101 [worker]=9102 [monitor]=9103 [printing]=9104
)
for s in "${services[@]}"; do
  ok=
  for _ in $(seq 1 50); do
    if bin/cmsctl healthcheck "http://127.0.0.1:${health[$s]}/healthz" 2>/dev/null; then ok=1; break; fi
    sleep 0.2
  done
  if [ -n "$ok" ]; then echo "  $s: healthy"; else echo "  $s: NOT healthy (see .cache/dev/logs/$s.log)"; exit 1; fi
done
echo "CMS is up: contest http://localhost:8888  admin http://localhost:8889 (admin/admin)  ranking http://localhost:8890"
if [ "${1:-}" = "--check" ]; then exit 0; fi
wait
