# Final summary

Skipping skills: immersive-web-design, master skill.

Where the system stands after SPEC.md (phases F0–F11), SPEC_AUDIT.md and
SPEC_CLOSE.md (blocks A–F): what is complete, what can only be verified on
the real VPS, and the decisions that shape it. Details per phase are in
[PROGRESS.md](PROGRESS.md), every requirement with its files and tests in
[AUDIT.md](AUDIT.md), every decision with its rationale in
[DECISIONS.md](DECISIONS.md).

## What is complete

Every requirement of AUDIT.md is **complete** and covered by at least one
automated test (its "initial" column records what already existed at the
first audit, its "status" column what was added since); `make test` (the
whole suite against real PostgreSQL, Valkey and isolate, judging real
programs) and `make test-sandbox` are green.

- **Judging.** Every CMS task type — batch with stdin/stdout or files,
  graders in C, C++, Java and Python, output only (files or a zip, partial
  submissions, merge with the best previous result), two steps,
  communication (manager + N processes, per-process or summed limits), and
  ICPC-style interactive — with exact, whitespace, float-tolerance, CMS and
  testlib checkers; languages are data (`config/languages/*.yaml`). The
  isolate sandbox passes a malicious battery (fork bombs, memory and disk
  exhaustion, network, forbidden syscalls, escapes) and the sample
  solutions of every language, twice with identical verdicts; the same
  battery ships in the binary and runs on each judging host
  (`cms-verify-host`).
- **Reliability.** Durable Redis-stream queues with explicit ACK, retries
  and leases; result generations make every result idempotent; killing a
  worker mid-evaluation or restarting the dispatcher loses nothing, and
  every service picks up where it was after a restart.
  Evaluations are served before compilations so every score arrives right
  after its own compilation (D67).
- **Contest web.** Contestant portal (htmx + SSE, no inline JS, strict CSP,
  CSRF on every POST, hardened cookies, failures-only login limits, bodies
  bounded before parsing), es/en: tasks and statements, submissions with
  per-testcase feedback, tokens, user tests, questions and announcements,
  printing, contestant ranking, certificates, registration, team contests,
  practice/analysis modes, per-contestant windows, ICPC mode with balloons.
- **Admin.** Everything from the web panel, es/en, with roles, optional
  TOTP, audit log with filters: contests (status, copies, time zone,
  extension), users (CSV import with preview, credentials PDF, sessions,
  impersonation, teams, sites), tasks (every type, subtask editor, datasets
  and autojudge, task tester, problem packages with validation, italy_yaml
  and Polygon conversion, export), submissions (filters, highlighting,
  diffs, zip, rejudge, invalidate), manual score adjustments, workers and
  queues panel, statistics, plagiarism report, results export (CSV, JSON,
  printable PDF), certificates, backups, contest archives.
- **Ranking.** Its own server fed by deltas, snapshots across restarts,
  freeze with manual unfreeze (revealed bottom-up), team ranking, score
  history, private boards; updates reach spectators ~0.2 s after scoring.
