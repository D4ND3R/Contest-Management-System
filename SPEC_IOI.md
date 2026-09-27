# SPEC_IOI — The perfect CMS for IOI-level workloads (third specification)

Given by the project owner after v0.2.1, with the first real installation on
an Ubuntu 26.04 server. It is the source of truth for this block together
with `SPEC.md` (still valid where this one does not change it). The owner's
words are kept below; the gap audit is in `AUDIT.md` §10 and the phases in
`PROGRESS.md`.

Session rules still apply: skipping skills immersive-web-design and master
skill; the reference CMS only for architecture ("never copy its code");
decisions in `DECISIONS.md`; `make test` green before every commit; code and
commits in English, user documentation in Spanish and English.

---

## Request

> I have not yet launched the last version. There was an error during the
> installation: the self-test failed (`fork_bomb_64_procs` got `memory` in
> the second run, want `timeout` or `timeout_wall`; "verdict changed between
> runs"; RESULT: FAIL (1 failed, 6 warnings)). You need to fix this.
>
> I ignored that error and tried the CMS either way. I'm going to describe
> exactly what I want the CMS to be able to do. The CMS that you return
> should apply everything that it says.

### Current problems reported (translated from Spanish)

1. You cannot easily see the result of your submission.
2. Improve the detection of malicious files that attack the server.
3. The testing section gives a 404.
4. There are many LaTeX errors.
5. Statements do not change (a new upload is not shown).
6. Statements are not in PDF.
7. Sample test cases should be shown in the PDFs, not as downloadable
   objects.

### Design

> Based on the image (`docs/design/reference-dashboard.webp`) do a total
> redesign of the UI to match the template used in the image. Important:
> do NOT copy the "RTIOI" style or things like that, that is placeholder —
> but keep the banner, for example.

The project rules still hold: server-rendered HTML + htmx + plain CSS, no
inline JavaScript, strict CSP, no external fonts or scripts, lightweight
pages (SPEC §2).

---

## The Perfect Contest Management System for IOI-Level Workloads

A complete feature specification and design rationale for a contest system
that can run an International Olympiad in Informatics (or anything smaller)
reliably, fairly, and fast.

### 0. What "IOI-level" actually means

| Dimension | Typical IOI value | Design implication |
|---|---|---|
| Contestants | 350–400 on-site, plus online mirrors | Hundreds of concurrent sessions, all logged in at the same second |
| Contest length | 5 hours per day, 2 days | Long-running, zero-downtime requirement |
| Tasks per day | 3 | Each task may have 50–150+ testcases and 5–10 subtasks |
| Submission cap | ~50 per task per contestant | Up to ~50,000 theoretical submissions/day; realistically 10–20k |
| Feedback | Full, per-subtask, near real-time | Evaluation latency is a fairness issue, not a convenience |
| Load shape | Spiky: a burst at start, heavy final hour | Capacity must be planned for the last 30 minutes, not the average |
| Stakes | Medals decided by single points | Timing must be reproducible; every result auditable; re-judging must be safe |

A rough compute budget: `submissions × testcases × avg_runtime`. For
example, 15,000 submissions × 100 tests × 0.5 s ≈ 750,000 CPU-seconds ≈ 210
core-hours inside a 5-hour window, which is ~42 dedicated cores on average
and 2–3× that at peak. The perfect CMS makes this number smaller
(short-circuiting, caching) and makes adding cores trivial.

### 1. Architecture

#### 1.1 Service-oriented, horizontally scalable

- **Contest Web Server (CWS)** – what contestants use. Stateless; many
  replicas behind a load balancer.
- **Admin Web Server (AWS)** – what organizers use. Separate process,
  separate port/network, separate auth.
- **Ranking Web Server (RWS)** – public scoreboard, deliberately isolated
  (can run on a different machine or in the cloud).
- **Evaluation Dispatcher** – owns the job queue, decides priorities,
  assigns jobs to workers, detects dead workers.
- **Workers** – compile and run code inside sandboxes. One worker per
  physical core. Add machines to scale linearly.
- **Scoring Service** – turns raw testcase outcomes into subtask/task
  scores; recomputes on demand.
- **Checker / Health Service** – watches every other service, restarts
  them, raises alerts.
- **Printing Service** – optional.
- **Log / Metrics aggregation** – centralized, searchable logs and
  time-series metrics.

#### 1.2 Data layer

- **PostgreSQL** as the single source of truth; transactions.
- **Streaming replica** (hot standby) for failover and for read-heavy
  queries (admin dashboards, exports) so they never touch the primary.
- **Content-addressed file store** (SHA-256); workers cache files by hash.
- **Durable job queue**; jobs survive restarts.

#### 1.3 Idempotent, deterministic jobs

Every evaluation job is identified by `(submission, dataset, testcase)` and
is safe to run twice; results are upserts.

### 2. Sandboxing and execution

#### 2.1 Isolation

- Kernel-level sandbox (namespaces + cgroups v2, `isolate`).
- No network; read-only root filesystem, private writable `/box` and `/tmp`.
- Process limits (1 for Batch; configurable).
- **Seccomp filter** to block dangerous syscalls as a second layer of
  defense.
- File size, open-file and stack limits (stack = memory limit).
- Fresh sandbox per run.

#### 2.2 Accurate, reproducible resource measurement

- CPU time (user + sys) plus a wall-time limit.
- Memory via cgroup peak usage.
- Worker machine hardening: one worker pinned per physical core;
  hyper-threading disabled or sibling cores left idle; governor
  `performance`, turbo off; swap off, ASLR optionally off; identical
  hardware.
- **Calibration tool**: a benchmark suite on every worker before the
  contest that flags machines more than a few percent off.

#### 2.3 Precise verdicts

Accepted / Wrong Answer / Partial, Time Limit Exceeded (CPU), Wall Time
Limit Exceeded, Memory Limit Exceeded, Runtime Error (signal/exit code),
Output Limit Exceeded, Compilation Error, **Security Violation**, and Judge
Error (never shown as the contestant's fault).

### 3. Languages and compilation

- C++, C, Python, Java, Rust, …; each language a plugin (compile and run
  commands).
- Pinned toolchains published to contestants before the contest.
- Compilation in a sandbox with its own limits.
- **Compilation caching** by hash of (source + language + grader files).
- Per-language time multipliers (optional).
- Compiler output shown to the contestant (truncated).

### 4. Task types

Batch (with optional grader), Communication (manager, multiple contestant
processes, stubs, separate time accounting), Output Only, Two Steps /
Multi-run, and a clean plugin API for custom task types.

### 5. Checking outputs

Built-in comparators (exact, whitespace-insensitive, floating point with
absolute/relative tolerance); custom checkers (testlib and CMS style) with
partial scores; checkers sandboxed; checker messages translatable and
optionally hidden from contestants.

### 6. Scoring

- Subtasks: GroupMin, GroupMul, GroupThreshold, Sum; testcases in several
  subtasks.
- Aggregation: IOI rule (best per subtask across submissions), last, best;
  recomputable from raw results at any time.
- **Short-circuit evaluation**: once a GroupMin testcase fails, skip the
  rest of that subtask (shown as "skipped"); configurable.
- Datasets and live re-judging: evaluate a fixed dataset silently, compare
  scoreboards, switch atomically.

### 7. Feedback to contestants

Full / restricted feedback, tokens, public vs private testcases, real-time
updates (SSE), **queue position / ETA** while waiting.

### 8. Contestant interface

Dashboard (server-synced timer, tasks, scores, announcements); task pages
with statements (PDF and/or HTML) in the contestant's language and
attachments; submission form (language, upload, optional in-browser
editor, client-side checks); submission history (scores, verdicts, compile
output, download); custom tests on the grading machines (lower priority);
clarifications; announcements with notification and sound; print requests;
localization (dozens of languages, right-to-left scripts); accessibility
(keyboard, screen readers, contrast, adjustable font size); lightweight
pages.

### 9. Admin interface

- 9.1 Contest setup: UI and CLI; import/export (italy_yaml, Polygon, native);
  task validation on import; contest settings; per-contestant extra time.
- 9.2 Users, teams and delegations: CSV import; **delegation leaders who can
  view (not submit for) their contestants**; IP binding / autologin; hidden
  users and **unofficial participants**.
- 9.3 Live operations: submissions browser (filters, source, output diffs,
  re-evaluation); re-judge tools at lower priority; clarification desk
  (canned answers, **multi-admin assignment**, broadcast); worker and queue
  monitor; **emergency controls: pause submissions, extend the contest,
  disable a single task**.
- 9.4 Security: roles (full admin, **task setter**, clarification answerer,
  read-only observer); 2FA; **immutable audit log**.

### 10. Ranking and scoreboard

Live scoreboard (per task and subtask, flags, photos); freeze and unfreeze;
separate public ranking server; score history; **medal cutoff
visualization**; configurable tie-breaking; CSV/JSON export.

### 11. Performance targets

| Metric | Target |
|---|---|
| Page load (contestant) | p95 < 200 ms under full load |
| Submission accepted & stored | p99 < 1 s |
| Time to first feedback (compile result) | < 10 s |
| Full evaluation of a typical submission | p95 < 60 s during peak |
| Login storm (400 users in 60 s) | No errors, no timeouts |
| Worker failure recovery | Job reassigned within 30 s |

Techniques: priority queue (compilations first; **each contestant's latest
submission before older ones**; live work before re-judges and user tests;
**fairness so one spammer can't starve others**); batching; local caching
pre-warmed before the contest; horizontal scaling with auto-registering
workers; rate limiting.

### 12. Reliability and disaster recovery

No single point of failure (replicated database, web replicas, workers,
dispatcher failover); **continuous backups (WAL archiving)** plus snapshots;
graceful degradation; **rehearsal mode** with a load generator replaying
realistic submission patterns; **chaos testing** (kill workers, the
dispatcher, a DB node); health checks and alerts (queue depth, latency,
errors, disk, **worker timing drift**).

### 13. Security

Network segmentation; hardened web tier; Argon2/bcrypt, session expiry,
single session; sandbox defense in depth (namespaces + cgroups + seccomp +
unprivileged user + ephemeral filesystem); testcase secrecy; **tamper
evidence: submissions hashed at arrival, hashes in the audit log**.

### 14. Post-contest features

Analysis mode; full data export; **anonymization tools**; **appeals
workflow**; certificates and result sheets.

### 15. Developer and operator experience

One-command local setup; reproducible production provisioning
(**Ansible/Terraform**); comprehensive tests; plugin APIs (languages, task
types, score types, checkers); documentation for contestants, task setters
and operators; **versioned configuration (contest and task configuration in
Git, imported deterministically)**.

### 16. Priorities

1. Core loop. 2. Correctness and fairness. 3. IOI task types. 4.
Operations. 5. Scale. 6. Polish.
