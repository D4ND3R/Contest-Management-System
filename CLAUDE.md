# CLAUDE.md — working on this repository

## Session rules
- Do NOT use the `immersive-web-design` or `master skill` skills, even if a task
  mentions landing pages, animations, glass effects, hero sections, etc. The UI
  is functional, sober and ultra-light (server-rendered HTML + htmx + plain CSS).
- `SPEC.md` is the source of truth for requirements. Re-read it and
  `PROGRESS.md` at the start of every phase.
- Record every non-obvious decision in `DECISIONS.md` (numbered, with the
  rationale); update `PROGRESS.md` when a phase ends.
- Reference implementation: https://github.com/cms-dev/cms (AGPL-3.0). Use it
  only for architecture/behaviour. Never copy its code.
- Code, identifiers and commit messages in English; user documentation in
  Spanish and English (`docs/es`, `docs/en`).

## Layout
- `cmd/cms` — single binary, one subcommand per service
  (`contest-web`, `admin-web`, `ranking-web`, `dispatcher`, `worker`,
  `monitor`, `printing`, `ctl`, `migrate`). `cmd/cmsctl` = `cms ctl`.
- `internal/config` YAML config + `CMS_*` env overrides.
- `internal/db` pgx pool, embedded forward-only migrations
  (`internal/db/migrations`), sqlc queries (`internal/db/queries/*.sql` →
  `internal/db/sqlc`, regenerate with `make generate`).
- `internal/blob` content-addressed (SHA-256) blob store: local / S3, LRU cache.
- `internal/queue` Redis Streams job queues (priorities, ack, retries).
- `internal/sandbox` isolate wrapper; `internal/worker` job execution.
- `internal/tasktypes`, `internal/checkers`, `internal/langs`, `internal/scoring`.
- `internal/dispatcher`, `internal/monitor`, `internal/printing`.
- `internal/contestweb` (CWS), `internal/adminweb` (AWS), `internal/rankingweb` (RWS).
- `internal/cli` cmsctl commands; `internal/importer`, `internal/dump`.
- `config/languages/*.yaml` — one file per language; adding a language needs no code.
- `web/` templates and static assets (embedded).
- `deploy/` docker + systemd; `docs/` user/operator docs; `loadtest/` k6.

## Commands
- `make build` — binaries in `bin/`.
- `make test` — full suite; starts throwaway PostgreSQL/Redis (native binaries
  or docker) via `scripts/infra.sh`. Must be green before every commit.
- `make test-sandbox` — malicious battery + sample solutions (root + isolate).
- `make lint` — gofmt + go vet. `make generate` — sqlc.
- `make dev` — docker compose stack; `make dev-native` — native processes.

## Conventions
- Performance is priority #1, judge correctness/security #2.
  No N+1 queries; every index has a comment justifying it; profile (pprof)
  before optimizing; benchmarks for hot paths (`make bench`).
- Explicit SQL (sqlc or hand-written pgx for dynamic filters). No ORM.
- Never store large blobs in PostgreSQL — store the SHA-256 digest.
- All services must survive restarts without losing work: durable queues,
  explicit ACK, idempotent result handling, retries with backoff.
- Logging with `log/slog` (JSON); metrics with Prometheus; `/healthz` everywhere.
- Web: no inline JS, strict CSP, CSRF tokens on every POST, HttpOnly +
  SameSite cookies, request size limits, rate limits.
- Tests: `testutil.DB(t)` gives an isolated migrated database;
  `testutil.Redis(t)` an isolated key namespace.