- **Operations.** `scripts/install.sh` (idempotent: packages, isolate 2 with
  cgroup v2, PostgreSQL and Valkey tuned for 2 vCPU, systemd units with CPU
  pinning, Caddy or nginx with Let's Encrypt, firewall), host verification,
  one-file self-verifying backups (scheduled, rotated, optional S3) and
  restore, external workers through a blob server, goroutine dumps on
  SIGUSR1, Prometheus metrics and `/healthz` everywhere.
- **Documentation** (es/en): deployment, verify host, administrator's
  guide with every problem type step by step, contest settings, task types,
  problem packages, languages, ranking, backups, external worker,
  contest-day runbook, and a drill with a checklist (the drill's steps are
  also an automated test, `e2e.TestDrill`).

## Load tests (2 vCPU layout)

Measured with `make loadtest` on this development machine, CMS confined to
two CPUs laid out like the reference VPS (details and every figure in
[loadtest/README.md](loadtest/README.md)):

| Scenario | Result |
|----------|--------|
| 500 contestants + 1,000 spectators, final burst | every threshold met, no failed request; contest pages p95 42 ms (k6), 18.9 ms in the server; web core 42% busy |
| Judging | 70–86 submissions/min on one judging core (heavy C++); a 1,000-submission final burst drains 20 min after the end |
| 1,000 contestants | pages p95 132 ms; a synchronized login storm takes up to 12 s (p95) |
| + 10,000 ranking streams | every update reaches all of them within 0.76 s; contest pages p95 46 ms in the server |

So the reference machine serves **about 500 contestants with margin, up to
about 1,000** with logins spread over a few minutes; judging (one core) is
the tighter limit and scales with more cores or remote workers. The load
tests found and fixed three real problems: evaluations starving behind
compilations (D67), the ranking fan-out writing whole rows to every
spectator (D70) and pending marks flickering on every scoreboard (D71).

## What remains to verify on the real VPS

These can only be checked on the machine that will run the contest; the
tools to check them are ready.

1. **Sandbox on cgroup v2 with isolate 2.** The development VM only offers
   cgroup v1 and isolate 1.10 (the CI `sandbox` job runs the battery on a
   cgroup v2 runner). Run `sudo cms-verify-host`: it must end in
   `RESULT: OK`, with the battery and the sample solutions judged twice
   with identical verdicts.
2. **Timing stability** with turbo boost and SMT off, as reported by
   `cms-verify-host`; judge a slow reference solution a few times and
   compare the times.
3. **Installation**: `scripts/install.sh` on a clean Debian/Ubuntu with the
   real domain (Let's Encrypt certificates, firewall, systemd restart after
   a reboot). It is tested by rendering every file it writes, not on a real
   host.
4. **Load**: repeat `make loadtest` on the target VPS (2 vCPU / 4 GB) — or
   with the web on the VPS and k6 on another machine — to confirm the
   figures above on its CPUs, disks and network; SPEC.md's 3,000-contestant
   target on 4 vCPU / 8 GB needs that machine too.
5. **The drill** ([docs/en/drill.md](docs/en/drill.md)) with the real staff,
   contestant computers and network, a few days before the contest; restore
   a backup into a scratch database as part of it.

## Important decisions

- **One binary, one subcommand per service** (D1); PostgreSQL as the only
  source of truth with explicit SQL (sqlc) and forward-only embedded
  migrations (D4); files as SHA-256 blobs, never in the database (D11).
- **Queues: one Redis stream per priority, at-least-once with ACK** (D19),
  crash recovery in layers (D20), leases instead of partitioning for HA
  (D21); **result generations** make repeated results harmless (D10);
  scoring inside the result transaction (D22).
- **Workers serve evaluations before compilations** (D67): found by the
  first load test, where strict compile-first starved the evaluations of
  already compiled submissions.
- **One judging slot per physical core**, the sandbox pinned to reserved
  cores, the web never on them (D14, D50): the judging backlog cannot slow
  the contest site.
- **Web: htmx + SSE, no SPA, no inline code, CSP as the XSS backstop,
  stateless signed sessions** (D23–D25); failures-only login limits,
  bounded password hashing and bodies bounded before parsing (D68).
- **Ranking: a pusher in the dispatcher sends deltas to a stateless-to-the-
  database ranking server** (D46).
- **Languages are data** (D15); **own problem package format**, with
  italy_yaml and Polygon converted into it (D43, D63).
- **Invalidated submissions stay, flagged, and never count** (D47);
  **score adjustments are an additive, audited term** (D60).
- **Backups are one self-verifying file taken by the admin web server**
  (D48); contest archives are generic JSON rows with id remapping (D64).
- **The judge self-test ships inside the binary** so every host is checked
  with the same battery as the tests (D49).
- **How the load tests measure a 2 vCPU machine** (D69); **ranking
  updates carry rank shifts, and pages reload when they missed one**
  (D70); **a submission is pending in the task score from its arrival**
  (D71).

## Relation to CMS

Functional parity with [cms-dev/cms](https://github.com/cms-dev/cms),
reimplemented from scratch: its code (AGPL-3.0) was used only as a
reference for behaviour and architecture, never copied.
