#!/usr/bin/env bash
# Installs CMS on a clean Debian 12+/Ubuntu 22.04+ machine, or brings an
# existing installation up to date. Safe to run again: secrets are generated
# once (/etc/cms/secrets.env), cms.yaml is only written when missing, the
# other generated files are rewritten identically.
#
# Main server (web + database + dispatcher + one judging core):
#   sudo scripts/install.sh --domain cms.example.org --email you@example.org
#   sudo scripts/install.sh --lan                       # plain HTTP on a LAN
#
# Extra worker on another machine (see docs/en/external-worker.md):
#   sudo scripts/install.sh --role worker --main 10.8.0.1 \
#        --redis-password ... --blob-token ...
#
# Options:
#   --role main|worker        what this machine is (default main)
#   --domain NAME             contest site; admin.NAME and ranking.NAME unless
#   --admin-domain NAME       these two are given
#   --ranking-domain NAME
#   --email ADDRESS           Let's Encrypt account
#   --lan                     no domain, plain HTTP: contest :80, ranking :8080, admin :8081
#   --web caddy|nginx         reverse proxy with automatic HTTPS (default caddy)
#   --admin-allow CIDR        only these addresses reach the admin site
#   --private-ip IP           also listen on this private address for external
#                             workers (Redis and the blob server)
#   --languages minimal|full  toolchains: C, C++, Python, Java | all twelve
#   --main IP                 (worker) private address of the main server
#   --redis-password P        (worker) from the main server's /etc/cms/secrets.env
#   --blob-token T            (worker) idem
#   --worker-name NAME        (worker) default: the hostname
#   --no-firewall             leave the firewall alone
#   --render-only DIR         write every generated file under DIR and change
#                             nothing else (review, tests)
#   --cpus N / --ram-mb N     pretend the machine has N CPUs / N MiB (render)
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
ROLE=main
DOMAIN="" ADMIN_DOMAIN="" RANKING_DOMAIN="" EMAIL="" LAN=0 WEB=caddy ADMIN_ALLOW=""
PRIVATE_IP="" LANGS=minimal MAIN="" REDIS_PASSWORD_ARG="" BLOB_TOKEN_ARG="" WORKER_NAME=""
FIREWALL=1 RENDER="" CPUS="" RAM_MB=""

while [ $# -gt 0 ]; do
  case "$1" in
    --role) ROLE=$2; shift 2 ;;
    --domain) DOMAIN=$2; shift 2 ;;
    --admin-domain) ADMIN_DOMAIN=$2; shift 2 ;;
    --ranking-domain) RANKING_DOMAIN=$2; shift 2 ;;
    --email) EMAIL=$2; shift 2 ;;
    --lan) LAN=1; shift ;;
    --web) WEB=$2; shift 2 ;;
    --admin-allow) ADMIN_ALLOW=$2; shift 2 ;;
    --private-ip) PRIVATE_IP=$2; shift 2 ;;
    --languages) LANGS=$2; shift 2 ;;
    --main) MAIN=$2; shift 2 ;;
    --redis-password) REDIS_PASSWORD_ARG=$2; shift 2 ;;
    --blob-token) BLOB_TOKEN_ARG=$2; shift 2 ;;
    --worker-name) WORKER_NAME=$2; shift 2 ;;
    --no-firewall) FIREWALL=0; shift ;;
    --render-only) RENDER=$2; shift 2 ;;
    --cpus) CPUS=$2; shift 2 ;;
    --ram-mb) RAM_MB=$2; shift 2 ;;
    -h|--help) sed -n '2,42p' "$0"; exit 0 ;;
    *) echo "unknown option $1 (see --help)" >&2; exit 2 ;;
  esac
done

die() { echo "error: $*" >&2; exit 1; }
step() { printf '\n==> %s\n' "$*"; }
case "$ROLE" in main|worker) ;; *) die "--role must be main or worker" ;; esac
case "$WEB" in caddy|nginx) ;; *) die "--web must be caddy or nginx" ;; esac
case "$LANGS" in minimal|full) ;; *) die "--languages must be minimal or full" ;; esac
if [ "$ROLE" = main ] && [ -z "$DOMAIN" ] && [ "$LAN" = 0 ]; then
  die "give --domain NAME (HTTPS) or --lan (plain HTTP on a local network)"
