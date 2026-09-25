# Progress

Status of each phase of SPEC.md §9. Updated at the end of every phase.

| Phase | Status |
|-------|--------|
| F0 Foundations | done |
| F1 Data model and blob store | done |
| F2 Sandbox + worker | done (cgroup v2 path: pending verification on real hardware) |
| F3 Task types, checkers, languages | done |
| F4 Dispatcher | done |
| F5 CWS | done (3000-user target: F10) |
| F6 AWS | done |
| F7 RWS | pending |
| F8 Tokens, limits, user tests, Q&A, printing, analysis, ICPC | pending |
| F9 Import/export | pending |
| F10 Performance and security | pending |
| F11 Deployment | pending |
| Audit (SPEC_AUDIT.md) | in progress — see AUDIT.md |

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

## F4 — Dispatcher, queues, scoring, reevaluation, monitor (done)
Skipping skills: immersive-web-design, master skill.

- `internal/queue`: Redis Streams with consumer groups; four job streams
  served strictly by priority (compile > evaluate > user tests >
  background); result + ack in one MULTI/EXEC; dispatcher events stream;
  ranking-updates stream (capped); worker heartbeats (TTL) and registry;
  `Requeue` (copy re-added before the original is acked); leadership lease
  (Lua renew/release) so dispatcher and monitor can run as HA replicas;
  `AdoptPending` hands a dead replica's unacknowledged work to the new
  leader. Round trip enqueue → prioritised read → result+ack ≈ 0.22 ms.
- `internal/scoring`: Sum, GroupMin, GroupMul, GroupThreshold (subtasks by
  count, regex or list; CMS array or object params), public/private scores
  (a subtask is public when all its testcases are), score precision; score
  modes max, max_subtask, max_tokened_last; ICPC aggregation (binary verdict,
  attempts, penalty minutes).
- `internal/dispatcher`: per-result transactions with `SELECT … FOR UPDATE`
  and generation checks (duplicates/stale results are no-ops); parallel
  result processing sharded by submission; compile → evaluate (testcases
  spread over workers, `testcases_per_job`) → score → task aggregate →
  ranking update + SSE event; retries with the attempt counter, system error
  + admin alert after `max_attempts`; user tests; autojudged background
  datasets; live-dataset switch (re-aggregation + judging); sweeper that
  re-derives lost/invalidated work from the database
  (`jobs_enqueued_at`, migration 0002).
- Reevaluation API and `cmsctl reevaluate`: recompile / reevaluate / rescore
  by contest, task, dataset, participation, user or submission, in batches,
  rejudges on the background queue (no downtime); `cmsctl set-live-dataset`,
  `cmsctl status`.
- `internal/monitor`: requeues jobs of dead workers (heartbeat expired) and
  stuck jobs (job timeout), reports exhausted jobs as errors, publishes
  queue/worker stats (Redis + Prometheus).
- `cms worker` (one consumer loop per slot, graceful shutdown finishing the
  current job), `cms dispatcher`, `cms monitor` are now real services.
- `internal/events`: Redis pub/sub for live notifications (used by CWS SSE).

Verification (`make test`, real PostgreSQL + Redis + isolate):
end-to-end AC/WA/CE scoring with details, events and ranking updates;
max_subtask across submissions; the three reevaluation levels (generation
and compilation counters checked); live dataset switch; autojudged
background dataset; retries → system error + alert; sweeper recovering a
lost notification; user tests; dispatcher restart with a different consumer
(adopts pending results); **F4 exit: `TestKillWorkerMidEvaluation`** — a
`cms worker` process is SIGKILLed while evaluating (8 slow testcases); the
monitor requeues its job, a second worker process finishes, the submission
scores 100 with exactly 8 evaluations (victim 1, rescuer 7).

## F5 — Contestant web server (CWS) with SSE (done)
Skipping skills: immersive-web-design, master skill.

- `internal/webkit`: HMAC-signed stateless session cookies (HttpOnly,
  SameSite=Lax, Secure behind TLS), CSRF tokens bound to the session plus an
  Origin/Referer check, strict CSP (`script-src 'self'`, no inline code or
  styles anywhere), security headers, trusted-proxy aware client IP, Redis
  fixed-window rate limiter, content-hashed static assets served
  precompressed with `immutable` caching, pooled template rendering.
- `internal/i18n`: English message keys with a Spanish catalog (UI, judge
  messages, limit errors); language from cookie → user preference →
  `Accept-Language`; `/lang` switcher.
- `internal/contest`: contest phase computation (start/stop, per-user
  window with `per_user_time`, delay and extra time, analysis mode,
  unrestricted users) and submission/user-test limits (total and per task,
  minimum interval), with tests for the edge cases.
- `internal/contestweb` (`cms contest-web`): contest list, login (rate
  limited, constant time for unknown users, hidden/IP-restricted users),
  autologin by IP, single-login (nonce bumped on each login), logout,
  overview with per-task scores and timing, "start" for per-user windows,
  task pages (statements served with a sandboxing CSP, attachments,
  limits, submission form with per-file language placeholders), submit
  (size/filename/language validation, limits, blobs stored before the
  database row, dispatcher notified), submission list and details
  (public vs tokened scores, restricted feedback, compilation output,
  per-subtask tables, source download when allowed), documentation page
  (languages and exact compiler commands).
