# Design decisions

Numbered, append-only log of decisions taken while implementing SPEC.md.
Each entry: context → decision → consequences.

## D1. Single binary with subcommands
`cms <service>` runs any service; `cmsctl` is `cms ctl`. One artifact to build,
version and deploy; services share code without IPC libraries. Startup cost is
unaffected (Go binaries start in milliseconds).

## D2. Go 1.27.1, module `github.com/D4ND3R/Contest-Management-System`
Latest stable Go at the time of writing (installed from go.dev). Module path
matches the GitHub repository exactly so `go install` works.

## D3. YAML configuration + `CMS_*` environment overrides
One file for every service (each reads its section). Unknown keys are
rejected to catch typos. Secrets and endpoints can come from the environment
(systemd `EnvironmentFile`, docker env).

## D4. Forward-only embedded migrations with an in-house runner
`internal/db/migrations/NNNN_*.sql`, applied in a transaction each under a
PostgreSQL advisory lock. Avoids a migration-tool dependency; down migrations
are rarely safe during a live contest (restore from backup instead).

## D5. Tests run against real PostgreSQL and Redis
`make test` starts throwaway native PostgreSQL/Redis (docker fallback) via
`scripts/infra.sh`; CI uses service containers. Each test clones a migrated
template database (`CREATE DATABASE … TEMPLATE`, ~50 ms) and gets a private
Redis key namespace, so packages run in parallel without interference.

## D6. Valkey 8 and a community MinIO build in docker compose
Valkey 8 is protocol-compatible with Redis 7 (Streams, consumer groups).
The upstream `minio/minio` Docker Hub images are no longer published;
`pgsty/minio` (community build of the same server) is used for the S3 blob
backend in development. Any S3-compatible store works in production.

## D7. isolate versions: v2.x in images, v1.10.1 in this sandbox
Production and the docker worker image build isolate v2.7 (cgroup v2, as the
spec requires). The development VM used to build this project exposes only a
cgroup v1 hierarchy whose memory controller is owned by the VM agent, so the
test suite here runs against isolate v1.10.1 (the last release with cgroup v1
support). The wrapper only uses the command-line subset common to both
(`--cg`, `--cg-mem`, `--time`, `--wall-time`, `--processes`, `--fsize`,
`--stack`, `--dir`, `--env`, `--meta`), and the meta-file format is identical.
Tests that depend on cgroup v2 semantics are marked "pending verification on
real hardware" in PROGRESS.md.

## D8. Optional extra CA for docker builds
The Dockerfile accepts a build secret `extra_ca` (compose reads
`$CMS_EXTRA_CA_FILE`) so images build behind TLS-intercepting proxies, which
is common in school/olympiad networks. Without it nothing changes.

## D9. Durations as integer seconds/milliseconds, sizes in bytes
`*_s` / `*_ms` / `*_bytes` columns instead of `interval`: trivial mapping to
Go, no calendar semantics needed, cheaper comparisons in limit checks.

## D10. Result generations for idempotent, restart-safe evaluation
`submission_results.generation` (and `user_test_results.generation`) is
bumped on every invalidation (recompile/reevaluate). Jobs carry the
generation they were created for; results of older generations are ignored.
Combined with upserts on `(submission, dataset, testcase)` this makes
duplicate or late job results harmless, so queues can be at-least-once.

## D11. Blob store layout and verification
Local: `objects/ab/<digest>`, writes to `tmp/` then atomic rename (no partial
reads, concurrent identical writes are harmless). S3: object key = digest,
content hashed before upload (≤8 MiB in memory, larger spooled). Workers read
through a verified LRU cache: content that does not hash to its digest is
never cached nor used. A `blobs` table records size/first upload for GC with
a grace period (uploads whose rows are not committed yet are safe).

## D12. Password hashing: argon2id, OWASP parameters (19 MiB, t=2, p=1)
~15 ms per verification keeps mass logins at contest start cheap while being
memory-hard. `plaintext:` hashes are accepted for imports (CMS compatibility)
and compared in constant time.