fi
if [ "$ROLE" = worker ] && { [ -z "$MAIN" ] || [ -z "$REDIS_PASSWORD_ARG" ] || [ -z "$BLOB_TOKEN_ARG" ]; }; then
  die "a worker needs --main, --redis-password and --blob-token (from the main server's /etc/cms/secrets.env)"
fi
[ -n "$DOMAIN" ] && ADMIN_DOMAIN=${ADMIN_DOMAIN:-admin.$DOMAIN} && RANKING_DOMAIN=${RANKING_DOMAIN:-ranking.$DOMAIN}

# In render-only mode every path is under $RENDER and nothing is executed.
P=${RENDER%/}
run() { if [ -n "$RENDER" ]; then echo "(render-only) would run: $*"; else "$@"; fi; }
if [ -z "$RENDER" ] && [ "$(id -u)" != 0 ]; then die "run as root (sudo $0 ...)"; fi

# write FILE MODE: stdin into FILE when the content changed; returns 0 when
# it changed (so callers can restart what depends on it).
write() {
  local f="$P$1" mode=$2 tmp
  mkdir -p "$(dirname "$f")"
  tmp=$(mktemp)
  cat > "$tmp"
  if [ -f "$f" ] && cmp -s "$tmp" "$f"; then rm -f "$tmp"; return 1; fi
  install -m "$mode" "$tmp" "$f"
  rm -f "$tmp"
  echo "  wrote $1"
  return 0
}

NCPU=${CPUS:-$(nproc --all)}
RAM_MB=${RAM_MB:-$(awk '/MemTotal/ {print int($2 / 1024)}' /proc/meminfo)}
# CPU 0 (0-1 from 6 CPUs up) runs the web servers, PostgreSQL, Valkey and the
# proxy; every other CPU judges. A single CPU is shared (not recommended).
if [ "$NCPU" -ge 6 ]; then WEB_CPUS="0 1"; FIRST_JUDGE=2; elif [ "$NCPU" -ge 2 ]; then WEB_CPUS="0"; FIRST_JUDGE=1; else WEB_CPUS="0"; FIRST_JUDGE=0; fi
JUDGE_CORES=$(seq -s ', ' "$FIRST_JUDGE" $((NCPU - 1)))
[ "$ROLE" = worker ] && JUDGE_CORES=$(seq -s ', ' $((NCPU >= 2 ? 1 : 0)) $((NCPU - 1)))

# --- packages -------------------------------------------------------------
if [ -z "$RENDER" ]; then
  step "packages"
  . /etc/os-release
  case "$ID" in debian|ubuntu) ;; *) die "Debian or Ubuntu required (found $ID)" ;; esac
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  PKGS="ca-certificates curl openssl gcc g++ libc6-dev python3 openjdk-17-jdk-headless"
  apt-cache show openjdk-21-jdk-headless >/dev/null 2>&1 && PKGS=${PKGS/openjdk-17/openjdk-21}
  [ "$LANGS" = full ] && PKGS="$PKGS pypy3 fp-compiler rustc golang-go kotlin mono-mcs ghc"
  if [ "$ROLE" = main ]; then
    PKGS="$PKGS postgresql ufw"
    if apt-cache show valkey-server >/dev/null 2>&1; then PKGS="$PKGS valkey-server"; else PKGS="$PKGS redis-server"; fi
    if [ "$WEB" = caddy ]; then PKGS="$PKGS caddy"; else PKGS="$PKGS nginx certbot python3-certbot-nginx"; fi
  else
    PKGS="$PKGS ufw"
  fi
  # shellcheck disable=SC2086
  apt-get install -y -qq --no-install-recommends $PKGS
fi

# --- isolate and control groups --------------------------------------------
if [ -z "$RENDER" ]; then
  step "isolate"
  if ! isolate --version 2>/dev/null | grep -q 'isolator 2'; then
    "$ROOT/scripts/install-isolate.sh"
  else
    echo "  isolate 2 already installed"
  fi
  if [ "$(stat -fc %T /sys/fs/cgroup)" != cgroup2fs ]; then
    echo "  cgroup v2 is not active: enabling it at boot (a reboot is needed before judging)"
    if ! grep -q 'systemd.unified_cgroup_hierarchy=1' /etc/default/grub; then
      sed -i 's/^GRUB_CMDLINE_LINUX="/GRUB_CMDLINE_LINUX="systemd.unified_cgroup_hierarchy=1 /' /etc/default/grub
      update-grub
    fi
  fi
fi

