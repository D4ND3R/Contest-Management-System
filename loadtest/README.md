# Load tests

`loadtest/run.sh` (or `make loadtest`) plays a contest against a complete
CMS installation laid out like the reference 2 vCPU machine
(`docs/en/deployment.md`):

| CPU | Runs |
|-----|------|
| `WEB_CPU` (0) | contest web server, ranking web server, dispatcher, monitor, PostgreSQL, Valkey |
| `JUDGE_CPU` (1) | the sandbox (the worker's boxes; `worker.cores: [1]`) |
| `K6_CPUS` (2,3) | k6, so the load generator does not steal from CMS |

PostgreSQL runs with the production settings (durable commits,
`shared_buffers` 512 MB, no parallel workers, no JIT) and Valkey with its
append-only file, as the install script configures them. The script
builds `cms`, creates a throwaway database, imports the documented
`batch-suma` example as the task of a contest "load" and creates the
contestants (`loadtest/seed`), starts the services pinned with `taskset`,
runs `contest.js`, waits for the judging queue to drain and writes a
report (`loadtest/report`) to `loadtest/results/<UTC time>/` with the raw
k6 summary, per-minute judging counts and resource samples.

It needs root (isolate), PostgreSQL and Valkey (or Redis) binaries, k6,
Go and a machine with at least 4 CPUs. The per-IP login limit is raised in
the test configuration because every virtual contestant comes from
127.0.0.1.

## The scenario (`contest.js`)

- **Contestants** (`CONTESTANTS`, 500): log in during the first minute
  (argon2id verification included), then every 5–15 s open the overview,
  the task or their submissions, and submit every 2–4 minutes. In the last
  `BURST_MIN` minutes (3) each submits every 20–40 s — the final rush.
  Submissions mix C++ with `<bits/stdc++.h>` (the expensive compile), lean
  C++ and Python, with some wrong answers.
- **Open pages**: every contestant also keeps the contest page's event
  stream (server-sent events) open, reconnecting every minute.
- **Spectators** (`SPECTATORS`, 1000): open the public ranking page and
  keep its event stream open (reloading the page now and then).

Durations: `NORMAL_MIN` (5) + `BURST_MIN` (3). Thresholds (the test fails
when they are not met): contest pages p95 < 150 ms and p99 < 400 ms,
submit p95 < 300 ms, ranking page p95 < 100 ms, logins p95 < 2 s, under 1%
failed requests.

```sh
sudo loadtest/run.sh                                   # 500 contestants, 1000 spectators
sudo CONTESTANTS=1500 SPECTATORS=2000 loadtest/run.sh  # find the limit
sudo HOLD=1 CONTESTANTS=5 loadtest/run.sh              # start everything and wait (debugging)
```

## Results

`loadtest/results/` keeps every run's files locally (not versioned); the
reference runs are summarised here. Host: 4 CPUs (Xeon @ 2.10 GHz),
15 GiB; CMS confined to two of them as described above.

### 500 contestants and 1,000 spectators (SPEC_CLOSE F1)

Run `20260925T2001Z`, 5 + 3 minutes: every threshold met, no failed
request, 2,072 submissions accepted (none rejected).

| Requests | count | k6 p50 | k6 p95 | k6 p99 | server p95 | server p99 |
|----------|------:|-------:|-------:|-------:|-----------:|-----------:|
| Contest pages (overview, task, submissions) | 23,442 | 2.6 | 42.4 | 75.3 | 18.9¹ | 65.7¹ |
| Submit | 4,144 | 4.0 | 62.6 | 132.0 | ¹ | ¹ |
| Login (argon2id) | 6,504 | 29.6 | 95.4 | 167.2 | 112.6 | 226.7 |
| Ranking page | 1,787 | 0.5 | 12.1 | 27.8 | 10.4 | 22.8 |

Milliseconds. "k6" is what the load generator measured, "server" the
servers' own request histograms (¹ pages and submissions together: 94% of
them under 15 ms, 98% under 40 ms). The web core was busy 42% on average
(84% in the busiest 5 seconds): the contest web server 28% of it,
PostgreSQL 4%, the ranking server 3%, the dispatcher 2%. Peak resident
memory of every CMS process together: 371 MiB.

**Judging** (run `20260925T1753Z`, the same scenario judged until the
queue drained): **70–86 submissions per minute on the one judging core**
(C++ with `<bits/stdc++.h>` takes ~1.5 s to compile and dominates), the
same during the load and after it: the web side never slowed judging down
and the judging backlog never slowed the web side down. The final burst
(1,000 submissions in the last 2 minutes) left 1,535 submissions waiting;
the last was judged 20 minutes after the contest ended (p50 8.5 min, p95
19.4 min from submission to score). One judging core is this machine's
limit: a contest this size needs [more judging cores or
workers](../docs/en/external-worker.md) to score in seconds. The first run
of this scenario found a real bug: evaluations starved behind compilations
and scoring stopped for 30 minutes (DECISIONS.md D67, fixed). The later
changes (D70, D71) do not touch the judging core.

### How many contestants the machine supports

Run `20260925T2010Z`, 1,000 contestants and 1,000 spectators: contest
pages p95 132 ms / p99 239 ms (k6; server p95 95 ms), submit p95 229 ms,
ranking page p95 34 ms, no failed request, 4,145 submissions accepted; the
web core was busy 72% on average and saturated at the start and in the
final burst. The one threshold missed was logins: 1,000 contestants
logging in within the same minute take up to 12 s (p95), because each
argon2id check costs ~30 ms of the one web core.

So the reference 2 vCPU machine serves **about 500 contestants with ample
margin** and **up to about 1,000** with pages still under 150 ms (p95),
if logins are spread over a few minutes (let contestants in before the
start). Judging, not the web, is the tighter limit: plan one judging core
per 70–80 submissions a minute of heavy C++ at the peak.

### 10,000 more ranking spectators (SPEC F7)

`SSE_CLIENTS=10000` adds 10,000 ranking event streams
(`loadtest/spectators`) to the 500 contestants and 1,000 spectators, on
the same web core. The first run saturated the core and every latency
degraded (fan-out of one update: p50 1.8 s): the ranking server spent 81%
of its time writing whole rows to every stream, and every minute a
recomputation flickered the pending marks of hundreds of rows. After the
fixes (DECISIONS.md D70, D71), run `20260925T1952Z`:

- 311 updates reached all 10,000 streams, none refused or dropped; the
  last spectator had each one 0.40 s (p50), 0.69 s (p99), 0.76 s (max)
  after the first.
- The ranking server used 18% of the web core (from 3% without them):
  one write to 10,000 connections per update, mostly kernel time on
  loopback, which also charges the receivers' side to this core.
- Contest pages: server p95 46 ms / p99 174 ms (from 19 / 66 ms); k6 p95
  74 ms. The ranking page, rendered on the same core, missed its own
  threshold (k6 p95 315 ms).

With that many spectators, run the ranking web server on its own core or
machine ([ranking](../docs/en/ranking.md)): the contestants then keep the
baseline figures.