## D13. Sandbox file handling: copy into the box, zero-copy only via a read-only stage
isolate `chown`s every file under the box directory to the sandbox user at
the start of each run (and deletes symlinks/special files afterwards). Hard
links from the shared blob cache into the box would therefore give untrusted
code ownership of cached inodes (cache poisoning across submissions).
Decision: files are *copied* into `/box`; large read-only inputs (stdin
testcases, checker inputs) are hard-linked into a per-slot staging directory
bound read-only and `noexec` at `/stage`. Outputs are read back with
`O_NOFOLLOW` and a regular-file check (second layer on top of isolate's
cleanup). Boxes are reused between runs: the worker wipes `/box` and the
box's private `/tmp` as root (≈4 ms per trivial run end-to-end) and falls
back to `isolate --cleanup/--init` after any error.

## D14. One slot per physical core, several boxes per slot
Worker parallelism = slots; a slot is pinned (sched_setaffinity inherited by
isolate and the program) to one physical core (hyperthread siblings are
skipped) and owns 6 boxes: program, checker/manager and Communication
user processes. Boxes never cross slots, so jobs cannot deadlock. By default
the first physical core is left to the OS and the worker when ≥3 cores exist.

## D15. Language definitions travel inside jobs; languages are data
The dispatcher embeds the language definition (commands, limits, env) in
every job. Workers need no language configuration and can never disagree
with each other; changing a flag takes effect for the next job.

## D16. Compile seeds instead of shared compiler caches
Some toolchains (Go) rebuild the standard library in a cold cache (5–10 s).
A shared writable cache would let one submission poison another's build, so
each worker compiles a warm-up program once and *copies* the resulting cache
into every compilation box of that language (0.3 s Go builds).

## D17. TwoSteps and Communication semantics
TwoSteps runs the two steps sequentially in different boxes (the second only
receives the first step's output, bounded by the output limit); time is the
sum, memory the maximum. Communication follows the CMS FIFO protocol; a
contestant killed by SIGPIPE after the manager exited successfully is not
penalised; a manager failure while contestants were merely blocked is a
system error (retried, then reported to admins).

## D18. Verdict mapping
Sandbox statuses map to verdicts: ok → AC/WA/partial (checker), timeout →
TLE, timeout_wall → TLE (wall), memory → MLE, output_limit → OLE, nonzero and
signal → RE (the signal number is kept), compilation failure → CE, any
infrastructure failure → retried, then SE. Contestant stderr is discarded.

## D19. Queues: one Redis stream per priority, strict priority, at-least-once
Workers poll the streams in priority order without blocking, then block on
all of them. Results are published and jobs acknowledged in one MULTI/EXEC,
so a job is either pending (and re-run after a crash) or done with its
result stored. Duplicate executions are harmless thanks to D10. Rejudges
and non-live datasets use the background stream so they never delay fresh
submissions.

## D20. Crash recovery layers
1) Worker death: heartbeat TTL expires → monitor requeues its pending jobs
(copy first, then ack). 2) Stuck job: pending longer than `job_timeout` →
requeued. 3) Dispatcher death: its unacknowledged results/events are
adopted by the next leader (`XAUTOCLAIM`). 4) Anything else (Redis data loss,
crash between commit and enqueue): the sweeper re-derives work from
PostgreSQL (`scored_at IS NULL` and `jobs_enqueued_at` older than
`StaleAfter`, or NULL after an invalidation). PostgreSQL is the only source
of truth; Redis only accelerates.