# --- user, directories, binaries ---------------------------------------------
step "user, directories and binaries"
if [ -z "$RENDER" ]; then
  id cms >/dev/null 2>&1 || useradd --system --home-dir /var/lib/cms --create-home --shell /usr/sbin/nologin cms
  install -d -o cms -g cms -m 750 /var/lib/cms /var/lib/cms/blobs /var/lib/cms/ranking /var/lib/cms/backups /var/lib/cms/worker
  install -d -o cms -g cms -m 750 /var/cache/cms
  install -d -m 755 /etc/cms
  if [ ! -x "$ROOT/bin/cms" ]; then
    command -v go >/dev/null || die "no bin/cms: build it first (make build) or install Go"
    make -C "$ROOT" build
  fi
  install -m 755 "$ROOT/bin/cms" "$ROOT/bin/cmsctl" /usr/local/bin/
  install -m 755 "$ROOT/scripts/verify-host.sh" /usr/local/sbin/cms-verify-host
  rm -rf /etc/cms/languages && cp -r "$ROOT/config/languages" /etc/cms/languages
  install -d /usr/local/share/doc/cms && cp -r "$ROOT/docs/." /usr/local/share/doc/cms/
else
  mkdir -p "$P/etc/cms"
fi

# --- secrets (generated once) ----------------------------------------------
SECRETS=/etc/cms/secrets.env
if [ -f "$P$SECRETS" ]; then
  # shellcheck disable=SC1090
  . "$P$SECRETS"
fi
rand() { openssl rand -hex "$1"; }
SECRET_KEY=${SECRET_KEY:-$(rand 32)}
DB_PASSWORD=${DB_PASSWORD:-$(rand 16)}
REDIS_PASSWORD=${REDIS_PASSWORD_ARG:-${REDIS_PASSWORD:-$(rand 16)}}
PUSH_TOKEN=${PUSH_TOKEN:-$(rand 16)}
BLOB_TOKEN=${BLOB_TOKEN_ARG:-${BLOB_TOKEN:-$(rand 24)}}
ADMIN_PASSWORD=${ADMIN_PASSWORD:-$(rand 9)}
write "$SECRETS" 600 <<EOF || true
# Generated by scripts/install.sh; keep private. Workers on other machines
# need REDIS_PASSWORD and BLOB_TOKEN.
SECRET_KEY=$SECRET_KEY
DB_PASSWORD=$DB_PASSWORD
REDIS_PASSWORD=$REDIS_PASSWORD
PUSH_TOKEN=$PUSH_TOKEN
BLOB_TOKEN=$BLOB_TOKEN
ADMIN_PASSWORD=$ADMIN_PASSWORD
EOF

# --- configuration (only when missing: later edits are kept) -----------------
step "configuration"
if [ "$LAN" = 1 ]; then SECURE=false; else SECURE=true; fi
if [ -n "$DOMAIN" ]; then
  CONTEST_URL="https://$DOMAIN"; RANKING_URL="https://$RANKING_DOMAIN"
else
  CONTEST_URL=""; RANKING_URL=""
