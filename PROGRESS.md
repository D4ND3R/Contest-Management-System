# Progress

Status of each phase of SPEC.md §9. Updated at the end of every phase.

| Phase | Status |
|-------|--------|
| F0 Foundations | done |
| F1 Data model and blob store | pending |
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