## D21. HA by leases, not partitioning
Dispatcher and monitor replicas compete for a Redis lease; exactly one is
active. The active dispatcher still processes results in parallel (sharded
by submission so a submission's results stay ordered).

## D22. Scoring runs inside the result transaction
Scores, the task aggregate (score mode + ICPC fields) and the ranking update
are computed in the same transaction that stores the last evaluation, so the
ranking never shows a score whose evaluations are not committed. Ranking
updates go to a durable, capped Redis stream consumed by the RWS pushers.

## D23. Stateless signed sessions, server-side revocation only where needed
Session cookies carry `{participation, user, issued, nonce}` signed with
HMAC-SHA256; no session table and no Redis lookup per request. Logout
clears the cookie. When the contest enables single login, a per-participation
nonce (cached 3 s) is bumped on each login, which invalidates older cookies
— the only server-side state. Secrets rotate by restarting with a new key.

## D24. No inline code; CSP as the XSS backstop
Templates never contain inline scripts, styles or event handlers, so the CSP
is `default-src 'self'` with no `unsafe-inline`. Behaviour is attached by
`app.js` through data attributes. Statements (arbitrary organiser HTML/PDF)
are served from their own URL with a sandboxing CSP. A test fails if any
template or rendered page contains inline code.

## D25. htmx + SSE instead of a SPA
Pages are server-rendered; htmx swaps fragments (submission rows, lists).
Live updates use Server-Sent Events: each process holds one Redis pub/sub
subscription and fans events out to connected contestants from memory, so
Redis load does not grow with the number of browsers. Events are only
hints ("submission 42 changed"); the browser fetches the row, which is
authorised like any page, so no data leaks through the event stream.

## D26. Short TTL caches for contest-wide data
Contest, task and dataset views change rarely during a contest and are
shared by every contestant, so they are cached in-process for 3 s (bounded
staleness, no invalidation protocol needed across replicas). Per-contestant
data (submissions, scores) is always read fresh with a single indexed query.

## D27. Contest names share the URL root
Contests live at `/{contest}/…` like CMS. Top-level names used by the
server (`static`, `healthz`, `metrics`, `lang`) are reserved and rejected
as contest names.

## D28. Latency assertions only on a quiet machine
Performance targets are asserted by dedicated runs (`make test-e2e`, and
the k6 scenarios of F10), never inside `go test ./...`, where packages run
in parallel with CPU-heavy sandbox tests and on shared CI runners. There the
same tests still run and report their percentiles.

## D29. Audit log written by the routing middleware
Every mutating admin route declares an action name; the middleware records
it after the handler succeeded (status < 400), with the target set by the
handler and the sanitized form values. New routes cannot forget auditing,
and failed validations do not pollute the log. The insert happens after
the change (not in the same transaction); a failure is logged loudly.

## D30. Destructive admin actions need the object's name typed
Deleting contests, tasks, users and participations cascades to
submissions, so the form requires typing the name (on top of a browser
confirmation). The live dataset cannot be deleted; the last enabled `all`
administrator cannot be demoted, disabled or deleted.

## D31. CSV user import is all-or-nothing
The file is validated completely first (unknown columns, duplicates, teams,
timezones, IPs); any error aborts the import and lists every problem.
Missing passwords can be generated (readable 10-character passwords from
an unambiguous alphabet) and are shown exactly once. Hashing runs in
parallel (argon2id is deliberately slow).

## D32. Admin sessions are bound to the password hash and role
The admin cookie carries a fingerprint of the admin's password hash and
role, checked against a 3 s cache of the admin row: disabling, deleting,
changing the password or the role of an administrator ends their sessions
within seconds without a session table.

## D33. Worker/queue status by polling, events by SSE
The system page polls a small HTML fragment every 2 s (cheap Redis reads,
always consistent after reconnects); discrete events (system error alerts,
new questions) are pushed to administrators over SSE.

## D34. How the audit (SPEC_AUDIT.md) is folded into the phase plan
The audit arrived after F6. Its sections are implemented in order (§2 → §6)
and absorb the remaining primary phases where they overlap: F7 (ranking web)
and F8 (tokens, limits, user tests, Q&A, printing, analysis, ICPC) are built
inside §4 (contest configuration), F9 (import/export, dump/restore) inside
§6 (backups) together with the importers needed by §2, and F10/F11 close the
work together with §7. PROGRESS.md keeps one section per primary phase;
AUDIT.md tracks every audit requirement with its files and tests. Each audit
section is a commit.

## D35. Interactive tasks use anonymous pipes between two sandboxes
Two isolate runs whose standard streams are opened by isolate on FIFOs
deadlock (each side opens its read end first). The worker instead creates
two anonymous pipes and hands them to the interactor's and the
contestant's isolate processes as inherited stdin/stdout (isolate never
writes to its own stdout with `--silent`), then closes its copies once both
started, so end-of-file and SIGPIPE propagate exactly like on Codeforces.
The interactor follows testlib's exit codes. Verdict order: contestant
limit exceeded → that verdict; contestant crash (not SIGPIPE) → RE;
otherwise the interactor's verdict.

