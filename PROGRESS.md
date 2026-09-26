# Progress

Status of each phase of SPEC.md §9. Updated at the end of every phase.

| Phase | Status |
|-------|--------|
| F0 Foundations | done |
| F1 Data model and blob store | done |
| F2 Sandbox + worker | done (cgroup v2 path: pending verification on real hardware) |
| F3 Task types, checkers, languages | done |
| F4 Dispatcher | done |
| F5 CWS | done (3000-contestant target on 4 vCPU: pending verification on real hardware) |
| F6 AWS | done |
| F7 RWS | done (SPEC_CLOSE A2; 10,000 spectators measured in SPEC_CLOSE F1) |
| F8 Tokens, limits, user tests, Q&A, printing, analysis, ICPC | done (SPEC_CLOSE A1, B2–B8) |
| F9 Import/export | done (SPEC_CLOSE A4, D7; K18–K21) |
| F10 Performance and security | done (SPEC_CLOSE F1–F3; the load tests: pending on the target VPS) |
| F11 Deployment | done (SPEC_CLOSE A5, A6, F4, F5) |
| Audit (SPEC_AUDIT.md, SPEC_CLOSE.md) | done — see AUDIT.md and SUMMARY.md |

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

## SPEC_CLOSE A3 — Invalidate submissions (done)
Skipping skills: immersive-web-design, master skill.

An admin with full permissions invalidates a contestant submission from its
page with a mandatory reason (audited); it stops counting for the task
score, ICPC attempts, the ranking and output-only merges at once, keeps its
result, and can be restored. Contestants see an "invalidated" tag in their
list and the reason on the submission page; the admin list tags it too.
Tests: dispatcher.TestInvalidatedSubmissionsDoNotCount (score and ICPC
attempts go and come back), e2e.TestInvalidateSubmissionFromAdminUI
(through both web servers with a real judge, audit rows).

## SPEC_CLOSE A4 — Backups (done)
Skipping skills: immersive-web-design, master skill.

`cmsctl dump` writes the database and every blob into one zstd tar whose
manifest holds the SHA-256 of every member; `cmsctl backup-verify` checks
it (and the whole-file digest), `cmsctl restore` loads it into an empty
database (or `-force`), older backups included (they are migrated forward).
The admin web server takes scheduled backups (every `contest_interval`
from 30 minutes before a contest to 30 minutes after, `interval`
otherwise), rotates them, throttles its reads and copies them to S3 when
configured; failures raise admin alerts and a metric. The admin page lists
them with progress, "Back up now", download and delete (full admins,
audited). Docs: docs/en/backups.md, docs/es/respaldos.md. Tests: dump →
empty database → restore → every table, sequence, migration and blob
identical; damaged/truncated archives rejected without committing; forced
replacement; older schema; throttle; schedule, rotation and S3 copy; CLI;
admin page.

## SPEC_CLOSE A5 — Judge verification on real hardware (done)
Skipping skills: immersive-web-design, master skill.

`scripts/verify-host.sh` checks root, kernel, isolate (installed, setuid,
configuration, safe box root, disk), cgroups (v2 controllers,
isolate.service), a real `isolate --cg` run, CPUs, SMT, turbo, governor,
swap, NTP and isolate-check-environment, then runs `cms ctl
judge-selftest`: the security battery and the AC/WA/TLE/MLE/RE/CE samples
of every installed language judged twice through the worker code with the
host checks, verdicts required identical. Each problem is printed as
OK/WARN/FAIL with its fix; `RESULT: FAIL … do NOT start the contest` exits
1. The battery and samples are now embedded in the binary
(`internal/selftest`) and shared with the worker tests. Docs:
docs/en/verify-host.md, docs/es/verificar-host.md. Test: cli.TestVerifyHost
runs the script end to end (OK with two judged runs; FAIL without isolate).
On this development VM: RESULT OK with 2 warnings (legacy cgroup v1 +
isolate 1.10; ASLR/THP), 25 programs per run in ~18 s.

