#!/usr/bin/env bash
# Load test of a contest on the reference 2 vCPU layout (docs/en/deployment.md):
# CPU $WEB_CPU runs the web servers, the dispatcher, PostgreSQL and Valkey,
# CPU $JUDGE_CPU the sandbox; k6 runs on $K6_CPUS so it does not steal
# from them. PostgreSQL and Valkey use the production settings (durable
# commits, append-only queues).
#
#   loadtest/run.sh                 # 500 contestants, 1000 spectators
#   CONTESTANTS=300 SPECTATORS=0 NORMAL_MIN=3 BURST_MIN=2 loadtest/run.sh
#   CONTESTANTS=1500 SPECTATORS=3000 DRAIN_MAX=0 loadtest/run.sh  # web capacity
#   SSE_CLIENTS=10000 loadtest/run.sh  # plus 10,000 streams following the ranking
#   PROFILE_AT=240 loadtest/run.sh     # CPU profiles of the web servers 4 minutes in
#
# Needs root (isolate), PostgreSQL and Valkey/Redis binaries, k6 and Go.
# Results (k6 summary, judging throughput, report.md) go to
# loadtest/results/<UTC time>/.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"
: "${CONTESTANTS:=500}" "${SPECTATORS:=1000}" "${NORMAL_MIN:=5}" "${BURST_MIN:=3}"
: "${SSE_CLIENTS:=0}"  # extra ranking event streams held by loadtest/spectators
: "${PROFILE_AT:=}"  # seconds into the run: take 20 s CPU profiles of both web servers
: "${DRAIN_MAX:=1800}" # seconds to wait for the judging queue after k6 (0: do not wait)
: "${WEB_CPU:=0}" "${JUDGE_CPU:=1}" "${K6_CPUS:=2,3}"
: "${PGPORT:=15432}" "${REDISPORT:=16379}"
STAMP=$(date -u +%Y%m%dT%H%MZ)
OUT="$ROOT/loadtest/results/$STAMP"
RUN="$ROOT/.cache/loadtest"
mkdir -p "$OUT"
rm -rf "$RUN" && mkdir -p "$RUN"
chmod 755 "$RUN"