## D36. Task tester runs are submissions without a participation
`submissions.participation_id` became nullable with a `tester` flag (CHECK
keeps them consistent). Tester runs are judged by the unchanged pipeline on
every dataset, at user-test priority, never aggregated, and their events
carry no contest; every participation-based query (contestant pages,
rankings, statistics, exports) ignores them by construction.

## D37. Output-only merge is resolved when submitting
With `merge_previous`, the contest web server completes a partial
output-only submission with, per missing file, the output of the previous
submission that scored best on that testcase (latest when unjudged). The
judge and scoring stay unchanged and the submission shows exactly what was
evaluated.

## D38. Problem packages (K18–K21) come right after §3
The request to add an own problem-package format (import with preview and
automatic validation, export) together with the subtask editor (K5) and
per-task languages (K10) is implemented as soon as the in-flight §3 work is
committed, before §4: it changes how tasks are created, which §4–§6 and the
F9 importers build on.

## D39. Order of work after SPEC_CLOSE.md
The closing prompt supersedes the remaining audit order: the in-flight user
management work (SPEC_CLOSE block C, audit §3) is finished and committed
first because the tree is half-way through it; then the problem package and
task configuration items (K5, K10, K18–K21); then blocks A → F in order.
Target hardware is a 2 vCPU VPS: one core for web + PostgreSQL + Redis, the
other reserved for one sandbox slot, so evaluation never competes with the
web servers for CPU.

## D40. Admin interface translated with the contestant catalog
The admin server reuses the gettext-style catalog of the contest web server
(English text as key, Spanish in `internal/i18n/es_admin.go`, merged at
init), so one language cookie (`cms_lang`) drives both servers. Messages
built in Go (flash notices, error pages, form validation) are translated at
the point where they reach the page; form errors whose format starts with
`%s` also translate the field label. A test parses the admin sources and
fails when a literal message or label has no Spanish entry, another checks
the dynamic values (phases, statuses, queue names). `POST /lang` needs no
session or CSRF token: it only sets a cookie limited to the known languages.

## D41. Subtask editor: server-rendered rows, JSON stays canonical
The visual editor (K5) edits the same Group* parameters as before (CMS
array form, `[[points, testcases(, threshold)], ...]`), so imports, exports
and hand-written JSON keep working; the JSON field remains under
"Advanced" for anything the editor cannot show. Rows are plain form fields
with one blank row for adding and a "remove" box, so the editor works
without JavaScript; with htmx every change re-renders the editor on the
server, which matches testcases with the very function the score type uses
(`scoring.MatchSubtasks` shares `members` with `newGroup`), so the preview
cannot disagree with scoring.

## D42. Languages per task narrow the contest's languages
`tasks.languages` (empty = no restriction) is intersected with the
contest's languages: a task cannot enable a language its contest forbids,
and a contest-wide change still applies to every task. The admin task
tester lists the task's languages but accepts any configured language, so
reference solutions in other languages can still be checked.

## D43. Own problem package format
Packages are a zip with `problem.yaml` (times in seconds, sizes in MiB, as
problem setters write them; unknown keys are errors), `statement/<lang>.*`,
`tests/<name>.in|.out`, `checker|interactor|manager[.c|.cpp]` at the root,
`graders/`, `attachments/` and `solutions/`. It maps one-to-one onto a task
and a dataset (task type and score type parameters are generated by the same
code that validates them), so export → import is lossless; the round trip is
tested through the database for every example. `internal/problempkg` never
fails on a bad package: it returns every problem with its file, and reads
files lazily from the zip so a package with large testcases is never held in
memory (the upload is spooled to the blob store and re-read on
confirmation). Files are stored before the single import transaction;
blobs of an aborted import are left to the blob garbage collector.

