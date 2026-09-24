# Progress

Status of each phase of SPEC.md §9. Updated at the end of every phase.

| Phase | Status |
|-------|--------|
| F0 Foundations | done |
| F1 Data model and blob store | done |
| F2 Sandbox + worker | done (cgroup v2 path: pending verification on real hardware) |
| F3 Task types, checkers, languages | done |
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

## F2 — Sandbox + worker (done)
Skipping skills: immersive-web-design, master skill.

- `internal/sandbox`: isolate wrapper (`--cg` or rlimit mode), meta-file
  parsing and verdict classification (OK, RE nonzero, RE signal, TLE CPU,
  TLE wall, MLE via cgroup OOM or peak at limit, OLE via SIGXFSZ, SE),
  unset streams forced to `/dev/null`, private `/dev/shm` for every run,
  output reading with `O_NOFOLLOW` + regular-file check, read-only `noexec`
  staging mount (`/stage`) for zero-copy inputs.
- Slots: one per physical core (hyperthread siblings skipped, first core
  reserved when ≥3), processes pinned with `sched_setaffinity`; 6 logical
  boxes per slot, each backed by 2 isolate boxes re-initialised in the
  background on the maintenance cores (fresh cgroup per run: peak memory and
  OOM counters never leak between runs). Trivial run end-to-end: ~5.5 ms.
- `internal/langs` (YAML language definitions, templates), `internal/jobs`
  (self-contained job/result protocol: workers never touch PostgreSQL),
  `internal/checkers` (exact, CMS white-diff, float tolerance, CMS custom
  checker protocol, testlib exit codes), `internal/tasktypes` (Batch with
  stdin/stdout or files, optional grader, built-in/custom/testlib checkers
  run in their own box, user tests), `internal/worker` executor with the
  verified LRU testcase cache and per-slot staging.
- `scripts/install-isolate.sh` (isolate v2.7 + cgroup keeper service) and a
  CI `sandbox` job running the battery as root on a cgroup v2 runner.

### Malicious battery (`TestMaliciousBattery`, two consecutive runs)
Environment: isolate 1.10.1 with cgroup v1 (see D7), 4 vCPU VM.
TL 1 s (0.5 s for sleep), 256 MiB, 16 MiB output, 1 process unless noted.

| Program | Run 1 | Run 2 | Extra assertion |
|---|---|---|---|
| control_ac (correct A+B) | ok (AC) | ok (AC) | outcome 1 |
| control_wa | ok (WA) | ok (WA) | outcome 0 |
| control_ce (syntax error) | CE | CE | |
| fork_bomb | TLE | TLE | no sandbox process survives |
| fork_bomb_64_procs | TLE | TLE | no sandbox process survives |
| read_passwd (/etc/passwd, /etc/shadow, /root, /home, box root, /proc/1/environ) | ok | ok | nothing readable (only the intended /etc/alternatives mount point is visible) |
| network (TCP to a host listener, 10.0.0.1, 1.1.1.1; UDP to 8.8.8.8) | ok | ok | all blocked, host listener got 0 connections |
| write_outside_box (/, /usr, /usr/bin, /etc, /box/.., ../, /proc/sys) | ok | ok | no host file created; private /tmp and /dev/shm not the host's |
| sleep_forever | TLE (wall) | TLE (wall) | CPU ≈ 0 |
| memory_hog (4 GiB) | MLE | MLE | |
| huge_output (2 GiB stdout) | OLE | OLE | |
| huge_stderr (2 GiB stderr) | ok (AC) | ok (AC) | stderr discarded to /dev/null, worker memory unaffected |
| `#include </dev/random>` | CE (memory) | CE (memory) | |
| `#include </dev/zero>` | CE (memory) | CE (memory) | |
| threads (16 busy threads, 1 process allowed) | RE (signal 6) | RE (signal 6) | |
| threads (64 processes allowed) | TLE (CPU summed over threads) | TLE | no process survives |
| kill_all (kill(1), kill(-1)) | ok | ok | nothing outside the pid namespace affected |
| stack_overflow | MLE | MLE | |
| privilege_escalation (setuid, chroot, mount) | ok | ok | all failed |

Bugs found and fixed by the battery: host `/dev/shm` writable with isolate
1.x (now a private tmp per run), contestant stderr inherited into the
worker's memory when not redirected (now `/dev/null`), cumulative cgroup
peak memory/OOM counters when reusing boxes (now a fresh box per run),
compilers unreachable through `/etc/alternatives` symlinks.

Pending verification on real hardware: isolate 2.x with cgroup v2 (this VM
only offers cgroup v1; the CI `sandbox` job runs the same battery on a
cgroup v2 GitHub runner); timing stability with turbo/hyperthreading
disabled.

