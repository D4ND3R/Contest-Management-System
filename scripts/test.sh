#!/usr/bin/env bash
# Runs the Go test suite against real PostgreSQL and Redis. When
# CMS_TEST_DATABASE_URL / CMS_TEST_REDIS_URL are already set (CI service
# containers) they are used as-is; otherwise scripts/infra.sh starts them.
set -eo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"
if [ -z "${CMS_TEST_DATABASE_URL:-}" ] || [ -z "${CMS_TEST_REDIS_URL:-}" ]; then
  eval "$(scripts/infra.sh start testenv 55432 56379)"
fi
export CMS_TEST_DATABASE_URL CMS_TEST_REDIS_URL
export CMS_TEST_S3_ENDPOINT CMS_TEST_S3_ACCESS_KEY CMS_TEST_S3_SECRET_KEY
exec go test "$@"
