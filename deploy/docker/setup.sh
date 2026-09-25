#!/usr/bin/env bash
# Prepares and starts the production compose stack (compose.yml):
#   ./setup.sh                              plain HTTP (contest :80, ranking :8080, admin :8081)
#   ./setup.sh --domain cms.example.org     HTTPS for cms., ranking. and admin.cms.example.org
#   ./setup.sh --version 1.2.0              a given release instead of the latest
#   ./setup.sh --env-only                   only write .env (review it, then run again)
#   ./setup.sh --no-pull                    use the images already present
# The first run writes .env with random secrets and a CPU/memory layout for
# this machine; later runs keep them. The first administrator ("admin")
# gets a random password printed once, here: it is stored nowhere (lost?
# docker compose run --rm init ctl admin-password).
set -euo pipefail
cd "$(dirname "$0")"
DOMAIN="" VERSION="" ENV_ONLY=0 PULL=1
while [ $# -gt 0 ]; do
  case "$1" in
    --domain) DOMAIN=$2; shift 2 ;;
    --version) VERSION=${2#v}; shift 2 ;;
    --env-only) ENV_ONLY=1; shift ;;
    --no-pull) PULL=0; shift ;;
    -h|--help) sed -n '2,12p' "$0"; exit 0 ;;
    *) echo "unknown option $1 (see --help)" >&2; exit 2 ;;
  esac
done

rand() { head -c "$1" /dev/urandom | od -An -tx1 | tr -d ' \n'; }
get() { sed -n "s/^$1=//p" .env | tail -1; }
set_var() {
  if grep -q "^$1=" .env; then sed -i "s|^$1=.*|$1=$2|" .env; else echo "$1=$2" >> .env; fi
}

if [ ! -f .env ]; then
  cp env.example .env
  # This machine's layout: CPU 0 (0-1 from 6 CPUs up) for the web servers,
  # PostgreSQL and Valkey; every other CPU judges.
  n=$(nproc --all)
  if [ "$n" -ge 6 ]; then set_var WEB_CPUS 0-1; set_var JUDGE_CPUS "2-$((n - 1))"
  elif [ "$n" -ge 2 ]; then set_var WEB_CPUS 0; set_var JUDGE_CPUS "1-$((n - 1))"
  else set_var WEB_CPUS 0; set_var JUDGE_CPUS 0; fi
  ram=$(awk '/MemTotal/ {print int($2 / 1024)}' /proc/meminfo)
  sb=$((ram / 8)); [ "$sb" -lt 128 ] && sb=128; [ "$sb" -gt 2048 ] && sb=2048
  set_var PG_SHARED_BUFFERS "${sb}MB"
  set_var PG_EFFECTIVE_CACHE_SIZE "$((ram / 2))MB"
  echo "wrote .env for $n CPUs and $ram MiB of RAM"
fi
for k in DB_PASSWORD REDIS_PASSWORD PUSH_TOKEN; do [ -n "$(get "$k")" ] || set_var "$k" "$(rand 16)"; done
[ -n "$(get SECRET_KEY)" ] || set_var SECRET_KEY "$(rand 32)"
if [ -n "$DOMAIN" ]; then
  set_var CONTEST_SITE "$DOMAIN"
  set_var RANKING_SITE "ranking.$DOMAIN"
  set_var ADMIN_SITE "admin.$DOMAIN"
  set_var COOKIE_SECURE true
  set_var RANKING_URL "https://ranking.$DOMAIN"
fi
[ -n "$VERSION" ] && set_var CMS_VERSION "$VERSION"
chmod 600 .env
[ "$ENV_ONLY" = 1 ] && { echo ".env ready"; exit 0; }

command -v docker >/dev/null || { echo "Docker is not installed (https://docs.docker.com/engine/install/)" >&2; exit 1; }
[ "$PULL" = 1 ] && docker compose pull --quiet
docker compose up -d --wait
out=$(docker compose run --rm -T init ctl bootstrap -generate-password)
grep -v '^admin password' <<<"$out" || true
pw=$(sed -n 's/^admin password (shown only now): //p' <<<"$out")

site() { case "$1" in http://*) echo "${1/http:\/\/:/http://<this machine>:}" ;; *) echo "https://$1" ;; esac; }
echo
echo "CMS $(get CMS_VERSION) is running:"
echo "  contest: $(site "$(get CONTEST_SITE)")"
echo "  ranking: $(site "$(get RANKING_SITE)")"
echo "  admin:   $(site "$(get ADMIN_SITE)")"
if [ -n "$pw" ]; then
  echo
  line() { printf '  |  %-60s|\n' "$1"; }
  echo "  +--------------------------------------------------------------+"
  line "Administrator: admin   password: $pw"
  line "Shown only now and stored nowhere: write it down."
  echo "  +--------------------------------------------------------------+"
fi
echo "Next: docker compose exec worker cms ctl judge-selftest   (judges the security battery twice)"