fi
if [ "$ROLE" = main ]; then
  CMS_YAML=$(cat <<EOF
# Generated by scripts/install.sh for a $NCPU-CPU machine; edit freely (the
# installer never overwrites this file). Reference: config/cms.example.yaml.
log: {level: info, format: json}
database:
  url: postgres://cms:$DB_PASSWORD@127.0.0.1:5432/cms?sslmode=disable
  max_conns: 16
redis:
  url: redis://:$REDIS_PASSWORD@127.0.0.1:6379/0
  pool_size: 32
blob:
  backend: local
  local_dir: /var/lib/cms/blobs
secret_key: "$SECRET_KEY"
languages_dir: /etc/cms/languages
contest_web:
  listen: "127.0.0.1:8888"
  cookie_secure: $SECURE
  trusted_proxies: ["127.0.0.1", "::1"]
admin_web:
  listen: "127.0.0.1:8889"
  cookie_secure: $SECURE
  trusted_proxies: ["127.0.0.1", "::1"]
  contest_url: "$CONTEST_URL"
ranking_web:
  listen: "127.0.0.1:8890"
  data_dir: /var/lib/cms/ranking
  push_token: "$PUSH_TOKEN"
  public_url: "$RANKING_URL"
dispatcher:
  metrics_listen: "127.0.0.1:9101"
  ranking_urls: ["http://127.0.0.1:8890"]
worker:
  metrics_listen: "127.0.0.1:9102"
  isolate_path: /usr/local/bin/isolate
  cores: [$JUDGE_CORES]
  work_dir: /var/lib/cms/worker
  cache_dir: /var/cache/cms/testcases
  cache_max_bytes: 2GiB
monitor:
  metrics_listen: "127.0.0.1:9103"
printing:
  metrics_listen: "127.0.0.1:9104"
blob_server:
  listen: "${PRIVATE_IP:-127.0.0.1}:8891"
  token: "$BLOB_TOKEN"
backup:
  dir: /var/lib/cms/backups
  interval: 24h
  contest_interval: 15m
  keep: 48
  max_rate: 32MiB
EOF
)
else
  CMS_YAML=$(cat <<EOF
# Generated by scripts/install.sh: a worker of the CMS at $MAIN; edit freely
# (the installer never overwrites this file).
log: {level: info, format: json}
redis:
  url: redis://:$REDIS_PASSWORD@$MAIN:6379/0
  pool_size: 8
blob:
  backend: http
  http: {url: "http://$MAIN:8891", token: "$BLOB_TOKEN"}
languages_dir: /etc/cms/languages
worker:
  name: "${WORKER_NAME:-$(hostname)}"
  metrics_listen: "127.0.0.1:9102"
  isolate_path: /usr/local/bin/isolate
  cores: [$JUDGE_CORES]
  work_dir: /var/lib/cms/worker
  cache_dir: /var/cache/cms/testcases
  cache_max_bytes: 4GiB
EOF
)
fi
if [ -f "$P/etc/cms/cms.yaml" ]; then
  echo "  kept the existing /etc/cms/cms.yaml"
else
  echo "$CMS_YAML" | write /etc/cms/cms.yaml 640 || true
  [ -z "$RENDER" ] && chgrp cms /etc/cms/cms.yaml
fi

# --- systemd units ------------------------------------------------------------
step "systemd units"
if [ "$ROLE" = main ]; then
  SERVICES="contest-web admin-web ranking-web dispatcher monitor worker"
  [ -n "$PRIVATE_IP" ] && SERVICES="$SERVICES blob-server"
else
  SERVICES="worker"