## F3 — Task types, checkers and languages (done)
Skipping skills: immersive-web-design, master skill.

- Task types: Batch (stdin/stdout or files, optional grader), OutputOnly,
  TwoSteps (second step isolated in another box, only sees the message),
  Communication (manager + 1..4 contestant processes in separate boxes over
  per-process FIFO pairs, `fifos` or `std_io`, optional stub).
- Checkers: exact, white-diff (CMS semantics), float tolerance, CMS custom
  protocol, testlib exit codes; checker/manager sources (`checker.cpp`,
  `manager.cpp`) compiled once per worker in the sandbox.
- 12 languages as YAML files: C11, C++17, C++20, Java, Python 3, PyPy 3,
  Pascal, Rust, Go, Kotlin, C#, Haskell. Generic "compile seeds" (warmed,
  copied compiler caches): Go compile 5–10 s → 0.3 s.
- Docs: `docs/{en,es}/languages.md`, `docs/{en,es}/task-types.md`.

### Sample solution suite (`TestSampleSolutions`, two consecutive runs)
A+B in every language; TL 2 s, 256 MiB. Every row matched the expected
verdict (AC/WA/TLE/MLE/RE/CE) and both runs were identical:

| Solution | Run 1 | Run 2 |
|---|---|---|
| c11/ac | AC | AC |
| c11/ce | CE | CE |
| c11/mle | MLE | MLE |
| c11/re | RE | RE |
| c11/tle | TLE | TLE |
| c11/wa | WA | WA |
| cpp17/ac | AC | AC |
| cpp17/ce | CE | CE |
| cpp17/mle | MLE | MLE |
| cpp17/re | RE | RE |
| cpp17/tle | TLE | TLE |
| cpp17/wa | WA | WA |
| cpp20/ac | AC | AC |
| cpp20/ce | CE | CE |
| cpp20/mle | MLE | MLE |
| cpp20/re | RE | RE |
| cpp20/tle | TLE | TLE |
| cpp20/wa | WA | WA |
| csharp/ac | AC | AC |
| csharp/ce | CE | CE |
| csharp/mle | MLE | MLE |
| csharp/re | RE | RE |
| csharp/tle | TLE | TLE |
| csharp/wa | WA | WA |
| go/ac | AC | AC |
| go/ce | CE | CE |
| go/mle | MLE | MLE |
| go/re | RE | RE |
| go/tle | TLE | TLE |
| go/wa | WA | WA |
| haskell/ac | AC | AC |
| haskell/ce | CE | CE |
| haskell/mle | MLE | MLE |
| haskell/re | RE | RE |
| haskell/tle | TLE | TLE |
| haskell/wa | WA | WA |
| java/ac | AC | AC |
| java/ce | CE | CE |
| java/mle | MLE | MLE |
| java/re | RE | RE |
| java/tle | TLE | TLE |
| java/wa | WA | WA |
| kotlin/ac | AC | AC |
| kotlin/ce | CE | CE |
| kotlin/mle | MLE | MLE |
| kotlin/re | RE | RE |
| kotlin/tle | TLE | TLE |
| kotlin/wa | WA | WA |
| pascal/ac | AC | AC |
| pascal/ce | CE | CE |
| pascal/mle | MLE | MLE |
| pascal/re | RE | RE |
| pascal/tle | TLE | TLE |
| pascal/wa | WA | WA |
| pypy3/ac | AC | AC |
| pypy3/ce | CE | CE |
| pypy3/mle | MLE | MLE |
| pypy3/re | RE | RE |
| pypy3/tle | TLE | TLE |
| pypy3/wa | WA | WA |
| python3/ac | AC | AC |
| python3/ce | CE | CE |
| python3/mle | MLE | MLE |
| python3/re | RE | RE |
| python3/tle | TLE | TLE |
| python3/wa | WA | WA |
| rust/ac | AC | AC |
| rust/ce | CE | CE |
| rust/mle | MLE | MLE |
| rust/re | RE | RE |
| rust/tle | TLE | TLE |
| rust/wa | WA | WA |

Task-type tests (`TestBatchVariants`, `TestOutputOnly`, `TestTwoSteps`,
`TestCommunication`, `TestBatchUserTest`): graders in C++/Python/Java, file
I/O and missing output, CMS checker from source (full/partial), testlib
checker (ok/points), float tolerance, exact diff, OutputOnly with built-in
and custom checkers, TwoSteps isolation, Communication OK/WA/crash/TLE/
2 processes/std_io — all green. The malicious battery was re-run in the same
session (two runs, identical, see F2).
