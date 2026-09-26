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

## D52. Live time changes reach open pages as a "clock" event
Every contest change already publishes a `contest` event (cache
invalidation). The contest web server now also forwards a `clock` event to
the open pages of that contest (or participation); each page fetches
`/<contest>/clock` — its own window, since extra time, delays, sites and
per-user time differ per contestant — after a random delay of up to two
seconds so thousands of pages do not hit the server at once, then updates
its countdown in place, or reloads when the phase changed (e.g. an
extension reopened a finished contest). Nothing a contestant was typing is
lost by a mere extension. "Extend the contest" moves `stop_time` and the
per-user duration in one UPDATE; analysis windows are left alone (analysis
only starts once the contestant's window is over).

## D53. Team contests share through the participation group
Team members keep one participation each (logins, sessions, questions and
extra time stay personal), and the contest web server widens its queries to
the **group**: the participation, or all participations of its team in a
team contest (cached for 3 s like other participation data). Submission
lists, access to a submission, the limits (`SubmissionStats` counts the
group) and output-only merges use `participation_id = ANY(group)` on the
existing index; task scores are merged exactly as the ranking merges a
team row (best member per subtask with "best per subtask", else the best
member score). Submission events are fanned out to the teammates' open
pages through a per-(contest, team) index of SSE clients. The dispatcher and
the stored aggregates stay per participation, so switching a contest
between individual and team mode needs no recomputation.

## D54. Tests that judge take a machine-wide lock
`go test ./...` runs packages in parallel, and four packages judge real
programs (worker, dispatcher, e2e, cli's verify-host). Disjoint isolate box
ranges (A2) removed box collisions, but not CPU contention: on a 4-core
machine the packages together ran more sandboxes than cores, so wall-clock
limits expired (a memory hog ended as "timeout_wall", a two-steps solution
lost a testcase to TLE) — exactly the instability the judge must never
show. `sandbox.TestIsolate` now takes an exclusive `flock` on
`$TMPDIR/cms-judging-tests.lock` for the rest of the test (reentrant within
a process), so judging tests of different packages run one at a time while
everything else stays parallel. The full suite got faster (4 min instead of
~5), because nothing is retried or waits on overloaded cores.

## D55. Tokens are computed, not stored
A token play is a row in `tokens`; the tokens *available* are recomputed
from the rules and the play times (`contest.Tokens`): start with
`gen_initial`, add `gen_number` every `gen_interval` from the contestant's
window start up to `gen_max`, subtract plays in time order, then apply the
total cap and the minimum interval. The contest pool (all plays) and the
task pool (plays on that task) must both allow a play. Nothing to keep in
sync, and changing the rules mid-contest is immediately consistent. Plays
lock the participation row (`SELECT … FOR UPDATE`) and recount inside the
transaction, so two clicks cannot spend one token twice; the unique
submission constraint rejects a second token on the same submission. After
a play the dispatcher re-aggregates (it matters for "max of tokened and
last") and the row refreshes with the full result. Tokens are only offered
while the contestant's window runs and scores are visible.

## D56. Self-registration creates ordinary accounts
Registration writes a normal `users` row plus a participation, so everything
else (credentials, impersonation, exports, rankings) needs no special case.
Approval is a flag on the participation (`approved`, true by default so
existing and admin-created participations are unaffected); the login refuses
unapproved participations with an explicit message, the session middleware
re-checks it (a session cannot outlive a revoked approval) and the ranking
skips them. Rejecting deletes the participation but keeps the account: the
username may be in use elsewhere. An existing username cannot be claimed by
registering, since that would let anyone take over another contest's
account; organizers add such users by hand. The invitation code is compared
in constant time and registration is rate-limited per IP like the login.
The password policy (minimum length, letters and digits, not the username)
applies where contestants choose passwords; admin-generated passwords are
already long random strings. The session duration is the age of the
session (issue time), checked on every request, so shortening it closes
existing sessions too; without a setting sessions last 24 hours as before.

## D57. ICPC verdicts are stored; balloons are derived
The binary verdict is computed by the dispatcher when it scores a
submission (`scoring.ICPCVerdict`: accepted at full score, otherwise the
failure kind of the first testcase that did not pass, in the order of the
score details) and stored in `submission_results.verdict`, so listing a
contestant's submissions never reads the score details. Results scored
before the column existed fall back on the score (accepted or "rejected").
ICPC contestants see verdicts only: the per-testcase table would give away
which tests fail, which ICPC does not show. Balloons need no bookkeeping of
their own: the solved (participation, task) pairs are already in
`participation_task_scores`; the balloons page groups them by ranking key
(the team in team contests) and only deliveries are stored, keyed by task
and that key. When an aggregation makes the submission being scored the
first accepted one, the dispatcher publishes a `balloon` event; open
balloon pages reload their list on it (and every 30 s as a fallback, e.g.
after a rejudge). Unfreezing no longer reloads public scoreboards: the
pusher's full board differs only in the freeze state, so the RWS sends the
changed rows ordered by their frozen rank, worst first, and the page reveals
them one by one (at most 30 s in all).

## D58. Printing counts pages at upload; the service only prints
The contest web server turns every upload into the PDF that will be
printed before queuing it: PDFs as they are, plain text typeset in Courier
with line numbers and a header (user, file, page i/n) by our own PDF
writer. So the page count is known at once and the limits (jobs, pages per
job, total pages) are checked in the same transaction that creates the job,
under the participation lock, and the contestant gets the answer right
away. PDF pages are counted without running anything: the largest /Count
of the page tree nodes (dictionaries with /Type /Pages and /Kids, so
outlines do not count), also inside Flate-compressed object streams with a
64 MiB inflation budget; a PDF whose pages cannot be counted is refused
with a hint to print it as text. Accepting only PDF and UTF-8 text keeps
the printer away from arbitrary formats. The `cms printing` service claims
jobs with `FOR UPDATE SKIP LOCKED`, sends a cover page and the document as
one `lp` job (media and fit-to-page from the config), retries lp three
times and then marks the job failed with lp's message; the staff can print
it again. It is woken by the "print" event and polls every 5 s as a
fallback. Jobs left "printing" by a crash are queued again at start: a job
may print twice, never zero times, which is the right side to err on
(there is one printing service per installation). Without a printer the
jobs are marked done with "not printed", for rehearsals. Delivery is a
separate staff mark (who and when), as with balloons.

## D59. Source highlighting is a server-side tokenizer
The admin's source view is highlighted in Go (`internal/highlight`): a
single pass per file that recognises comments, strings, numbers, keywords
and preprocessor lines by the conventions of a language family chosen by
file extension, and writes one `<span class="l">` per line (spans that
cross lines are closed and reopened), numbered with CSS counters. No
JavaScript library (the admin keeps its strict CSP and tiny assets), no
parser to maintain, and a language configured in YAML with a known
extension is highlighted without code. Profiled: the first version spent
most of its time allocating tokens; writing straight into one builder and
skipping bytes that cannot start a token brought it from 16 to 52 MB/s
(`BenchmarkHighlight`), so a typical source costs well under a
millisecond. The submissions zip uses the same filters as the list and is
streamed (index.csv first, then task/user/id/file) without temporary
files; being a bulk export, it is audited like a change (GET routes can
now carry an audit action; backup downloads use it too). Date filters are
read in the contest's timezone, as the contest's times are entered.

## D60. Score adjustments are an additive term of the task score
A manual adjustment is a row of `score_adjustments` (participation, task,
points, mandatory reason, admin, time) and its sum is kept in
`participation_task_scores.adjustment`. The aggregation upsert always
stores "computed from the submissions + adjustment", so a rejudge, an
invalidation or a new dataset never loses it, and every reader of the
stored score (rankings, contestant overview, exports) sees the same
number with no extra query. Readers that rebuild a score from subtask
scores (best per subtask across a team) add the members' adjustments.
The ranking replay applies adjustments at the time they were made, so a
frozen board does not reveal those made during the freeze and the score
history shows them as steps. Adjustments are append-only (corrections are
new adjustments), visible to the contestant with the reason, and audited.
They change scores, not ICPC solved counts: in ICPC contests submissions
are invalidated or rejudged instead. After writing the adjustment the
admin web server asks the dispatcher (reaggregate event with the
participation and task) to recompute and push the ranking update, keeping
the dispatcher the only writer of ranking updates.

## D61. Machine load travels with the worker heartbeat
The system panel needs the load of machines the admin server cannot see.
Workers already send a heartbeat every second; it now carries a small
snapshot (CPU use since the previous heartbeat, load average, memory,
free space of the work and cache directories) read from /proc and statfs
— a few microseconds, no agent, no new port. The admin server samples its
own machine the same way (blob, backup and temporary directories) and adds
the blob store and database sizes, cached for 30 s because counting blobs
is not for every 2-second poll. In-flight jobs come from the Redis pending
lists with their payload (one pipelined XRANGE per job); a job is flagged
stuck after 2 minutes or when its worker's heartbeat is gone, and an
administrator can requeue it at once with the monitor's own mechanism
(claim, re-add, acknowledge) but without the attempt limit, since it is a
deliberate decision; a result arriving later from the old run is handled
idempotently as for monitor requeues. The system-errors list got a
partial index (errors are rare, the result table is not).

## D62. No fork while an executable is being written (ETXTBSY)
A full test run showed a correct TwoSteps solution judged "runtime error,
exit code 127" on one testcase. A stress test (many TwoSteps evaluations
on three slots at once) reproduced it: the executable was in the box, yet
isolate's execve failed. Cause: while one slot copies an executable into
its box, another slot forks to start isolate; the child inherits the
still-open write descriptor (close-on-exec only closes it at exec), and
executing a file that some process holds open for writing fails with
ETXTBSY, which isolate reports as exit code 127. Any multi-core worker
could hit it in a contest. The sandbox now holds syscall.ForkLock for
reading from opening to closing a file written with an executable mode;
every fork of the Go runtime takes that lock for writing, so no child can
be created in that window, while writes of data files are not held back
and the copies themselves still run in parallel. The stress test is kept
(worker.TestExecutablesUnderConcurrentSlots) and fails without the lock.


## D63. Other package formats are converted, not supported natively
italy_yaml tasks and Polygon packages are translated on read into the
entries of an own package (a generated problem.yaml plus the member files
under their new paths) and then go through the very same reader,
validation, preview and import as a native package. One code path checks
limits, subtasks, checkers and solutions, so a converted task is
indistinguishable from a native one and exports as a native package.
Conversion never guesses silently: what cannot be carried over becomes a
warning in the preview (HTML statements without images, unconverted score
parameters, solutions missing from the package) and a Polygon package
without its generated tests is refused with an explanation (the usual
mistake is downloading the "standard" package instead of the full one).
Polygon solution tags map to expected verdicts, so the reference
solutions are judged on import like native ones. Detection is by the
marker file (problem.yaml wins, then task.yaml, then problem.xml).

## D64. Contest archive: generic JSON rows with id remapping
A contest archive is a zip with the contest's rows as `row_to_json` lines,
the referenced files and a header (format, migrations, counts). Rows keep
every column, so columns added by later migrations travel without code
changes; an older archive imports into a newer schema (only the columns it
has are inserted, the rest take their defaults), a newer one is refused.
Import allocates fresh ids from each table's identity sequence and inserts
in batches with `json_populate_recordset` (OVERRIDING SYSTEM VALUE), in one
transaction after the files are stored (content-addressed, so an aborted
import leaves only collectable files). References are rewritten by column
name (`task_id` → tasks, …); a task's live dataset is written once the
datasets exist; references to administrators are emptied (they belong to
an installation). A test walks the catalog so that every table is either
archived or explicitly left out and every foreign key of an archived table
is remapped: a new table or reference cannot be forgotten silently.
Existing users and teams (same username or code) are reused unchanged, so
a contest can be re-imported into the installation it came from; names
that are unique per installation (contest, tasks) must be free, with a
task name suffix as the escape hatch. Left out: executables (recompiled on
demand by the dispatcher's sweep), user tests, print jobs, balloons, the
audit log. Zip rather than the backup's tar.zst because an archive is
meant to be opened and read by people (results.csv) and imported through
a browser upload; the rows come from one REPEATABLE READ snapshot.

## D65. Plagiarism: normalised tokens, winnowing, base code removed by token
The report reuses the syntax highlighter's tokenizer (one lexer for every
configured language, chosen by file extension) and reduces each source to
keywords and operators, with every identifier, number and string replaced
by a fixed token and comments and preprocessor lines dropped: renaming,
reformatting and commenting do not change the stream. Hashes of 12-token
windows are winnowed with a window of 8 (Schleimer, Wilkerson and Aiken,
the method behind MOSS), so any common run of 19 tokens or more is found
wherever it sits, with a few fingerprints per program. A rolling hash over
per-token hashes keeps fingerprinting linear (profiled: per-k-gram FNV and
token strings dominated). Pairs come from an inverted index of
fingerprints, so the work follows the shared fragments, not n² pairs; a
fingerprint in more than max(5, n/10) submissions is an idiom and is
dropped. Code handed to contestants is removed at token level rather than
by fingerprint: every token covered by a k-gram of the attachments,
graders and stubs is marked and no fingerprint may include one, otherwise
the seam between the template and each contestant's code would look like
shared code (a test reproduced exactly that). Similarity is the share of
the smaller program's own fingerprints found in the other, which catches
a copy padded with extra code; the page presents it as a lead to review.
One submission per contestant (latest or best), official and valid; a
team's members are not paired in team contests. Computed on request (the
admin runs it after the contest; it never touches the judging queue).

## D66. Certificates: a stored template rendered on demand
A certificate template is one row per contest (texts, awards, signatures,
logo digest), and certificates are rendered when downloaded from the
final ranking, never stored: they always match the current results
(after a late rejudge or adjustment) and cost no storage. The layout is
fixed (A4 landscape, border, logo, title, centred paragraphs, signature
lines, footer) and the organisers control the words: plain paragraphs
with placeholders and two emphasis levels (`#`, `##`) instead of a
templating language or HTML, so a template cannot break the page and
needs no escaping rules; a paragraph emptied by its placeholders
disappears, which lets one template serve contestants with and without an
award. Awards are rank ranges (ties share rank and award), the common
rule for medals and mentions; score cut-offs are covered by the minimum
score. The PDF writer gained images (JPEG embedded as is, other formats
re-encoded as compressed RGB over white, size capped before decoding) so
the logo costs one object for the whole document. Contestants may
download their own certificate only when the organisers allow it and
their time is over (so ranks cannot leak during the contest or before the
ceremony); the overview checks the template only after the contest, so
the page costs nothing while the contest runs, and the ranking behind
contestants' downloads is cached 30 seconds (everybody downloads at once
after the ceremony).