## SPEC_CLOSE A6 — Deployment (done)
Skipping skills: immersive-web-design, master skill.

`scripts/install.sh` installs (or upgrades) a main server or an extra
worker on Debian/Ubuntu: packages, isolate 2 and cgroup v2, the `cms`
user and directories, secrets generated once, `cms.yaml`, PostgreSQL and
Valkey tuned for 2 vCPUs, systemd units (`deploy/systemd`, `cms.target`,
restart always) with CPU-pinning drop-ins keeping everything but the
sandbox off the judging core, Caddy or nginx with Let's Encrypt (or plain
HTTP with `--lan`), ufw, migrations and the first admin. New `cms
blob-server` + `blob.backend: http` let workers on other machines judge
with only Valkey and the blob server reachable over a private network.
Docs (es/en): deployment step by step, external worker (WireGuard),
contest-day runbook. Tests: cli.TestInstallScriptRender (rendered files,
idempotency, variants), blobserver.TestBlobServerRoundTrip,
e2e.TestExternalWorker (a real judgement through the blob server),
cli.TestDocsLinks.

Block A (A1–A6) is complete.

## SPEC_CLOSE B1 — Contest status, copies, languages, timezone (done)
Skipping skills: immersive-web-design, master skill.

Migration 0009 adds the block B contest settings. Contests are draft
(hidden from contestants, previewable by admins, off the ranking servers),
published or archived (read-only, unlisted). "Copy this contest" creates a
draft with every setting, the sites and the tasks with all their datasets,
testcases, managers, statements and attachments (task names get a suffix),
optionally the participants, never submissions. Programming languages,
interface languages and the timezone were already per contest; tests now
cover them. Tests: adminweb.TestContestClone, contestweb.TestContestStatus,
db.TestRowToUpdateCopiesEveryField.

## SPEC_CLOSE B2 — Schedule (done)
Skipping skills: immersive-web-design, master skill.

Practice (upsolving) after the contest: unofficial submissions with no end.
"Extend the contest" (± minutes, audited) moves the end and every per-user
window; open contest pages receive a `clock` event and update their
countdown without reloading. Per-user windows with Start, analysis mode and
the countdown were already in place. Tests: contestweb.TestPracticeMode,
contestweb.TestClockFollowsExtension, adminweb.TestContestExtend.

## SPEC_CLOSE B3 — Modality (done)
Skipping skills: immersive-web-design, master skill.

Team mode and the maximum team size are editable; in team contests members
see and open each other's submissions (with the author), share the
submission limits and the task score (merged as the ranking does) and get
each other's live updates. IOI/ICPC with penalty already existed. New tasks
and imported packages take the contest's default score mode and precision
(packages keep what they state). Tests: contestweb.TestTeamSharedSubmissions,
adminweb.TestContestModality, problempkg.TestImportExportRoundTrip.

## SPEC_CLOSE B4 — Results and feedback (done)
Skipping skills: immersive-web-design, master skill.

Per contest: scores shown always, only after the end or never (overview,
task page, rows and details all follow it) and the compiler's messages can
be hidden; the feedback level stays per task. Tokens now have their
contestant UI: the task page shows the tokens available and when the next
one comes, each own official submission offers "use a token", which shows
its full result and re-aggregates the score. Tests: contest.TestTokens,
contestweb.TestTokens, contestweb.TestScoreVisibilityAndCompilerOutput.

## SPEC_CLOSE B5 — Submissions and user tests (done)
Skipping skills: immersive-web-design, master skill.

A per-contest maximum file size (shown with the task limits) caps every
submitted file. Contestants can now test: on the task page they send their
source with an uploaded or typed input, follow the status live, see the
first 4 KiB of the output inline and download input and output; the number
and frequency of tests follow the contest and task limits, and the contest
toggle hides it all. Tests: contestweb.TestUserTests,
e2e.TestUserTestJudged (real judge).