- Live updates: one Redis pub/sub subscription per process fans out to an
  in-memory SSE hub; the browser (`app.js`, 2 KB) swaps only the affected
  submission row via htmx. Heartbeats every 25 s, `retry: 3000`.
- Contest/task/participation views are cached for 3 s in-process (the hot
  path of a page view is one small query for the contestant's own data).
- Frontend budget: htmx 2.0.7 + app.js = **17.7 KB gzip** (limit 30 KB),
  one CSS file, no SPA, no inline code.

Verification (`make test`):
`internal/contestweb` (11 tests: login and pages, Spanish UI, submit flow,
public score display, single login, IP restriction + autologin, per-user
start, SSE delivery, no inline code + security headers, all template
strings translated, JS budget) and `internal/e2e`:
- **`TestSubmissionFlow` (F5 functional exit)**: contestant logs in over
  HTTP, submits a C solution through the form, receives `compiling` →
  `evaluating` → `scored` over SSE (**138 ms** from POST to `scored` with the
  real dispatcher + isolate worker), sees "Evaluated 25 / 100" (public
  score) and correct details, and the ranking aggregate holds 100.
- **`TestLightLoadLatency` (F5 performance exit)**: 50 concurrent logged-in
  contestants browsing overview/task/submissions/documentation for 5 s with
  20 ms think time — 11,485 requests, **p50 0.95 ms, p95 4.1 ms, p99 8.0 ms**
  (target p95 < 15 ms). The bound is enforced by `make test-e2e`, which
  runs the package alone; inside `go test ./...` (packages in parallel with
  the worker's compilation-heavy tests, e.g. on 2-4 vCPU CI runners) the
  latencies are only reported.
- The 3000-contestant target (p95 < 15 ms, p99 < 40 ms on 4 vCPU / 8 GB) is
  measured in F10 with k6; **pendiente de verificar en hardware real**.

## F6 — Admin web server (AWS) (done)
Skipping skills: immersive-web-design, master skill.

- `internal/adminweb` (`cms admin-web`): administrator login (argon2id,
  rate limited, constant time for unknown names), signed session cookies
  bound to the admin's password hash and role (changing either logs every
  session out), CSRF on every POST, strict CSP, roles enforced per route:
  `all` (everything), `messaging` (read + communication), `read_only`.
- Audit log of every mutating request, written by the routing middleware
  after a successful response (action, target, sanitized form values and
  uploaded file names — never passwords or CSRF tokens —, client IP) plus
  logins, failed logins and logouts; `/audit` with filters and paging.
- Contests: list with phase and counters, create/edit every setting
  (timezone-aware times, languages, localizations, per-user time, analysis
  mode, access, tokens, limits, IOI/ICPC, freeze time, printing), delete
  with typed confirmation, task order (move up/down), add/remove tasks.
- Tasks: create (with a live default dataset), edit (submission format,
  primary statements, score mode, feedback, precision, tokens, limits),
  statements per language, attachments, datasets (create, clone, edit
  limits and task/score type parameters validated server-side, make live →
  dispatcher re-aggregates and judges, delete non-live), managers upload,
  testcases (single upload, zip archive with `*.in`/`*.out` templates, zip
  bomb guard, public toggle, download), maximum score and missing-manager
  hints.
- Users: search/paging, create/edit/delete, password reset, CSV import
  (header row, any column order, atomic: nothing is imported when a row is
  invalid, optional update of existing users, generated passwords shown
  once as a table and CSV, parallel argon2id hashing, participations with
  team/IP/hidden/unrestricted/delay/extra), teams with flag and photo.
- Participations: bulk add by username, edit team, IPs, delay, extra time,
  hidden, unrestricted, contest-specific password, reset start, log out
  everywhere.
- Submissions: filters (task, user, status, language, score range),
  keyset paging, detail with sources, compilation output and per-dataset
  per-testcase evaluations, source download, line diff between two
  submissions (Myers), reevaluation (recompile / reevaluate / rescore) by
  submission, participation, user, task, dataset or contest.
- Live status: workers (slots, current job, jobs/errors) and queues
  (waiting/running per priority, results backlog) refreshed every 2 s;
  system errors on the overview; alerts and new questions pushed to the
  admin pages over SSE.
- Ranking view and exports (CSV/JSON, optional hidden users) from the
  shared `internal/ranking` package (IOI totals with shared ranks, ICPC
  solved/penalty); per-task statistics (counters, score histogram, testcase
  verdict distribution with times and memory).
- Administrators: create/edit/disable/delete, cannot delete oneself or
  remove the last enabled `all` administrator.

Verification (`make test`): `internal/adminweb` (login/roles/CSRF/audit,
every page renders without inline code, downloads, task/dataset/testcase
management incl. zip import and dataset switch, CSV import with
generated passwords and participations, reevaluation and open-redirect
guard, administrator safety, diff and template unit tests),
`internal/ranking` (IOI ties + hidden, ICPC order/penalty, CSV) and
**`internal/e2e.TestSetUpContestFromAdminUI` (F6 exit)**: from an empty
system with one bootstrap administrator, a contest, task, statement,
dataset limits and scoring, 4 testcases (zip), team and two users (CSV,
generated passwords) are created only through the admin web; the contestant
logs into the CWS with the generated password, submits, and is judged by
the real dispatcher + isolate worker (100/100); the admin submission list,
ranking CSV, statistics and audit log reflect it.

## Audit §2 — Problem types (done)
Skipping skills: immersive-web-design, master skill.

Interactive task type (interactor and contestant in separate sandboxes
over anonymous pipes, testlib verdicts, interactor time not charged),
Communication with per-process or summed limits and stubs in several
languages, output-only zip / partial / best-previous merge, C and C++
checkers and managers compiled from source, structured task-type form in
the admin, task tester (reference solutions judged on every dataset, never
counted). `worker.TestProblemTypeSamples` judges AC/WA/TLE/MLE/RE for every
type (57 verdicts); `e2e.TestEveryTaskTypeFromAdminUI` creates every type
through the admin UI and judges it on the real worker.

## SPEC_CLOSE block C / audit §3 — User management (done)
Skipping skills: immersive-web-design, master skill.

User profile fields and photo, disable/enable, forced logout (a session
epoch checked on every request), active sessions with IP and browser
(tracked in Redis, written at most once a minute per session, off the
request path), password generation and single/bulk reset with printable
PDF credential cards (1–8 per page with name, site and contest URL), CSV
import with a preview of every row before anything is written and CSV
export, read-only "view as contestant" through a signed one-minute link
(audited), teams with institution and members (maximum team size), sites
with their own start time (contest duration kept) and a ranking filter,
administrator TOTP 2FA (enrolment with an inline SVG QR code, second login
step, reset by another administrator). Tests: adminweb (import/export,
account actions, teams/sites, TOTP), auth (RFC 6238 vectors), pdf,
contest (site start, practice), e2e.TestAdminControlsContestantSessions.

## Admin i18n, K5, K10, K18–K21 — Task configuration and problem packages (done)
Skipping skills: immersive-web-design, master skill.

The admin web is translated (es/en) like the contest web: every template,
flash notice, error page, form error and field label, with source-parsing
tests that fail on any untranslated message. The dataset page edits
subtasks visually (regex, hand-picked testcases or next N; live preview of
matches, uncovered and shared testcases; thresholds) and tasks can narrow
the contest's languages. Problem packages (`problem.yaml`, `statement/`,
`tests/`, checker/interactor/manager, `graders/`, `attachments/`,
`solutions/`) are documented in docs/en/problem-package.md and
docs/es/paquete-de-problema.md with one example per problem type; the admin
imports them by drag and drop with a preview and per-file errors before
anything is created (new task, optionally in a contest, or a new dataset of
an existing task), judges the reference solutions through the task tester
and shows a validation report (expected verdict from the file name, broken
checkers as system errors); any task exports in the same format, also from
`cmsctl task-import` / `task-export`. Tests: problempkg (examples, per-file
errors, round trip through the database), adminweb (editor, languages,
preview/conflicts/export), contestweb (task languages), cli, and
e2e.TestProblemPackagesFromAdminUI (every example type imported through the
UI and validated by the real judge; a checker that does not compile is
reported; the export imports back as a second dataset).