## D67. Workers serve evaluations before compilations
(Amends the order of D19; strict priorities remain.) The first
500-contestant load test (one judging core) stopped scoring
for 30 minutes: workers polled the queues in strict priority order with
compilations first, and with new submissions arriving faster than one
core compiles them (`<bits/stdc++.h>` takes ~1.5 s), every evaluation of
an already compiled submission waited behind all the newer compilations;
the dispatcher's sweeper then re-enqueued the waiting evaluations as
stale. Diagnosed with goroutine dumps (SIGUSR1 now writes one for every
service without stopping it) and the database timestamps, not guessed.
Evaluations now come first: a compiled submission is finished before new
ones start, so every score arrives right after its own compilation and
the evaluation backlog is bounded by the submissions in flight;
evaluations are short (tens of milliseconds per testcase here), so
compilations barely wait. Stream names are unchanged, so queued jobs
survive an upgrade. User tests and background work (non-live datasets,
old rejudges) still come after both.

## D68. Web hardening: failures-only login limits, bounded hashing, bodies bounded first
- Login limits count **failures only**, per address and per username
  (10 a minute, from any address), and second-factor codes per
  administrator (5 a minute): a school lab behind one NAT address logs in
  at the start signal without hitting the per-address limit, while
  guessing stays limited even from many addresses.