## SPEC_CLOSE B6 — Access (done)
Skipping skills: immersive-web-design, master skill.

Contests choose who creates accounts: the organizers (as before),
self-registration approved by an admin, or self-registration with an
invitation code. The login page links to a registration form (username,
name, optional email and institution, password twice, code) with the
contest's password policy; approval-mode registrations wait (login refused
with a clear message) until an admin approves or rejects them from
Participations, where they are marked and counted (also on the contest
page); they are not ranked meanwhile. Sessions last the contest's session
duration (24 hours by default). IP restriction, autologin and single login
were already in place. New docs: docs/en/contest-settings.md,
docs/es/configuracion-del-concurso.md. Tests:
contestweb.TestRegistrationWithApproval, contestweb.TestRegistrationWithCode,
contestweb.TestSessionDuration, adminweb.TestRegistrationSettings,
ranking.TestPendingRegistrationsNotRanked.

## SPEC_CLOSE B7 — ICPC mode (done)
Skipping skills: immersive-web-design, master skill.

Every scored submission now carries a binary verdict (AC, WA, TLE, MLE, RE,
OLE, CE). In ICPC contests contestants see only verdicts — task page, rows,
submission page — and an overview of accepted tasks and rejected attempts;
never scores or testcase details. Penalty and freeze were in place; the
public scoreboard now reveals the rows bottom-up when unfrozen instead of
reloading. New staff **Balloons** page: every task solved by a team, oldest
first, first solves marked, site filter, live refresh, delivered/undo.
Tests: scoring.TestICPCVerdict, dispatcher.TestEndToEndScoring (real
judging: verdicts and the balloon event), contestweb.TestICPCVerdicts,
adminweb.TestBalloons, rankingweb.TestPushProtocolAndLiveRows.

## SPEC_CLOSE B8 — Printing (done)
Skipping skills: immersive-web-design, master skill.

Contestants print PDFs or source code from a new Printing page during the
contest; text is typeset with line numbers, pages are counted at upload and
the contest limits (jobs, pages per job, new total pages per contestant)
apply at once. `cms printing` (previously a stub) sends each job to CUPS
with a cover page, retries, and recovers interrupted jobs. The staff queue
shows jobs to deliver, waiting, failed and delivered, with deliver/undo,
reprint, cancel and a PDF view; both pages update live. Docs: contest
settings and deployment (CUPS). Tests: pdf.TestCountPages,
printing.TestPrepareText, printing.TestService (fake lp: printing, retries,
failure, restart recovery, events), contestweb.TestPrinting,
adminweb.TestPrintQueue.

Block B is complete: B1–B8 (C1–C9 and X11 of the audit).

## SPEC_CLOSE D1 — Submissions (done)
Skipping skills: immersive-web-design, master skill.

The admin submission list filters by verdict and by a date range (contest
timezone) besides task, user, status, language and score, and shows the
verdict. Sources are highlighted server-side with line numbers. "Download
as zip" gives every submission matching the filters (contest, task or user)
with an index.csv; bulk downloads (this one and backups) are audited.
Tests: highlight.TestHighlight (+ benchmark),
adminweb.TestSubmissionFiltersSourceAndZip.

## SPEC_CLOSE D2 — Manual score adjustments (done)
Skipping skills: immersive-web-design, master skill.

Administrators add or remove points on a contestant's task score with a
mandatory reason (participation page). Adjustments are append-only, kept
in the stored score by every re-aggregation, counted by live and frozen
rankings (from their time) and team merges, shown to the contestant with
the reason and audited. Tests: adminweb.TestScoreAdjust,
db.TestParticipationTaskScores, dispatcher.TestInvalidatedSubmissionsDoNotCount
(real judging), ranking.TestScoreAdjustments,
contestweb.TestScoreAdjustmentShown.

## SPEC_CLOSE D3 — System panel (done)
Skipping skills: immersive-web-design, master skill.