## D44. Package validation runs through the task tester
Reference solutions are tester runs (comment `solutions/<name>`), judged on
every dataset at user-test priority by the unchanged pipeline, so
validation cannot slow down contestants' submissions and a checker,
interactor or manager is compiled exactly as in a contest (on the worker;
a source that does not compile turns into a system error shown by the
report). The expected verdict comes from the file name (`ac_`, `pa_`, `wa_`,
`tle_`, `mle_`, `re_`, `ce_`, `any_`); a failure-kind verdict passes when
the solution does not get the full score and shows that kind on some
testcase. Imported tasks stay outside contests (and imported datasets are
not live) until an administrator publishes them. The CLI import does not
run solutions (it has no administrator identity); the admin panel does.

## D45. Communication: seen timestamp, English quick answers, fan-out
Unread counts come from one timestamp per participation (last visit to the
communication page) compared with announcements, messages and answers in
a single indexed query, read from the database rather than the
participation cache so a visit resets the badge at once; live events only
bump the badge client-side. Quick answers are stored as their English text
and translated when shown, so each contestant reads them in their own
language. A message to a team is one message per member (their existing
per-participation index and SSE routing need nothing new). The question
limit uses the Redis rate limiter (no database write on rejection). The
admin SSE hub forwards only alerts and new questions: submission events are
far too frequent to fan out to staff browsers that ignore them.

## D46. Ranking: pusher in the dispatcher, stateless-to-the-DB RWS
The public scoreboard (RWS) never reads the database: a pusher (under its
own Redis lease, started with the dispatcher when `ranking_urls` is set)
watches the ranking stream the dispatcher already writes, recomputes the
affected contest's board — from the per-task aggregates while live, by
replaying submissions before the cutoff while frozen or on a full
resynchronisation — and pushes only changed rows with sequence numbers; a
server that is new or missed a delta answers 409 and gets the full board.
Recomputations are coalesced (250 ms live, 2 s frozen) so the web and
database core never compute a ranking per score. The RWS renders every
changed row once, language-neutral, and sends that HTML over SSE, so ten
thousand spectators cost one render per change; pages are cached per
language. Visibility "contestants" is served by the contest web server
only (it has the sessions), "admins" by the RWS behind a key derived from
the secret, "hidden" nowhere but the admin panel. Team boards merge members
per task (best member per subtask with max_subtask scoring); anonymous
boards label rows by participation order so labels do not move with the
scores.

## D47. Invalidated submissions stay, flagged, and never count
Invalidating a submission (A3) sets `invalidated_at`, a mandatory reason
and the admin on the submission row instead of deleting it or zeroing its
result: the evidence, its result and its evaluations remain for the appeal,
and restoring is one UPDATE. Every query that feeds a score filters
`invalidated_at IS NULL` (task-score aggregation, which also drives ICPC
attempts and penalties; the ranking replay used while frozen; the
output-only merge of previous outputs), so nothing downstream needs to know
about invalidation. After the change the admin web notifies the dispatcher
with a `reaggregate` event: it recomputes that participation's task score
and pushes the ranking update through the usual path, taking about as long
as a normal scoring. Invalidated submissions still count toward submission
limits and intervals, like in CMS (they were really submitted); tester runs
cannot be invalidated.

## D48. Backups: one self-verifying file, taken by the admin web server
A backup is a zstd tar with a JSON header, each table's `COPY` text output
(in 8 MiB members, since tar needs sizes up front and the memory must stay
bounded), the sequences, every registered blob named by its SHA-256, and a
manifest with the SHA-256 of every other member, written last. Why not
`pg_dump`: it is not always installed next to the services, its version
must match the server, and it knows nothing about the blob store; one
format carrying both, readable by our own binary, makes "restore on a fresh
VPS" a single command. The database is read in one REPEATABLE READ
snapshot; blobs (immutable) are streamed afterwards so the snapshot is
short. A restore migrates the target to the backup's migration, drops the
foreign keys, loads every table in one transaction while the blobs go to
the store (Put re-hashes them), checks the manifest, restores sequences and
foreign keys (which validates the data), commits, then applies newer
migrations — so a backup from an older CMS restores into a newer one, and a
damaged file never commits anything. Blob uploads are left outside the
transaction: they are content-addressed, so a failed restore leaves only
unreferenced files that the blob GC removes.

