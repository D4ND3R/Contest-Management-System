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