Workers & queues now shows, live: the jobs in flight (stuck ones flagged,
with an audited requeue), the CPU, load, memory and free disk of the main
server and of every worker's machine (sent with the heartbeat), the blob
store and database sizes, and the system errors with reevaluate. Tests:
adminweb.TestSystemPanel, queue.TestInFlightAndManualRequeue,
hoststat.TestSample.

Fix found on the way (D62): spurious "exit code 127" runtime errors on
multi-core workers — a forked child could briefly hold an executable open
for writing while another slot ran it (ETXTBSY). Executables are now
written with forks held off; regression test
worker.TestExecutablesUnderConcurrentSlots.

## SPEC_CLOSE D4 — Task statistics (done)
Skipping skills: immersive-web-design, master skill.

The statistics page adds, per task, the submissions by verdict (from the
stored binary verdicts) and the first accepted submission (who, when,
contest minute; hidden contestants excluded), next to the existing score
distribution and testcase verdicts. Test: adminweb.TestTaskStatistics.

## SPEC_CLOSE D5 — Results export (done)
Skipping skills: immersive-web-design, master skill.

The admin ranking exports a printable PDF besides CSV and JSON: A4
landscape, one row per contestant (or team), one column per task, total
or solved/penalty, header repeated on every page, labels in the admin's
language, same site/hidden filters. Test: ranking.TestWritePDF (+ the
download in adminweb.TestEveryPageRenders).

## SPEC_CLOSE D6 — Audit log (done)
Skipping skills: immersive-web-design, master skill.

The audit log filters by administrator, action prefix (with suggestions of
the recorded actions) and a UTC date range; "older" pages keep the
filters. Every mutating admin request, logins and bulk downloads are
recorded. Test: adminweb.TestAuditFilters.


## SPEC_CLOSE D7 — Other package formats and contest archives (done)
Skipping skills: immersive-web-design, master skill.

- CMS italy_yaml tasks and full Polygon packages are converted on import
  (admin preview says "converted from the … format", warnings for what
  does not carry over) and then validated and imported like native
  packages, reference solutions included (D63). Examples in
  docs/examples/other-formats/ are judged for real in
  e2e.TestProblemPackagesFromAdminUI; unit tests problempkg.TestConvert*.
- Contest archive: one zip with every row of a contest (optionally with
  submissions, results and evaluations), the referenced files and
  results.csv; imported as a new contest with fresh ids in this or a newer
  installation (D64). Admin: *Archive* on the contest page, *Import a
  contest archive* on the contests page (both audited); CLI:
  `cmsctl contest-export` / `contest-import`. Tests:
  contestarchive.TestExportImportRoundTrip (another installation, every
  table compared after normalising ids; re-import with reused users),
  TestExportWithoutSubmissions, TestImportRejectsDamagedAndNewerArchives,
  TestEveryTableIsDecided (catalog-driven), adminweb.TestContestArchiveFromAdmin,
  cli.TestContestArchiveCommands.
- Docs: problem-package / paquete-de-problema (other systems),
  backups / respaldos (contest archives), contest-day runbook.

Block D is complete.

## SPEC_CLOSE E1 — Plagiarism report (done)
Skipping skills: immersive-web-design, master skill.

Contest → Plagiarism (and *similarity report* per task on the statistics):
choose the task, the latest or best submission of each contestant and a
minimum similarity; the report lists the pairs with their similarity and
shared fragments, and opens a side-by-side view with the shared lines
marked (D65). Package plagiarism (normalised tokens from the highlighter,
winnowed fingerprints, base code removed by token, common fragments
ignored); ~80 ms for 500 submissions of 150 lines
(plagiarism.BenchmarkReport500). Tests: plagiarism.Test*,
highlight.TestTokens/TestHTMLMarked, adminweb.TestPlagiarismReport.

## SPEC_CLOSE E2 — Certificates (done)
Skipping skills: immersive-web-design, master skill.