pgbin() { command -v pg_ctl >/dev/null && dirname "$(command -v pg_ctl)" || ls -d /usr/lib/postgresql/*/bin | sort -V | tail -1; }
PGBIN=$(pgbin)
REDIS=$(command -v valkey-server || command -v redis-server)
RCLI=$(command -v valkey-cli || command -v redis-cli)
as_pg() { if [ "$(id -u)" = 0 ]; then runuser -u postgres -- "$@"; else "$@"; fi; }
pids=()
cleanup() {
  for p in "${pids[@]}" ${spect:-} ${sampler:-}; do kill "$p" 2>/dev/null || true; done
  wait 2>/dev/null || true
  as_pg "$PGBIN/pg_ctl" -D "$RUN/pg" -m fast stop >/dev/null 2>&1 || true
  "$RCLI" -p "$REDISPORT" shutdown nosave >/dev/null 2>&1 || true
}
trap cleanup EXIT
trap "exit 130" INT TERM

echo "== PostgreSQL and Valkey on CPU $WEB_CPU (production settings)"
chown postgres "$RUN" 2>/dev/null || true
as_pg "$PGBIN/initdb" -D "$RUN/pg" -U postgres --auth=trust -E UTF8 --no-locale >/dev/null
cat >> "$RUN/pg/postgresql.conf" <<CONF
listen_addresses = '127.0.0.1'
port = $PGPORT
unix_socket_directories = '$RUN'
shared_buffers = 512MB
effective_cache_size = 2GB
work_mem = 8MB
max_connections = 100
max_parallel_workers_per_gather = 0
jit = off
synchronous_commit = on
wal_compression = on
random_page_cost = 1.1
CONF
as_pg taskset -c "$WEB_CPU" "$PGBIN/pg_ctl" -D "$RUN/pg" -l "$RUN/pg.log" -w start >/dev/null
taskset -c "$WEB_CPU" "$REDIS" --port "$REDISPORT" --bind 127.0.0.1 --daemonize yes --appendonly yes --appendfsync everysec \
  --maxmemory-policy noeviction --dir "$RUN" --pidfile "$RUN/redis.pid" --logfile "$RUN/redis.log" >/dev/null
for _ in $(seq 1 50); do "$RCLI" -p "$REDISPORT" ping >/dev/null 2>&1 && break; sleep 0.1; done
as_pg "$PGBIN/psql" -h 127.0.0.1 -p "$PGPORT" -U postgres -qc "CREATE DATABASE cms" >/dev/null

echo "== build, migrate, seed $CONTESTANTS contestants"
go build -o "$RUN/cms" ./cmd/cms
go build -o "$RUN/spectators" ./loadtest/spectators
export CMS_CONFIG="$RUN/cms.yaml"
cat > "$CMS_CONFIG" <<YAML
log: {level: warn, format: json}
database: {url: "postgres://postgres@127.0.0.1:$PGPORT/cms?sslmode=disable", max_conns: 16}
redis: {url: "redis://127.0.0.1:$REDISPORT/0"}
blob: {backend: local, local_dir: "$RUN/blobs"}
secret_key: "6c6f61642d746573742d7365637265742d6b65792d6e6f742d666f722d70726f64"
languages_dir: "$ROOT/config/languages"
contest_web: {listen: "127.0.0.1:18888", login_rate_limit_per_minute: 100000, rate_limit_per_minute: 1000, pprof: true}
admin_web: {listen: "127.0.0.1:18889"}
ranking_web: {listen: "127.0.0.1:18890", data_dir: "$RUN/ranking", push_token: load-token, pprof: true}
dispatcher: {metrics_listen: "127.0.0.1:19101", ranking_urls: ["http://127.0.0.1:18890"]}
worker:
  metrics_listen: "127.0.0.1:19102"
  isolate_path: "$(command -v isolate)"
  cores: [$JUDGE_CPU]
  work_dir: "$RUN/worker"
  cache_dir: "$RUN/worker-cache"
monitor: {metrics_listen: "127.0.0.1:19103"}
YAML
"$RUN/cms" ctl migrate >/dev/null
go run ./loadtest/seed -users "$CONTESTANTS" -minutes $((NORMAL_MIN + BURST_MIN + 30))

echo "== services: web, dispatcher, monitor on CPU $WEB_CPU; sandbox on CPU $JUDGE_CPU"
start() { taskset -c "$1" "$RUN/cms" "${@:2}" >"$RUN/$2.log" 2>&1 & pids+=($!); }
start "$WEB_CPU" contest-web
start "$WEB_CPU" ranking-web
start "$WEB_CPU" dispatcher
start "$WEB_CPU" monitor
start "$WEB_CPU,$JUDGE_CPU" worker
for port in 18888 18890; do
  for _ in $(seq 1 100); do curl -fsS "http://127.0.0.1:$port/healthz" >/dev/null 2>&1 && break; sleep 0.1; done
done
sleep 3

if [ -n "${HOLD:-}" ]; then
  echo "== holding: contest web http://127.0.0.1:18888/load/ (load0001 / load-pw), ranking http://127.0.0.1:18890/load/; Ctrl-C stops"
  wait
fi

echo "== k6 on CPUs $K6_CPUS: $CONTESTANTS contestants, $SPECTATORS spectators, ${NORMAL_MIN}+${BURST_MIN} minutes"
psql_q() { as_pg "$PGBIN/psql" -h 127.0.0.1 -p "$PGPORT" -U postgres -d cms -XAtqc "$1"; }
# busy percentage of CPU $1 since the previous call
cpu_busy() {
  local u n sy i w q sq st
  read -r _ u n sy i w q sq st _ < <(grep "^cpu$1 " /proc/stat)
  local busy=$((u + n + sy + q + sq + st)) total=$((u + n + sy + i + w + q + sq + st))
  local prev; prev=$(cat "$RUN/cpu$1" 2>/dev/null || echo "$busy $total")
  echo "$busy $total" > "$RUN/cpu$1"
  local db=$((busy - ${prev% *})) dt=$((total - ${prev#* }))
  if [ "$dt" -gt 0 ]; then echo $((100 * db / dt)); else echo 0; fi
}
# every 5 s: the phase (k6 running or draining), how busy each core was,
# the memory of the CMS processes and the judging backlog
sample() {
  while :; do
    local k6=0 c b
    for c in ${K6_CPUS//,/ }; do b=$(cpu_busy "$c"); [ "$b" -gt "$k6" ] && k6=$b; done
    echo "$(date -u +%T) phase=$(cat "$RUN/phase") web_cpu=$(cpu_busy "$WEB_CPU") judge_cpu=$(cpu_busy "$JUDGE_CPU") k6_cpu=$k6 rss_mib=$(ps -o rss= -p "$(IFS=,; echo "${pids[*]}")" | awk '{r+=$1} END {printf "%d", r/1024}') backlog=$(psql_q "SELECT count(*) FROM submission_results WHERE scored_at IS NULL")"
    sleep 5
  done
}
echo load > "$RUN/phase"
sample > "$OUT/samples.txt" & sampler=$!
# CPU seconds used so far by each CMS service, PostgreSQL and Valkey
cpu_by_process() {
  ps -eo times=,args= | awk '
    $2 ~ /\/cms$/ {n[$3] += $1; next}
    $2 ~ /^postgres/ || $2 ~ /bin\/postgres$/ {n["postgresql"] += $1; next}
    $2 ~ /(valkey|redis)-server/ {n["valkey"] += $1}
    END {for (k in n) print k, n[k]}' | sort
}
cpu_by_process > "$RUN/cpu-start.txt"
K6_START=$(date -u +%s)
if [ -n "$PROFILE_AT" ]; then
  (sleep "$PROFILE_AT"
   for p in 18888:cws 18890:rws; do
     curl -fsS -o "$OUT/${p#*:}-cpu.pprof" "http://127.0.0.1:${p%:*}/debug/pprof/profile?seconds=20" &
   done
   wait) & profiler=$!
fi
if [ "$SSE_CLIENTS" -gt 0 ]; then
  taskset -c "$K6_CPUS" "$RUN/spectators" -url http://127.0.0.1:18890/load/events -n "$SSE_CLIENTS" \
    -for "$((NORMAL_MIN + BURST_MIN))m" -out "$OUT/sse.txt" & spect=$!
fi
taskset -c "$K6_CPUS" k6 run --quiet --summary-export "$OUT/k6-summary.json" \
  -e CONTESTANTS="$CONTESTANTS" -e SPECTATORS="$SPECTATORS" -e NORMAL_MIN="$NORMAL_MIN" -e BURST_MIN="$BURST_MIN" \
  loadtest/contest.js 2>&1 | tee "$OUT/k6.txt" || true
K6_END=$(date -u +%s)
cpu_by_process | join -a1 - "$RUN/cpu-start.txt" | awk -v s=$((K6_END - K6_START > 0 ? K6_END - K6_START : 1)) '{printf "%s cpu_s=%d share=%.0f%%\n", $1, $2-$3, 100*($2-$3)/s}' > "$OUT/cpu-by-process.txt"
echo drain > "$RUN/phase"
[ -n "${spect:-}" ] && { wait "$spect" || true; }
[ -n "${profiler:-}" ] && { wait "$profiler" || true; }
curl -fsS http://127.0.0.1:18888/metrics > "$OUT/cws-metrics.txt" || true
curl -fsS http://127.0.0.1:18890/metrics > "$OUT/rws-metrics.txt" || true

echo "== waiting for the judging queue to drain"
DRAIN_START=$(date -u +%s)
while [ "$(psql_q "SELECT count(*) FROM submission_results WHERE scored_at IS NULL")" != 0 ]; do
  [ $(( $(date -u +%s) - DRAIN_START )) -ge "$DRAIN_MAX" ] && break
  sleep 5
done
DRAINED=$(date -u +%s)
PENDING=$(psql_q "SELECT count(*) FROM submission_results WHERE scored_at IS NULL")
kill "$sampler" 2>/dev/null || true

psql_q "SELECT count(*) FROM submissions" > "$OUT/submissions.txt"
psql_q "SELECT to_char(date_trunc('minute', scored_at), 'HH24:MI'), count(*) FROM submission_results WHERE scored_at IS NOT NULL GROUP BY 1 ORDER BY 1" > "$OUT/judged-per-minute.txt"
psql_q "SELECT to_char(date_trunc('minute', submitted_at), 'HH24:MI'), count(*) FROM submissions GROUP BY 1 ORDER BY 1" > "$OUT/submitted-per-minute.txt"
psql_q "SELECT round(percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM r.scored_at - s.submitted_at))::numeric, 1),
               round(percentile_cont(0.95) WITHIN GROUP (ORDER BY extract(epoch FROM r.scored_at - s.submitted_at))::numeric, 1),
               round(max(extract(epoch FROM r.scored_at - s.submitted_at))::numeric, 1)
        FROM submission_results r JOIN submissions s ON s.id = r.submission_id WHERE r.scored_at IS NOT NULL" > "$OUT/judge-latency.txt"
cat > "$OUT/meta.txt" <<META
contestants=$CONTESTANTS spectators=$SPECTATORS sse_clients=$SSE_CLIENTS normal_min=$NORMAL_MIN burst_min=$BURST_MIN
web_cpu=$WEB_CPU judge_cpu=$JUDGE_CPU k6_cpus=$K6_CPUS
k6_seconds=$((K6_END - K6_START)) drain_seconds=$((DRAINED - K6_END)) pending=$PENDING
host_cpus=$(nproc) host_mem_gib=$(free -g | awk '/Mem:/ {print $2}') kernel=$(uname -r)
cpu_model=$(grep -m1 'model name' /proc/cpuinfo | cut -d: -f2 | xargs)
META
go run ./loadtest/report "$OUT" > "$OUT/report.md"
cat "$OUT/report.md"
