# Progress

Status of each phase of SPEC.md §9. Updated at the end of every phase.

| Phase | Status |
|-------|--------|
| F0 Foundations | done |
| F1 Data model and blob store | done |
| F2 Sandbox + worker | pending |
| F3 Task types, checkers, languages | pending |
| F4 Dispatcher | pending |
| F5 CWS | pending |
| F6 AWS | pending |
| F7 RWS | pending |
| F8 Tokens, limits, user tests, Q&A, printing, analysis, ICPC | pending |
| F9 Import/export | pending |
| F10 Performance and security | pending |
| F11 Deployment | pending |

## F0 — Foundations (done)
- Monorepo: single `cms` binary with one subcommand per service + `cmsctl`
  (`cmd/`, `internal/cli`). Services are stubs exposing `/healthz` and
  `/metrics` until their phase replaces them.
- `internal/config` (YAML + `CMS_*` env overrides, strict keys, validation),
  `internal/logging` (slog JSON/text), `internal/metrics` (Prometheus registry),
  `internal/httpx` (graceful server, health checks, observe middleware with
  latency histograms), `internal/app` (signals, run group),
  `internal/db` (pgx pool + embedded forward-only migration runner),
  `internal/redisx`, `internal/deps`, `internal/testutil` (per-test database
  cloned from a migrated template, per-test Redis namespace).
- `Makefile` (build, test, lint, dev, dev-native, bench, generate, ...),
  `scripts/infra.sh` (throwaway native PostgreSQL/Redis, docker fallback),
  `scripts/test.sh`, `scripts/dev.sh`.
- `Dockerfile` (targets `cms` and `worker` with isolate v2.7 built from
  source), `docker-compose.yml` (postgres 16, valkey 8, minio + every service
  with health checks), worker entrypoint that delegates a cgroup v2 subtree
  to isolate (falls back to no-cgroup mode on cgroup v1 hosts).
- CI: `.github/workflows/ci.yml` (lint+build, tests with -race against
  service containers, docker image build).
- `CLAUDE.md`, `README.md`, `DECISIONS.md` (D1–D8).

Verification in this environment:
- `make test` green; `make lint` green.
- `make dev`: all 10 containers healthy (`docker compose up --build --wait`).
- `make dev-native` (`scripts/dev.sh --check`): all 7 services healthy.
- isolate 2.7 inside the worker container runs programs; `--cg` is not
  available because this VM's host uses cgroup v1 (see D7).

## F1 — Data model and blob store (done)
- Schema `internal/db/migrations/0001_schema.sql`: every entity of SPEC §5
  (contests, tasks, statements, attachments, datasets, managers, testcases,
  languages, users, teams, participations, submissions, submission files,
  tokens, submission results, executables, evaluations, user tests + files +
  results + executables, questions, announcements, messages, print jobs,
  admins with roles, audit log) plus `blobs` (GC bookkeeping) and
  `participation_task_scores` (ranking aggregate). CHECK constraints for
  enums/ranges, a `sha256_digest` domain, cascading FKs, and every index
  documented with the query it serves (enforced by a test).
- `generation` column on results: stale job results are discarded, which
  makes queue retries idempotent (used from F4 on).
- sqlc (`internal/db/sqlc.yaml`, `internal/db/queries/*.sql` →
  `internal/db/sqlc`), `db.InTx`, default/update-param helpers.
- `internal/blob`: content-addressed store (SHA-256), backends Local
  (atomic temp+rename, optional fsync) and S3 (minio-go; hashes before
  upload), `Cache` (verified read-through LRU with singleflight, read-only
  files, reindex on restart), `Tracked` (registers blobs in the DB), `GC`
  (`cmsctl blobs-gc`), in-memory store for tests.
- `internal/auth`: argon2id password hashing (PHC format, OWASP params).
- `cmsctl bootstrap` now creates the first admin.

Verification: `make test` green — repository tests for every table
(constraints, cascades, idempotent upserts, generation checks, concurrent
print-job claims with SKIP LOCKED, keyset pagination), blob tests (contract
for Local/Mem/Cache/S3 on a real MinIO, 16 concurrent writers of the same
content → one object, LRU eviction, corruption detection, restart reindex),
tracked dedup (one `blobs` row) and GC with grace period.