Scheduled and on-demand backups run inside the admin web server (always
running during a contest; the button needs it anyway) under a Redis lease,
at most one at a time. The list lives in the backup directory (a JSON file
next to each archive), not in the database, so it stays true after a
restore and shows archives copied in by hand. To protect the 1-core web
tier, reads are throttled (`backup.max_rate`, 32 MiB/s by default: 1 GiB in
~30 s) and compression uses zstd's fastest level on one goroutine; the
throttle also back-pressures PostgreSQL's COPY. Restoring is deliberately
CLI-only: it needs every service stopped. Rotation applies to scheduled
backups only; manual ones are kept until someone deletes them.

## D49. The judge self-test ships inside the binary
`scripts/verify-host.sh` must run on production hosts, which have no Go
toolchain and no source tree, yet it has to judge the security battery and
the sample solutions through the real worker code. The programs moved from
`internal/worker/testdata` to `internal/selftest/testdata` (still a
`testdata` directory, so the go tool ignores the sample `.go` files) and are
embedded; `selftest.Judge` runs them on any executor and applies the host
checks (surviving sandbox processes by uid range, files created outside the
box, connections to a host listener). `cms ctl judge-selftest` wraps it for
the script, and the worker tests run the very same cases, so the battery
cannot drift between CI and the contest machine. The shell script keeps the
OS checks (kernel, cgroups, isolate permissions and a real `--cg` run, SMT,
turbo, governor, swap, NTP, isolate-check-environment) because they need no
code and are easier to read and adapt as shell. It fails hard only on what
makes judging unsafe or impossible; what only makes timings noisier is a
warning with its fix. The self-test uses boxes 500+ by default so it can run
next to a stopped (or even a running) worker with the default offset 0.

## D50. Deployment: systemd units, CPU pinning by drop-ins, a blob server for remote workers
The single-VPS layout (SPEC §2: 1 core for web + DB, 1 for a sandbox) is
enforced by systemd, not by trust: `scripts/install.sh` writes
`CPUAffinity=` drop-ins for every CMS service except the worker and for
PostgreSQL, Valkey and the proxy, so none of them can run on the judging
core, and the worker (`worker.cores`) pins each box there. Drop-ins keep
the unit files in `deploy/systemd` host-independent. PostgreSQL gets no
parallel query workers and no JIT (they would steal the judging core or
cost more than short queries gain), `synchronous_commit` stays on (an
accepted submission is never lost), Valkey never evicts and keeps an
append-only log (the queues). The installer is idempotent: secrets are
generated once into `/etc/cms/secrets.env`, `cms.yaml` is written only when
missing (operators edit it), every other file is compared before being
rewritten and services restart only through `cms.target`; `--render-only`
lets tests and reviewers see exactly what it would write.

Remote workers used to need the S3 backend (the local blob directory is not
reachable from another machine), which forces an object store on small
installations. `cms blob-server` serves the existing store over HTTP with a
bearer token (GET/HEAD by digest, POST to add; no delete, no overwrite:
content addressing makes that safe), and `blob.backend: http` lets a worker
use it behind its usual local cache, verifying every download against its
SHA-256. With Valkey, that is all a worker needs, over WireGuard or a VPC.

## D51. Contest status and copies
Three statuses: **draft** (the contest web server answers 404 to everybody
but an administrator's read-only preview; the pusher keeps it off the
ranking servers), **published**, **archived** (read-only for contestants:
the phase is forced to "finished", nothing can be submitted or asked; not
listed). Existing contests became "published"; the admin's New contest form
starts at "draft" so nothing half-configured is visible by accident.

A copy is made in one transaction with `INSERT … SELECT` over the columns
the catalog lists (`pg_attribute`), naming only the columns that change
(ids, names, contest, created_at, the live dataset); a column added by a
future migration is therefore copied without anyone remembering to. Task
names are unique across the installation (they appear in URLs and in
packages), so the copies get a suffix (default `-<new contest>`), renameable
afterwards. Participants are optional (same users, teams, IP ranges and
times, sites mapped by name, nobody started); submissions, questions,
announcements and tester runs never travel. `db.TestRowToUpdateCopiesEveryField`
guards the other direction: every column an update query sets is copied
from the row by the `*ToUpdate` helpers, so edit forms cannot reset a
column they do not show.