Contest → Certificates: title, text with placeholders ({name}, {rank},
{award}, {score}, {institution}, {date}, …), awards by rank, minimum score
or awarded only, signatures, footer and a PNG/JPEG logo; preview, one PDF
with every certificate, and each participation's own (audited). Optional
self-service download for contestants once their time is over (D66). The
PDF writer draws images. The template is copied by contest clone and
travels in contest archives. Tests: certificate.Test*, pdf.TestImages,
adminweb.TestCertificatesFromAdmin, adminweb.TestContestClone,
contestweb.TestContestantCertificate, contestarchive round trip.

Block E is complete.

## SPEC_CLOSE F1 — Load tests on the 2 vCPU layout (done)
Skipping skills: immersive-web-design, master skill.

`loadtest/` (`make loadtest`): a complete installation pinned like the
reference 2 vCPU server (web, dispatcher, PostgreSQL and Valkey on one
CPU, the sandbox on another, k6 on the other two), production database
settings, contestants logging in, browsing, submitting with a final burst,
their event streams open, and ranking spectators; `loadtest/spectators`
adds thousands of ranking streams. The report gives client- and
server-side latency, judging per minute, per-core and per-process CPU,
memory and optional CPU profiles (D69). Results in loadtest/README.md:

- 500 contestants + 1,000 spectators: every threshold met, no failure;
  contest pages p95 42 ms as k6 sees them, 18.9 ms in the server; web core
  42% busy. Judging 70–86 submissions/min on the judging core; the final
  burst drains 20 minutes after the end (one judging core is the limit).
- 1,000 contestants: pages p95 132 ms; only the synchronized login storm
  misses its threshold (p95 12 s). The machine supports ~500 contestants
  with margin, up to ~1,000 with logins spread out.
- 10,000 more ranking streams on the same core: every update reaches all
  of them within 0.76 s; contest pages p95 46 ms in the server.

Bugs the load tests found and fixed: evaluations starving behind
compilations (D67); the ranking fan-out writing whole rows to every
stream (D70, rows that only move now travel as rank shifts, pages detect
missed updates); pending marks flickering on every scoreboard once a
minute (D71). The web servers' `pprof` option was declared but never
wired; it now serves the profiler to local requests only. Tests:
rankingweb.TestCompactUpdates, rankingweb.TestShifts,
rankingweb.TestLivePageInBrowser (the page in headless Chromium),
dispatcher.TestPendingCountsOnArrival, webkit.TestProfilingIsLocalOnly.
Pending on real hardware: the same runs on the target VPS.

## SPEC_CLOSE F2 — Web hardening (done)
Skipping skills: immersive-web-design, master skill.

- Login limits count failures only, per address and per username (and
  second-factor codes per administrator): a lab behind one NAT address
  logs in at once, guessing stays limited (D68).
- At most max(2, cores) argon2id computations at once (19 MiB each): a
  login storm queues instead of exhausting memory; absurd stored
  parameters are refused.
- Request bodies are bounded before the CSRF check parses them (uploads by
  route, 64 KiB/1 MiB forms, 16 KiB login forms): larger ones get 413 and
  never reach the disk. The language cookie only takes known languages.