- At most max(2, GOMAXPROCS) argon2id computations run at once: each needs
  19 MiB and ~20 ms of a core, so a thousand simultaneous logins queue
  (seconds) instead of needing 19 GiB; stored hashes asking for more than
  256 MiB are refused.
- Request bodies are bounded **before** the CSRF check reads the form
  (Go parses multipart bodies, files included, on first access): the
  contest web server allows each route its upload limit (submission, user
  test, print job) or 64 KiB for plain forms, the admin 1 MiB or its upload
  limit, the login pages 16 KiB; larger bodies get 413 and are never
  spooled to disk. The interface-language cookie only takes known
  languages.
- Every POST route of both servers is enumerated from the source by a test
  and must refuse a missing token, another session's token and another
  origin, so a new route cannot ship without CSRF protection.

## D69. How the load tests measure a 2 vCPU machine
The load test runs a complete installation on one machine with at least 4
CPUs, pinned like the reference 2 vCPU server (`docs/en/deployment.md`):
one CPU for the web servers, dispatcher, monitor, PostgreSQL and Valkey,
one CPU for the sandbox, and the load generator on the other two so it
never steals from CMS. PostgreSQL and Valkey run with the production
settings (durable commits, append-only file), not the throwaway test
settings. Latency is reported twice: as k6 sees it (the contestant's
view, network and generator included) and from the servers' own request
histograms scraped right after the run (the figure SPEC.md's targets are
stated in). The capacity question ("how many contestants does the machine
support") is answered by raising the load until the web CPU saturates,
with judging throughput measured separately on its own core: judging
backlog never slows the web side, so the two limits are independent. k6
spends a whole virtual user (several MiB) per open stream, so the 10,000
ranking spectators of SPEC.md F7 are played by a small Go client
(`loadtest/spectators`) that measures how long one update takes to reach
every stream.

## D70. Ranking updates: rows that only move travel as rank shifts
(Amends D46.) With 10,000 extra spectators on the 2 vCPU layout the web
core saturated and every web latency degraded. The profile (the web
servers' `pprof` option, now wired: loopback only, never through the
proxy) put 81% of the ranking server in write syscalls: when one
contestant climbs, every row it passes changes rank, and each such row
travelled as its whole HTML, so one update was tens of kilobytes written
to every stream (three syscalls each, past the 4 KiB buffer).
- Rows whose only change is the rank travel as runs `[from, to, delta]`
  of old ranks: rows tied at a rank whose score did not change move
  together, so a climb past hundreds of rows is one run. A rank whose rows
  would move differently (never, for a board ranked by score) falls back
  to one entry per row. Rows whose content changed still travel as
  ready-made HTML. The pusher already sends at most one update per contest
  every 250 ms, so the ranking server needs no batching of its own.
- Shifts are only right for a page in exactly the state they start from,
  so the ranking server remembers the sequence at which the rows last
  changed visibly (`shown`, not every push: history-only pushes change
  nothing on the board); pages carry it and every update names it as its
  `base`. A page whose sequence differs (a frame dropped for a slow
  client, a reconnection, a restart of either server) reloads, with
  jitter, instead of applying changes meant for another state; it used to
  stay stale until its rows changed again.
- Only rows whose content changed and moved are highlighted, not every
  row they pass.
- The client script is tested in a real browser (`TestLivePageInBrowser`:
  headless Chromium through Playwright, random updates, a dropped
  connection, an unfreeze; skipped where Playwright is missing).

## D71. A submission is pending in the task score as soon as it arrives
The per-task aggregate (`participation_task_scores`, D22) was recomputed
only when a submission was scored, so a submission waiting to be judged
was not counted as pending there, while the full recomputation of the
ranking (every minute, and at every restart) counted it. The scoreboards
alternated between the two: every minute hundreds of rows flashed the
pending mark on and off, each flash an update of every row to every
spectator (found in the frames of the 10,000-spectator load test). The
dispatcher now recomputes the aggregate when it takes a new submission (or
a rejudged one), so both computations agree (`TestPendingCountsOnArrival`)
and the contestant's task page counts it at once. It publishes no ranking
update for it: publishing one doubled the updates every spectator
receives (measured: the ranking server went from 18% to 41% of the web
core with 10,000 spectators), and the mark appears with the next update of
the board, which during a contest is a moment away. The cost is one
aggregate query and upsert per submission, in the transaction that
enqueues its compilation.

## D72. License: Apache-2.0
The project is an independent implementation (CMS, AGPL-3.0, was studied
only for behaviour and architecture, CLAUDE.md), so the license is
free to choose. Apache-2.0: permissive, so schools and olympiad
committees can adapt and host it without having to publish their
deployment changes (AGPL would oblige every organizer who customizes a
networked installation to do so, which in practice discourages use), and,
unlike MIT, with an explicit patent grant and terms for contributions.
Every dependency is MIT, BSD or Apache-2.0 and the bundled htmx is
Zero-Clause BSD (NOTICE), all compatible.

## D73. Releases: GoReleaser tarball per architecture, images on GHCR
A tag `vX.Y.Z` runs `.github/workflows/release.yml`: the lint and the
fast unit tests first, then GoReleaser (`.goreleaser.yaml`) and the
images in parallel. The binaries are static (`CGO_ENABLED=0`) for
linux/amd64 and linux/arm64, with the version stamped in. One tarball
per architecture, `cms_<version>_linux_<arch>.tar.gz`, unpacks into one
directory with everything an installation reads from disk: both
binaries, example configuration, language files, systemd units, the
installer and verify-host, the documentation, and the migrations,
templates and static files. The last three are also embedded in the
binary; they ship so an operator can review them. `checksums.txt`
(SHA-256) is what both the installer and `cmsctl upgrade` verify
before unpacking anything. The changelog comes from the commits since
the previous tag. The images are
`ghcr.io/<owner>/<repo>/cms` and `/worker` (GHCR requires lower case, so
`${GITHUB_REPOSITORY,,}`), built by buildx for both architectures (QEMU
for arm64; the Go part cross-compiles on the build platform, so only the
apt steps are emulated), tagged `X.Y.Z`, `X.Y` and `latest` (not for
pre-releases). No separate dispatcher image: the `cms` image runs every
non-judging service, the `worker` image adds isolate and the compilers.
`internal/cli.TestReleaseConfiguration` guards the configuration (every
listed file exists, both architectures, the trigger, the image matrix);
the CI job `release-snapshot` builds a real snapshot and
`scripts/check-release.sh` checks it.

## D74. One-line installer: releases under /opt/cms, dry run, blockers
`scripts/install.sh` is meant to be piped (`curl … | sudo bash`), so the
whole script is functions with `main "$@"` on the last line: a truncated
download runs nothing. It installs releases side by side in
`/opt/cms/releases/<version>` with `/opt/cms/current` pointing at the one
in use, and `/usr/local/bin/cms` and `cmsctl` link through `current`.
Switching versions and rolling back are then a single symlink change
(D76), and the three most recent releases stay on disk. It checks everything it can
before changing anything. It supports Ubuntu 22.04/24.04 and Debian 12
on amd64/arm64. It refuses containers (OpenVZ, LXC, Docker), WSL and
cgroup v1. On cgroup v1 it explains how to switch; with
`--enable-cgroup-v2` it edits GRUB and asks for a reboot, and it never
reboots by itself. `--dry-run` prints every command and file it would
write and lists every blocker instead of stopping at the first, so one
run shows everything to fix. Re-running it reconfigures the installed
release: it keeps `/etc/cms/cms.yaml`, the secrets and the database, and
never downloads a different version. A different `--version` is refused
with a pointer to `cmsctl upgrade`, which takes the backup and handles
rollback. PostgreSQL and Valkey are tuned from the detected CPUs and
RAM. The tests run the real script piped from a fake release server
(`TestInstallFromRelease`), in addition to the rendered-files test.

## D75. Production Docker worker: minimal privileges, not --privileged
isolate needs to create namespaces, mount inside them, bring up the
loopback interface of each box and manage a cgroup v2 subtree. The worker
container therefore gets `CAP_SYS_ADMIN` (namespaces and mounts; Docker's
default seccomp profile relaxes for it), `CAP_NET_ADMIN` (the box's `lo`),
`apparmor:unconfined` (docker-default denies `mount`) and a private
cgroup namespace. Its entrypoint remounts `/sys/fs/cgroup` writable,
moves its own processes into a leaf and delegates the rest to isolate.
It is never `--privileged`: devices, the other capabilities and the
host's cgroup tree stay out of reach. The worker then drops to the
unprivileged `cms` user (uid/gid 2000 in both images, so volumes are
shared without chown games). The production stack
(`deploy/docker/compose.yml`, project `cms-production`) is separate from
the development one (`docker-compose.yml` + `cms.dev.yaml`, project
`cms`), so the two never share volumes. Its configuration carries no
secrets: they live in `.env`, written once by `setup.sh`.

## D76. cmsctl upgrade: contest check, backup, forward-only, rollback
Migrations only go forward, so the previous release cannot run against a
migrated schema. Rolling back therefore always means the previous
release *and* the database restored from the backup taken just before.
Order: resolve the version (downgrades refused), refuse while any
participation's window is still open (extra time and delay included,
`ListRunningContests`) unless `-force`, then download and verify the
checksum and unpack. All of this happens before the services are
touched, so a failure there changes nothing. Then: backup (kind
*upgrade*), stop `cms.target`, switch `current`, migrate with the *new*
binary, start, and wait until every enabled service answers `/healthz`.
A failure after the stop rolls back automatically; if the rollback itself
fails, the error names the backup to restore by hand. A remote worker
(`blob.backend: http`, no database) only switches and restarts: no
contest check, backup or migrations, since its judging jobs are
re-queued. Workers are upgraded after the main server, to the same
version. The Docker install upgrades by image tag instead (`setup.sh
--version`; `init` migrates before the services start).

## D77. First boot: no default administrator password
There is no `admin/admin` any more outside `make dev`. The installer and
`setup.sh` run `cms ctl bootstrap -generate-password`, which prints a
random 16-character password (no look-alike characters) once and stores
only its hash. `cms ctl admin-password` resets it the same way if lost.
The development stacks keep `admin/admin` for convenience, but it is
still caught at login. Any administrator who logs in with a well-known
default is flagged `password_change_required` (migration 0016). This
covers `admin`, `password`, `changeme`, `123456`… and a password equal
to the username. Until the password is changed, every admin page
redirects to the account page. Only that page, the password change and
logout are allowed. The flag is stored, not recomputed per request, so
the check costs nothing after login. It is set before the session exists,
so it cannot be skipped by racing requests. Creating or updating an
administrator with such a password is refused outright.

## D78. Installer packages: read the archive first, Caddy's repository where missing
The installer picks packages by what the system's archive offers: Java 21
or 17, Valkey or Redis, and Caddy. It used to check before refreshing the
package lists (a fresh cloud image has none, so every check failed), and
it assumed `caddy` exists everywhere, which is not true on Ubuntu 22.04.
It now runs `apt-get update` first. Where the archive has no `caddy`, it
adds Caddy's official repository: the signing key goes to
`/usr/share/keyrings/caddy-stable-archive-keyring.gpg` and the list to
`/etc/apt/sources.list.d/caddy-stable.list`, following Caddy's own
instructions. It does not fall back to nginx, because `--domain`
promises automatic HTTPS with Caddy; `--web nginx` remains the explicit
alternative. Every package the installer asks for was resolved against
the archives of Ubuntu 22.04 and 24.04 and Debian 12 and 13, and Caddy
2.11 installed from its repository on Ubuntu 22.04.
`CMS_INSTALL_APT_CACHE` lets the tests play a system without these
packages (`TestInstallFromRelease`).

## D79. Installer: Ubuntu 26.04, and the PostgreSQL cluster it actually finds
Ubuntu 26.04 LTS replaces GNU coreutils with uutils (Rust) and ships
PostgreSQL 18. The installer was run for real in an `ubuntu:26.04`
container, with a stand-in for systemctl that starts PostgreSQL and
Valkey directly. Everything it uses behaves like GNU. That includes
`stat -f` naming cgroup v2 `cgroup2fs`, `sort -V`, `sha256sum`, `seq`
and `install`. `find`, `sed`, `tar` and `cmp` are still GNU. The run
showed three real faults, none specific to uutils.
- The `ln` into `/usr/local/share/doc`, which minimal systems lack, and
  the dry run's PostgreSQL step both stopped the script silently under
  `set -e` on a machine without PostgreSQL yet.
- The PostgreSQL step assumed the newest `/etc/postgresql/<version>`
  cluster runs on 5432 and that `template1` is UTF-8. The published
  installer fails on two ordinary server states:
  - after a distribution upgrade, the old version's cluster keeps 5432
    (its server removed) and the new one gets 5433: `psql` cannot
    connect;
  - a cluster created under the C locale is SQL_ASCII: `createdb -E UTF8`
    is refused.

Now `pg_cluster` takes the `main` cluster of the newest PostgreSQL whose
server is installed, and uses its port everywhere (psql, createdb, the
URL in `cms.yaml`). It creates one in `C.UTF-8` when there is none. The
database is created from `template0` with `--locale C.UTF-8`, and when
PostgreSQL does not start the installer shows its log instead of
failing further on. The installer also sets `LC_ALL=C.UTF-8`, because a
locale forwarded by SSH but missing on the server floods apt and
PostgreSQL's scripts with warnings that read like errors. Each state was
run in a 26.04 container: fresh, no cluster, SQL_ASCII, upgraded.
`TestInstallPostgresCluster` covers the choice of cluster.

## D80. Valkey on a free port when 6379 is taken
Reported from an Ubuntu 26.04 server: valkey-server failed to start
("bind: Address already in use") because `redis-server`, most likely left
by an earlier installer run that fell back to Redis, already listened on
127.0.0.1:6379. A personal or shared server can also have another
application's Redis, or a container, there. The installer must not stop
or remove a program it did not configure, so it moves instead:
`kv_port` keeps 6379 when it is free or already held by the store it
configures, and otherwise takes the next free port up to 6399. It says
so, writes `port` into the store's `cms.conf` and the URL into
`cms.yaml`, and opens it in the firewall with `--private-ip`. An
existing `cms.yaml` is pointed at the current PostgreSQL and Valkey
ports (`sync_ports`). The file is otherwise never rewritten, but a
wrong port there would break every service. `--redis-port` sets the
port explicitly; workers pass the main server's.

When the store still does not start, the installer prints its journal,
its log and the program holding the port, instead of systemd's generic
line. It first clears systemd's restart limit, which earlier failures
trip. Testing this showed that the PostgreSQL and Valkey log output
(`… 2>/dev/null >&2`) sent everything to /dev/null: redirections apply
left to right, so it is now `>&2 2>/dev/null`. The user's exact state
was reproduced in an Ubuntu 26.04 container, with the archive's
redis-server on 6379 and a cms.yaml from the failed run. The published
installer fails the same way; the fixed one finishes on 6380, with Redis
untouched. `TestInstallValkeyPort` covers the port choice and
`sync_ports`.

## D81. Sharing the machine: web ports, the firewall and the Caddyfile
The same Ubuntu 26.04 server runs Nextcloud AIO, whose containers publish
ports 80, 443 and 8080. With `--lan`, CMS's Caddy needs 80, 8080 and 8081,
so it would fail next, after the packages, the database and the services
were already set up. Its firewall step would also have enabled `ufw` deny
by default on a machine that serves other things, which cuts them off (and
the administrator too, with SSH on another port).

- **Web ports are checked first.** `detect()` checks the ports the web
  server needs (on a LAN the three site ports, for a domain 80 and 443)
  and names the program holding each. Like every blocker, it stops a real
  run before anything changes and is listed by `--dry-run`. The web
  server CMS configures (a re-run) is not a conflict.
- **`--http-ports C,R,A`** moves the contest, ranking and admin sites on a
  LAN (default 80,8080,8081). It is refused with `--domain` (ACME needs
  80/443; behind an existing proxy is the way there) and for 8888-8891,
  where the CMS services themselves listen on 127.0.0.1. Picking free
  ports automatically was rejected: unlike Valkey's port, which only CMS
  reads, these are the addresses people type, so they must be chosen and
  known.
- **The firewall is not enabled over other services.** When `ufw` is not
  active yet and anything else listens on a non-loopback address, TCP or
  UDP, the step names each listener with its port, prints the ports CMS
  needs and the commands, and leaves the decision to the administrator.
  Not counted: SSH, WireGuard, containers' published ports
  (`docker-proxy`; Docker bypasses `ufw` for them anyway), CMS's own
  services and the usual DHCP, mDNS and time daemons. Sockets without a
  program are the kernel's (WireGuard, NFS) and are named by port;
  `systemd` is counted unless the port is SSH's (socket activation).
  When it does enable `ufw`, it also allows every port `sshd` or
  `ssh.socket` listens on (not only 22) and WireGuard's listen ports
  (`wg show`), so neither the administrator nor the workers are locked
  out.
- **A Caddyfile the installer did not write** (the package's example, or
  another site's) is copied once to `Caddyfile.before-cms`, and
  `--uninstall` puts it back.

Verified in an Ubuntu 26.04 container with listeners on 80, 443 and 8080
named `docker-proxy` and a Redis on 6379. The default ports are blocked
before any change, with `--http-ports` and the example. With
`--http-ports 8000,8001,8002` it installs; contest, ranking and admin
answer through Caddy while :80 stays with the other program. With
another service (a listener on 3000) the firewall is left off, and a
re-run is clean. A custom Caddyfile is kept and restored by
`--uninstall`. `TestInstallWebPorts` covers the port check, the option's
validation, the rendered Caddy/nginx configuration and both firewall
paths: TCP, UDP, kernel and `systemd` listeners, SSH on 2222 and a
WireGuard tunnel.

## D82. The host check on a hyperthreaded server (SPEC_IOI, H0)
The first real installation (Ubuntu 26.04, 8 CPUs) failed `cms-verify-host`:
in the second run `fork_bomb_64_procs` was stopped by the memory limit
instead of a time limit, so it both "failed" and "changed between runs".

- **Fork bombs.** Every fork refused by the process limit still allocates
  the child's task structure and kernel stack, charged to the box's memory
  control group and freed after an RCU grace period. 64 processes forking in
  a loop can pile those up to the 256 MiB limit before the second of CPU
  runs out; whether they do depends on the kernel's timing. Both outcomes
  kill the whole box and leave no process behind (checked), so both are
  "contained". Security cases are compared between runs by that question
  (did the sandbox hold), not by which limit fired; samples are still
  compared by verdict, where a TLE/MLE flip is real instability.
- **One judging CPU per physical core.** The installer judged on every CPU
  after the web ones. On that machine (4 cores × 2 threads, siblings 0/4,
  1/5, ...) CPUs 2–7 include pairs of siblings and siblings of the web CPUs,
  so each judging box ran next to another box or the web server on the
  same core: unstable times, the very thing the second run measures. The
  layout now comes from `thread_siblings_list`: the web keeps whole cores
  (the first; the first two from six cores up), and every other core judges
  on its first CPU with the siblings idle. Machines without SMT get the
  same layout as before. An existing `cms.yaml` whose `worker.cores` is
  exactly the old default is moved (cores chosen by hand are kept, with a
  note); the self-test warns about sibling judging CPUs.
  `--judge-all-threads` keeps the old behaviour for those who prefer
  throughput. Leaving siblings idle halves the judging CPUs on SMT
  machines; SPEC_IOI §2.2 asks exactly for that, and a medal decided by
  timing noise costs more than a longer queue.
- **The worker pauses during the check.** The installer starts the services
  and then runs the check, so the worker could be judging at the same time
  (one of the six warnings). It is stopped for the check and started again.
- **Host tuning is opt-in.** `cms-host-tuning` (and `--tune-host`) sets the
  performance governor and turns turbo boost and transparent huge pages
  off, persistently through a oneshot unit. ASLR and SMT only change when
  set to `off` in `/etc/cms/host-tuning.conf`. It is not the default
  because the installer also runs on shared servers (the owner's runs
  Nextcloud): turbo and huge pages are machine-wide, and turning ASLR off
  weakens every internet-facing program on the machine.

## D83. Statements: one source, rendered on the server (SPEC_IOI H1)
The owner reported LaTeX errors, statements not in PDF, samples offered as
downloads, and statements that did not change after an upload. Causes:
statements were files linked from the task page; Markdown/text files were
offered raw; Polygon's HTML statements rely on MathJax, which the strict
CSP (no inline or foreign scripts) blocks, so every formula showed as TeX;
the samples of converted packages were attachments; and statements were
served from a fixed address with `max-age=3600`.

- **One document model, two renderers.** `internal/statement` reads
  Markdown (CommonMark basics, GFM tables), LaTeX (documents, fragments,
  olymp.sty problems, Polygon sections) and HTML (Polygon's layout; active
  content dropped) into blocks and inlines, with TeX formulas parsed into a
  math tree. The task page gets HTML with **MathML**, which current
  browsers lay out natively: no JavaScript, nothing to load, and the CSP
  stays strict. KaTeX/MathJax were rejected: 75 KB+ of JavaScript and fonts
  against the SPEC's 30 KB budget, and client-side rendering on 400
  machines at the start of a contest.
- **PDF typeset by the CMS.** The same tree is typeset into A4 pages by the
  existing PDF writer, extended with the standard Times, Helvetica, Courier
  and Symbol fonts (widths from Adobe's core-14 metrics): title, a box with
  the limits and I/O files, justified text with inline formulas, TeX-style
  math boxes (fractions, radicals, scripts, limits, stretchy delimiters
  drawn as curves, matrices), lists, tables, code, images and the examples
  as Input/Output columns, with page numbers. Nothing is embedded, so a
  statement is a few KiB. Limitation: the standard fonts cover Western
  European text; other scripts show fully on the page and need an uploaded
  PDF for print. Shelling out to LaTeX or a browser was rejected: hundreds
  of MB on every server, and one more thing to break on contest day.
- **Examples belong to the task** (`task_examples`: input and output
  digests and an optional Markdown note), shown in every statement, on the
  page and in the PDF. They come from the admin (typed, files, or any
  testcase), from packages (`statement/examples/`), from italy_yaml sample
  pairs in `att/` (no longer attachments) and from Polygon (`example.NN`,
  else sample tests). Polygon's LaTeX sections become the statement (a
  generated `.tex`), preferred over its PDF/HTML.
- **Always the current version.** Renderings are cached (HTML at first
  view, PDF at first download) under a key hashing everything they depend
  on: the source digest, examples, limits, task and contest names,
  attachments. The key is the `?v=` of the links, served `immutable`; any
  unversioned address (bookmarks, the old URL) is `no-cache` with an ETag.
  Attachments follow the same scheme with their digest.
- **An editor.** Statements are written in the admin with a live preview
  (htmx, 600 ms after typing stops) listing what was not understood, and a
  PDF preview. Uploads accept `.tex` too.

While adding the examples table, the blob garbage collector turned out to
miss `certificate_templates.logo_digest`: `cmsctl blobs-gc` would have
deleted certificate logos. Both are now referenced.

## D84. Results next to the button; the Testing page (SPEC_IOI H1)
"You cannot easily see the result of your submission": the submit form
swapped the submissions table at the bottom of the page and said nothing
next to the button. The task page now has a side column with the form, a
**latest result** card (status, progress, score, one chip per subtask,
the first lines of a compilation error, a link to the details) and the
limits. Submitting replaces the card with the new submission's (the list
below updates out of band) and the card follows the live events of its
task's newest submission. The chips come from the public score details
already stored, so the card costs one query more per task page.

The "Testing" menu entry pointed to `/testing`, which did not exist (tests
lived only on the task pages). The page now exists: a tab per task with its
test form and every task's tests; a test run from it comes back to it. A
test (`TestLoginAndPages` and the new ones) now fetches every page linked
from the menus.

