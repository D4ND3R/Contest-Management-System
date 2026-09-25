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

See the latest measured run below; `loadtest/results/` keeps every run's
files locally (not versioned).