## SPEC_CLOSE A1 — Communication (done)
Skipping skills: immersive-web-design, master skill.

Contestants ask about a task or in general (with a per-minute limit),
read announcements, private messages, their answers and the answers made
public for everyone; an unread badge in the menu follows new items live
over SSE with an optional sound. The staff inbox lists pending questions
oldest first (contest and task filters, a live counter in the admin menu)
and answers privately or publicly with free text or one of five quick
answers; announcements and messages to a user or a whole team. Tests:
e2e.TestCommunicationFlow (both web servers, SSE, unread counts, Spanish),
adminweb.TestQuestionInbox, contestweb.TestAskQuestion.

## SPEC_CLOSE A2 — Ranking (done)
Skipping skills: immersive-web-design, master skill.

Per-contest ranking settings in the admin (who sees it, what contestants
see, during/after, freeze of the last minutes with manual unfreeze,
subtasks, flags, institutions, hidden users, anonymous), a contestant
ranking page (full or own position), team ranking, and the ranking web
server: fed by a pusher inside the dispatcher (deltas with sequence numbers,
full boards on demand), cached JSON snapshots, live rows over SSE, score
history per participant with an SVG chart, flags as assets, admin-only
boards behind a secret link, persistence across restarts. The e2e test
measures the scoreboard update at ~0.2 s after scoring (target < 1 s).
Test infrastructure: the isolate box ids of the test packages that judge
(e2e 100–299, worker 400–599, dispatcher 600–879, sandbox 900+) no longer
overlap; an e2e stack could reach into the worker range when packages ran
in parallel, which explained rare wrong verdicts in the full suite.