fi
UNITS_CHANGED=0
for f in "$ROOT"/deploy/systemd/*; do
  write "/etc/systemd/system/$(basename "$f")" 644 < "$f" && UNITS_CHANGED=1
done
# CPU pinning: everything but the worker stays on the web CPUs; the worker
# pins its boxes to worker.cores and its own threads to the web CPUs.
PIN=$(printf '# Generated by scripts/install.sh: keep off the judging cores.\n[Service]\nCPUAffinity=%s\n' "$WEB_CPUS")
if [ "$ROLE" = main ]; then
  for s in contest-web admin-web ranking-web dispatcher monitor printing blob-server; do
    echo "$PIN" | write "/etc/systemd/system/cms-$s.service.d/cpu.conf" 644 && UNITS_CHANGED=1
  done
  for s in postgresql@.service valkey-server.service redis-server.service caddy.service nginx.service; do
    echo "$PIN" | write "/etc/systemd/system/$s.d/cms-cpu.conf" 644 && UNITS_CHANGED=1
  done
fi
[ "$UNITS_CHANGED" = 1 ] && run systemctl daemon-reload

# --- PostgreSQL -----------------------------------------------------------------
if [ "$ROLE" = main ]; then
  step "PostgreSQL"
  SB=$((RAM_MB / 8)); [ "$SB" -lt 128 ] && SB=128; [ "$SB" -gt 2048 ] && SB=2048
  ECS=$((RAM_MB / 2))
  PGCONF=$(cat <<EOF
# Generated by scripts/install.sh for $NCPU CPUs and $RAM_MB MiB of RAM:
# small, durable, no parallel query workers (they would take the judging core).
shared_buffers = ${SB}MB
effective_cache_size = ${ECS}MB
work_mem = 8MB
maintenance_work_mem = 128MB
max_connections = 100
synchronous_commit = on
wal_compression = on
checkpoint_completion_target = 0.9
max_wal_size = 2GB
min_wal_size = 256MB
random_page_cost = 1.1
effective_io_concurrency = 200
max_worker_processes = 4
max_parallel_workers_per_gather = 0
max_parallel_workers = 0
max_parallel_maintenance_workers = 1
jit = off
log_min_duration_statement = 250ms
listen_addresses = 'localhost'
EOF
)
  if [ -n "$RENDER" ]; then
    echo "$PGCONF" | write /etc/postgresql/cms.conf 644 || true
  else
    PGVER=$(ls /etc/postgresql | sort -V | tail -1)
    if echo "$PGCONF" | write "/etc/postgresql/$PGVER/main/conf.d/cms.conf" 644; then
      systemctl restart "postgresql@$PGVER-main"
    fi
    systemctl enable --now "postgresql@$PGVER-main" >/dev/null 2>&1 || systemctl enable --now postgresql
    runuser -u postgres -- psql -qtAc "SELECT 1 FROM pg_roles WHERE rolname = 'cms'" | grep -q 1 ||
      runuser -u postgres -- psql -qc "CREATE ROLE cms LOGIN"
    runuser -u postgres -- psql -qc "ALTER ROLE cms PASSWORD '$DB_PASSWORD'"
    runuser -u postgres -- psql -qtAc "SELECT 1 FROM pg_database WHERE datname = 'cms'" | grep -q 1 ||
      runuser -u postgres -- createdb -O cms -E UTF8 cms
  fi
fi

# --- Valkey / Redis ---------------------------------------------------------------
if [ "$ROLE" = main ]; then
  step "Valkey"
  BIND="127.0.0.1 -::1"
  [ -n "$PRIVATE_IP" ] && BIND="$BIND $PRIVATE_IP"
  KVCONF=$(cat <<EOF
# Generated by scripts/install.sh. The queues live here: never evict, and
# keep an append-only log so a restart loses at most a second.
bind $BIND
protected-mode yes
requirepass $REDIS_PASSWORD
appendonly yes
appendfsync everysec
maxmemory-policy noeviction
save 300 1
EOF
)
  if [ -n "$RENDER" ]; then
    echo "$KVCONF" | write /etc/valkey/cms.conf 640 || true
  else
    if [ -d /etc/valkey ]; then KVDIR=/etc/valkey; KVMAIN=valkey.conf; KVSVC=valkey-server; else KVDIR=/etc/redis; KVMAIN=redis.conf; KVSVC=redis-server; fi
    CHANGED=0
    echo "$KVCONF" | write "$KVDIR/cms.conf" 640 && CHANGED=1
    chgrp "$(stat -c %G "$KVDIR/$KVMAIN")" "$KVDIR/cms.conf"
    grep -q "^include $KVDIR/cms.conf" "$KVDIR/$KVMAIN" || { echo "include $KVDIR/cms.conf" >> "$KVDIR/$KVMAIN"; CHANGED=1; }
    systemctl enable "$KVSVC" >/dev/null 2>&1
    if [ "$CHANGED" = 1 ]; then systemctl restart "$KVSVC"; else systemctl start "$KVSVC"; fi
  fi
fi

# --- reverse proxy with HTTPS ----------------------------------------------------------
if [ "$ROLE" = main ]; then
  step "reverse proxy ($WEB)"
  if [ "$WEB" = caddy ]; then
    ADMIN_GUARD=""
    [ -n "$ADMIN_ALLOW" ] && ADMIN_GUARD=$(printf '\t@outside not remote_ip %s\n\trespond @outside 403\n' "$ADMIN_ALLOW")
    if {
      echo "# Generated by scripts/install.sh (server-sent events are streamed as they come)."
      [ -n "$EMAIL" ] && printf '{\n\temail %s\n}\n\n' "$EMAIL"
      if [ "$LAN" = 1 ]; then
        printf 'http://:80 {\n\tencode zstd gzip\n\treverse_proxy 127.0.0.1:8888\n}\n\n'
        printf 'http://:8080 {\n\tencode zstd gzip\n\treverse_proxy 127.0.0.1:8890\n}\n\n'
        printf 'http://:8081 {\n%s\n\treverse_proxy 127.0.0.1:8889\n}\n' "$ADMIN_GUARD"
      else
        printf '%s {\n\tencode zstd gzip\n\treverse_proxy 127.0.0.1:8888\n}\n\n' "$DOMAIN"
        printf '%s {\n\tencode zstd gzip\n\treverse_proxy 127.0.0.1:8890\n}\n\n' "$RANKING_DOMAIN"
        printf '%s {\n%s\n\treverse_proxy 127.0.0.1:8889\n}\n' "$ADMIN_DOMAIN" "$ADMIN_GUARD"
      fi
    } | write /etc/caddy/Caddyfile 644; then
      run systemctl reload-or-restart caddy
    fi
    run systemctl enable caddy
  else
    ALLOW=""
    [ -n "$ADMIN_ALLOW" ] && ALLOW=$(printf '    allow %s;\n    deny all;\n' "$ADMIN_ALLOW")
    PROXY='    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_http_version 1.1;
    proxy_buffering off;          # server-sent events
    proxy_read_timeout 1h;'
    if [ "$LAN" = 1 ]; then
      L1="listen 80;" L2="listen 8080;" L3="listen 8081;" N1="_" N2="_" N3="_"
    else
      L1="listen 80;" L2="listen 80;" L3="listen 80;" N1=$DOMAIN N2=$RANKING_DOMAIN N3=$ADMIN_DOMAIN
    fi
    NGINX=$(cat <<EOF
# Generated by scripts/install.sh; certbot adds the HTTPS parts.
server {
    $L1
    server_name $N1;
    client_max_body_size 64m;
    location / {
        proxy_pass http://127.0.0.1:8888;
$PROXY
    }
}
server {
    $L2
    server_name $N2;
    location / {
        proxy_pass http://127.0.0.1:8890;
$PROXY
    }
}
server {
    $L3
    server_name $N3;
    client_max_body_size 1g;
    location / {
$ALLOW        proxy_pass http://127.0.0.1:8889;
$PROXY
    }
}
EOF
)
    if echo "$NGINX" | write /etc/nginx/sites-available/cms 644; then
      run ln -sf /etc/nginx/sites-available/cms /etc/nginx/sites-enabled/cms
      run rm -f /etc/nginx/sites-enabled/default
      run systemctl reload-or-restart nginx
    fi
    if [ "$LAN" = 0 ] && [ -z "$RENDER" ] && [ ! -d "/etc/letsencrypt/live/$DOMAIN" ]; then
      certbot --nginx --non-interactive --agree-tos ${EMAIL:+-m "$EMAIL"} ${EMAIL:---register-unsafely-without-email} \
        -d "$DOMAIN" -d "$RANKING_DOMAIN" -d "$ADMIN_DOMAIN"
    fi
  fi
fi

# --- firewall ---------------------------------------------------------------------
if [ "$FIREWALL" = 1 ]; then
  step "firewall"
  run ufw default deny incoming
  run ufw default allow outgoing
  run ufw allow OpenSSH
  if [ "$ROLE" = main ]; then
    run ufw allow 80/tcp
    if [ "$LAN" = 1 ]; then
      run ufw allow 8080/tcp
      if [ -n "$ADMIN_ALLOW" ]; then run ufw allow from "$ADMIN_ALLOW" to any port 8081 proto tcp; else run ufw allow 8081/tcp; fi
    else
      run ufw allow 443/tcp
    fi
    if [ -n "$PRIVATE_IP" ]; then
      run ufw allow to "$PRIVATE_IP" port 6379 proto tcp
      run ufw allow to "$PRIVATE_IP" port 8891 proto tcp
    fi
  fi
  run ufw --force enable
fi

# --- database schema, first admin, services -------------------------------------------
if [ "$ROLE" = main ]; then
  step "database schema and first administrator"
  run runuser -u cms -- env CMS_CONFIG=/etc/cms/cms.yaml /usr/local/bin/cmsctl bootstrap -admin-username admin -admin-password "$ADMIN_PASSWORD"
fi
step "services"
run systemctl enable cms.target
for s in $SERVICES; do
  run systemctl enable "cms-$s.service"
done
# Restarting the target restarts every enabled CMS service (PartOf=).
run systemctl restart cms.target

echo
echo "Done."
if [ "$ROLE" = main ]; then
  if [ "$LAN" = 1 ]; then
    echo "  contest:  http://<this machine>/          ranking: http://<this machine>:8080/"
    echo "  admin:    http://<this machine>:8081/     user admin, password in $SECRETS (ADMIN_PASSWORD)"
  else
    echo "  contest:  https://$DOMAIN/    ranking: https://$RANKING_DOMAIN/"
    echo "  admin:    https://$ADMIN_DOMAIN/    user admin, password in $SECRETS (ADMIN_PASSWORD)"
  fi
fi
echo "  judging cores: [$JUDGE_CORES]; web CPUs: $WEB_CPUS"
echo "Next: sudo cms-verify-host --config /etc/cms/cms.yaml   (never start a contest if it fails)"