- SIGUSR1 dumps every goroutine of any service without stopping it.
- Tests: contestweb.TestEveryPostNeedsCSRF and
  adminweb.TestEveryAdminPostNeedsCSRF (every POST route, enumerated from
  the source: missing token, another session's token, another origin),
  TestSessionCookiesAreHardened / TestAdminCookiesAreHardened,
  TestLoginLimitsCountFailures / TestAdminLoginLimits (NAT lab, per
  username, 2FA lock), TestRequestBodiesAreBounded /
  TestAdminBodiesAreBounded, auth.TestVerificationsAreBounded,
  webkit.TestLimiterOverAndHit; CSP and headers were already covered
  (webkit.TestSecurityHeaders, contestweb.TestNoInlineCodeAndSecurityHeaders).
- Docs: "Security and limits" in the deployment guides.

## SPEC_CLOSE F3 — Suite, security battery and sample solutions (done)
Skipping skills: immersive-web-design, master skill.

On the final code: `make lint` clean; `make test` green (34 packages,
~20 minutes: real PostgreSQL, Valkey and isolate, the e2e stacks, the
drill, the live ranking page in headless Chromium); `make test-sandbox`
green (worker.TestMaliciousBattery 31 s, worker.TestSampleSolutions
71 s); cli.TestVerifyHost judges the embedded self-test twice with
identical verdicts. Pending on real hardware: isolate 2 with cgroup v2
(this VM has cgroup v1 and isolate 1.10), via `cms-verify-host` on the
target VPS.

## SPEC_CLOSE F4 — Administrator documentation (done)
Skipping skills: immersive-web-design, master skill.

docs/en/admin-guide.md and docs/es/guia-del-admin.md: administrators and
roles, a contest from scratch, contestants (CSV, credentials, teams),
tasks from packages or by hand, every problem type step by step (task type,
options and managers to upload), before/during/after the contest, linking
the reference guides (contest settings, task types, packages, contest day,
backups, security settings).

## SPEC_CLOSE F5 — Drill (done)
Skipping skills: immersive-web-design, master skill.

docs/en/drill.md and docs/es/simulacro.md: a 45-minute rehearsal with a
checklist — three problems (normal I/O, interactive, output only) from the
documented packages, two contestants, a question answered publicly, an
announcement, an invalidated submission, the frozen ranking, unfreezing,
CSV/PDF results, backup and archive. internal/e2e/drill_test.go
(TestDrill) plays the same steps with real judging in `make test`.

## SPEC_CLOSE F6 — Final summary (done)
Skipping skills: immersive-web-design, master skill.

[SUMMARY.md](SUMMARY.md): what is complete, what remains to verify on the
real VPS, and the important decisions. AUDIT.md has no row left missing
or partial; the rows marked **hw** need the target machine (isolate 2
with cgroup v2, the load tests on its CPUs). SPEC_CLOSE.md blocks A–F are
complete.

## Distribution and installation (done)
Skipping skills: immersive-web-design, master skill.

AUDIT.md §9 (G1–G8), decisions D72–D77.

- **Releases (G1).** A tag `vX.Y.Z` runs `.github/workflows/release.yml`:
  lint and fast tests, then GoReleaser publishes static binaries for
  linux/amd64 and linux/arm64, `checksums.txt` (SHA-256), a changelog
  from the commits and `cms_<version>_linux_<arch>.tar.gz`. The tarball
  holds both binaries, example config, languages, migrations, templates,
  static files, systemd units, installer, verify-host and docs. CI builds
  a snapshot on every push and checks it (`scripts/check-release.sh`).
- **Images (G2).** `ghcr.io/<owner>/<repo>/cms` and `/worker` for amd64 and
  arm64, tagged X.Y.Z, X.Y and latest. The Go part is cross-compiled,
  both images run as uid 2000, and there is no baked-in configuration.
- **One-line installer (G3).** `curl -fsSL …/scripts/install.sh | sudo bash`
  downloads the release and verifies its checksum. It lays out
  `/opt/cms/releases/<v>` + `current`, installs isolate and PostgreSQL /
  Valkey tuned to the machine's CPUs and RAM, systemd, and Caddy (or
  nginx) HTTPS for a domain. It then runs `cms-verify-host`. It stops
  on unsupported systems (containers, WSL, cgroup v1 with an explanation
  or `--enable-cgroup-v2`), and `--dry-run` lists every blocker. It is
  idempotent and can uninstall (`--uninstall [--purge]`).
- **Docker Compose (G4).** `deploy/docker/compose.yml` + `setup.sh`: random
  secrets in `.env` and a CPU/RAM layout. The worker gets only
  `SYS_ADMIN` + `NET_ADMIN`, no AppArmor profile and a private cgroup
  namespace (never privileged). Data is in named volumes, Caddy serves
  HTTPS, and backups go to a volume. Verified end to end with locally
  built images.
- **`cmsctl upgrade` (G5).** Refuses during a contest unless `-force`
  and refuses downgrades. It downloads and verifies the release, takes a
  backup, stops, switches, migrates with the new binary, starts and waits
  for `/healthz`. On any failure it switches back and restores the
  database. Remote workers only switch and restart.
- **First boot (G6).** No admin/admin outside `make dev`: the first
  administrator's password is random and printed once (`cms ctl
  bootstrap -generate-password`; `cms ctl admin-password` resets it).
  Logging in with a well-known default forces a password change before
  any other admin page.
- **Documentation (G7).** README and docs/{en,es}: server requirements
  (and what cannot judge), sizes by number of contestants, one-line
  install, Docker, upgrade and uninstall. The external worker, contest
  day and backups pages were updated to match.
- **License (G8).** Apache-2.0 (LICENSE, NOTICE, README).

Tested here: `make lint`, `make test`, shellcheck and actionlint on the
scripts and workflows, a GoReleaser snapshot with `check-release.sh`, and
the production compose stack with local images (all services healthy,
backup and restore, re-run). Pending on real infrastructure: the first
tag's release and GHCR push, the arm64 images on arm64 hardware, and a
one-line install on a fresh VPS of each supported distribution.

## Final check (done)
Skipping skills: immersive-web-design, master skill.

- **CI was red since the problem packages were added.** `.gitignore`
  ignored `*.out`, so the expected outputs of the example packages were
  never committed. It is green again on every job, and the fix also
  revealed two bugs: an HTTP 500 in the package preview and a
  core-count-dependent test.
- **Static analysis.** staticcheck is clean except for user-facing
  message strings, which are capitalized on purpose; dead code was
  removed. govulncheck reports no reachable vulnerability, and
  x/crypto was bumped past the two it listed. sqlc and go.mod show no
  drift.
- **Bad input.** A new test sends every admin and contestant GET route
  (1,000+ requests) and every POST route (a one-off run) real, missing
  and malformed ids and junk parameters: no 5xx. The contestant route
  tests also read `extra.go` now, whose `POST /questions` the CSRF test
  had missed (it was protected).
- **The published v0.1.0.** Checksums and contents check out. The
  one-line installer's dry run and `cmsctl upgrade` work against the real
  GitHub release. The GHCR images pull anonymously for both
  architectures.
- **Installer on Ubuntu 22.04.** Its archive has no Caddy, so the
  installer now adds Caddy's repository there and reads the package lists
  before choosing (D78). Every package was resolved on Ubuntu 22.04/24.04
  and Debian 12/13.
- **A test racing itself.** TestLivePageInBrowser simulated a dropped
  connection by closing every client connection, including the test
  script's own polling connection, so it failed whenever a poll was in
  flight (most runs under load). It now closes only the browser's
  connections (15 of 15 runs pass).
- **Rate-limit tests across a minute boundary.** The limiter counts per
  calendar minute; the login-limit and question tests expected all their
  events in one window, and under the race detector they take seconds,
  so a minute boundary in between split the count (about one CI run in
  ten). The limiter takes a clock (`Limiter.Now`, also passed to the
  contest server through `Deps.Limiter`), which those tests freeze.
- **Printing events.** printing.TestService stopped listening as soon as
  the job's database state was final, but the matching event travels
  through Redis a moment later, so the last one was sometimes missed. It
  now waits for it (20 of 20 runs under the race detector pass).
- **Open: one unexplained CI failure.** TestReevaluationLevels failed
  once (CI run 46, isolate 2.7 on cgroup v2): a correct C solution was
  judged 0 on its first judging. It passed in the runs before and after
  and in every local run (the failure could not be reproduced on this
  machine's cgroup v1). The test now checks the first score and logs
  the compilation and every evaluation on failure, so the next
  occurrence will show the cause.
- **Not fixable from here.** The repository has no `main` branch yet,
  so the documented one-liner (`…/main/scripts/install.sh`) answers 404
  until one exists.
