#!/usr/bin/env bash
# Installs CMS on Ubuntu 22.04/24.04/26.04 or Debian 12/13 (amd64 or arm64), or
# brings an existing installation up to date. One line, as root:
#
#   curl -fsSL https://raw.githubusercontent.com/D4ND3R/Contest-Management-System/main/scripts/install.sh | sudo bash
#   curl -fsSL .../scripts/install.sh | sudo bash -s -- --domain cms.example.org --email you@example.org
#
# It checks the machine (distribution, architecture, not a container,
# control groups v2), downloads the release and verifies its SHA-256
# checksum, installs the dependencies and isolate, creates the cms user,
# tunes PostgreSQL and Valkey to the CPUs and memory found, installs the
# systemd units, sets up HTTPS with Caddy when a domain is given (plain HTTP
# otherwise), creates the first administrator, whose random password is
# printed once at the end, and runs cms-verify-host. Safe to run again:
# secrets are generated once, cms.yaml is only written when missing, the
# other generated files are rewritten identically.
#
# Extra worker on another machine (see docs/en/external-worker.md):
#   ... | sudo bash -s -- --role worker --main 10.8.0.1 --redis-password ... --blob-token ...
#
# Options:
#   --version X.Y.Z|latest   release to install (default latest)
#   --role main|worker       what this machine is (default main)
#   --domain NAME            contest site with HTTPS; admin.NAME and ranking.NAME
#   --admin-domain NAME      unless these two are given
#   --ranking-domain NAME
#   --email ADDRESS          Let's Encrypt account
#   --lan                    no domain, plain HTTP: contest :80, ranking :8080,
#                            admin :8081 (the default without --domain)
#   --web caddy|nginx        reverse proxy (default caddy)
#   --admin-allow CIDR       only these addresses reach the admin site
#   --private-ip IP          also listen on this private address for external
#                            workers (Redis and the blob server)
#   --languages minimal|full toolchains: C, C++, Python, Java | all twelve
#   --main IP                (worker) private address of the main server
#   --redis-password P       (worker) from the main server's /etc/cms/secrets.env
#   --http-ports C,R,A       (--lan) ports of the contest, ranking and admin
#                            sites; default 80,8080,8081
#   --redis-port N           Valkey's port (worker: the main server's); default
#                            6379, or the next free one if another program has it
#   --blob-token T           (worker) idem
#   --worker-name NAME       (worker) default: the hostname
#   --no-firewall            leave the firewall alone
#   --enable-cgroup-v2       on a cgroup v1 system, turn v2 on for the next boot
#                            (then reboot and run the installer again)
#   --skip-verify            do not run cms-verify-host at the end
#   --dry-run                check the machine, download and verify the release,
#                            and print every change it would make; change nothing
#   --uninstall [--purge]    remove CMS; --purge also deletes its database,
#                            data, configuration and user
#   --archive FILE           install this release tarball instead of downloading
#                            (checksums.txt next to it is verified)
#   --release-url URL        where releases are published (mirrors, tests)
#   --from-source            build from this checkout (developers)
#   --render-only DIR        write every generated file under DIR and change
#                            nothing else (review, tests)
#   --cpus N / --ram-mb N    pretend the machine has N CPUs / N MiB
set -euo pipefail

REPO=D4ND3R/Contest-Management-System
RELEASE_URL=https://github.com/$REPO/releases
OPT=${CMS_INSTALL_ROOT:-/opt/cms}   # overridable for tests

ROLE=main VERSION=latest DOMAIN="" ADMIN_DOMAIN="" RANKING_DOMAIN="" EMAIL="" LAN=0 WEB=caddy ADMIN_ALLOW=""
PRIVATE_IP="" LANGS=minimal MAIN="" REDIS_PASSWORD_ARG="" BLOB_TOKEN_ARG="" WORKER_NAME=""
REDIS_PORT=6379 REDIS_PORT_GIVEN=0 HTTP_PORTS=80,8080,8081 HTTP_PORTS_GIVEN=0
C_PORT=80 R_PORT=8080 A_PORT=8081
FIREWALL=1 ENABLE_CGROUP_V2=0 SKIP_VERIFY=0 DRY=0 UNINSTALL=0 PURGE=0 ARCHIVE="" FROM_SOURCE=0
RENDER="" CPUS="" RAM_MB="" VERSION_GIVEN=0

# The directory this script came from, when it is a file (not a pipe): a
# checkout, or an unpacked release.
SELF_DIR=""
if [ -n "${BASH_SOURCE[0]:-}" ] && [ -f "${BASH_SOURCE[0]}" ]; then
  SELF_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
fi

say() { printf '%s\n' "$*"; }
step() { printf '\n==> %s\n' "$*"; }
warn() { printf 'warning: %s\n' "$*" >&2; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }
# blocker: a reason not to install on this machine. A dry run reports every
# one and carries on with the plan.
BLOCKERS=0
blocker() {
  if [ "$DRY" = 1 ]; then printf 'BLOCKER: %s\n' "$*"; BLOCKERS=$((BLOCKERS + 1)); else die "$*"; fi
}
# run: a command that changes the system (only shown in dry-run and render modes).
run() {
  if [ "$DRY" = 1 ]; then say "(dry-run) would run: $*"
  elif [ -n "$RENDER" ]; then say "(render-only) would run: $*"
  else "$@"; fi
}
# write FILE MODE: stdin into FILE when the content changed; returns 0 when
# it changed (so callers can restart what depends on it).
write() {
  local f="$P$1" mode=$2 tmp
  tmp=$(mktemp)
  cat > "$tmp"
  if [ -f "$f" ] && cmp -s "$tmp" "$f"; then rm -f "$tmp"; return 1; fi
  if [ "$DRY" = 1 ]; then
    if [ -f "$f" ]; then say "(dry-run) would rewrite $1"; else say "(dry-run) would write $1"; fi
    rm -f "$tmp"
    return 0
  fi
  mkdir -p "$(dirname "$f")"
  install -m "$mode" "$tmp" "$f"
  rm -f "$tmp"
  say "  wrote $1"
  return 0
}
real() { [ "$DRY" = 0 ] && [ -z "$RENDER" ]; }
is_port() { case "$1" in ''|*[!0-9]*) return 1 ;; esac; [ "${#1}" -le 5 ] && [ "$1" -ge 1 ] && [ "$1" -le 65535 ]; }

parse_args() {
  while [ $# -gt 0 ]; do
    case "$1" in
      --version) VERSION=${2#v}; VERSION_GIVEN=1; shift 2 ;;
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
      --redis-port) REDIS_PORT=$2; REDIS_PORT_GIVEN=1; shift 2 ;;
      --http-ports) HTTP_PORTS=$2; HTTP_PORTS_GIVEN=1; shift 2 ;;
      --blob-token) BLOB_TOKEN_ARG=$2; shift 2 ;;
      --worker-name) WORKER_NAME=$2; shift 2 ;;
      --no-firewall) FIREWALL=0; shift ;;
      --enable-cgroup-v2) ENABLE_CGROUP_V2=1; shift ;;
      --skip-verify) SKIP_VERIFY=1; shift ;;
      --dry-run) DRY=1; shift ;;
      --uninstall) UNINSTALL=1; shift ;;
      --purge) PURGE=1; shift ;;
      --archive) ARCHIVE=$2; shift 2 ;;
      --release-url) RELEASE_URL=${2%/}; shift 2 ;;
      --from-source) FROM_SOURCE=1; shift ;;
      --render-only) RENDER=$2; shift 2 ;;
      --cpus) CPUS=$2; shift 2 ;;
      --ram-mb) RAM_MB=$2; shift 2 ;;
      -h|--help) if [ -n "$SELF_DIR" ]; then awk 'NR > 1 { if (/^#/) print; else exit }' "${BASH_SOURCE[0]}"; else say "see https://github.com/$REPO/blob/main/scripts/install.sh"; fi; exit 0 ;;
      *) die "unknown option $1 (see --help)" ;;
    esac
  done
  case "$ROLE" in main|worker) ;; *) die "--role must be main or worker" ;; esac
  case "$WEB" in caddy|nginx) ;; *) die "--web must be caddy or nginx" ;; esac
  case "$LANGS" in minimal|full) ;; *) die "--languages must be minimal or full" ;; esac
  is_port "$REDIS_PORT" || die "--redis-port must be a port number"
  IFS=, read -r C_PORT R_PORT A_PORT _ <<<"$HTTP_PORTS"
  for p in "$C_PORT" "$R_PORT" "$A_PORT"; do
    is_port "$p" || die "--http-ports takes three port numbers: contest,ranking,admin (e.g. 8000,8001,8002)"
    # The CMS services themselves listen on 127.0.0.1:8888-8891.
    if [ "$p" -ge 8888 ] && [ "$p" -le 8891 ]; then die "--http-ports: 8888-8891 are CMS's own services' ports"; fi
  done
  if [ "$C_PORT" = "$R_PORT" ] || [ "$C_PORT" = "$A_PORT" ] || [ "$R_PORT" = "$A_PORT" ]; then
    die "--http-ports: three different ports"
  fi
  [ -n "$DOMAIN" ] && [ "$HTTP_PORTS_GIVEN" = 1 ] && die "--http-ports is for --lan (a domain is served on 80 and 443)"
  if [ "$ROLE" = main ] && [ -z "$DOMAIN" ] && [ "$LAN" = 0 ]; then
    LAN=1
    [ "$UNINSTALL" = 1 ] || say "No --domain: plain HTTP on this machine's addresses (contest :$C_PORT, ranking :$R_PORT, admin :$A_PORT)."
  fi
  if [ "$ROLE" = worker ] && [ "$UNINSTALL" = 0 ] && { [ -z "$MAIN" ] || [ -z "$REDIS_PASSWORD_ARG" ] || [ -z "$BLOB_TOKEN_ARG" ]; }; then
    die "a worker needs --main, --redis-password and --blob-token (from the main server's /etc/cms/secrets.env)"
  fi
  if [ -n "$DOMAIN" ]; then ADMIN_DOMAIN=${ADMIN_DOMAIN:-admin.$DOMAIN}; RANKING_DOMAIN=${RANKING_DOMAIN:-ranking.$DOMAIN}; fi
  # In render-only mode every path is under $RENDER and nothing is executed.
  P=${RENDER%/}
  if real && [ "$(id -u)" != 0 ]; then die "run as root (sudo bash, or --dry-run to only look)"; fi
}

# --- the machine ---------------------------------------------------------------
detect() {
  step "this machine"
  local os=${CMS_INSTALL_OS_RELEASE:-/etc/os-release} id="" ver="" name=""
  if [ -f "$os" ]; then
    # shellcheck source=/dev/null
    id=$(. "$os"; echo "${ID:-}")
    # shellcheck source=/dev/null
    ver=$(. "$os"; echo "${VERSION_ID:-}")
    # shellcheck source=/dev/null
    name=$(. "$os"; echo "${PRETTY_NAME:-$ID}")
  fi
  case "$id:$ver" in
    ubuntu:22.04|ubuntu:24.04|ubuntu:26.04|debian:12|debian:13) say "  system: $name" ;;
    ubuntu:*|debian:*) warn "$name is not tested (supported: Ubuntu 22.04/24.04/26.04, Debian 12/13); carrying on" ;;
    *) blocker "unsupported system ${name:-unknown}: CMS installs on Ubuntu 22.04/24.04/26.04 or Debian 12/13 (Windows and macOS cannot judge; see docs/en/deployment.md, or use Docker Compose on another Linux)" ;;
  esac
  ARCH=${CMS_INSTALL_ARCH:-$(uname -m)}
  case "$ARCH" in
    x86_64|amd64) ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) blocker "unsupported architecture $ARCH: amd64 or arm64 only"; ARCH=unsupported ;;
  esac
  [ "$ARCH" = unsupported ] || say "  architecture: $ARCH"
  local virt=${CMS_INSTALL_CONTAINER:-$(systemd-detect-virt --container 2>/dev/null || true)}
  if [ -n "$virt" ] && [ "$virt" != none ]; then
    blocker "this is a container ($virt): the sandbox needs a KVM virtual machine or a dedicated server (OpenVZ/LXC VPSs and serverless platforms cannot judge); to run CMS in Docker, see deploy/docker/"
  fi
  if grep -qi microsoft /proc/version 2>/dev/null; then
    blocker "this is WSL (Windows): judging needs a Linux machine or a KVM virtual machine"
  fi
  local cgfs
  cgfs=${CMS_INSTALL_CGROUP_FS:-$(stat -fc %T /sys/fs/cgroup 2>/dev/null || echo none)}
  if [ "$cgfs" = cgroup2fs ]; then
    say "  control groups: v2"
  else
    cgroup_v1
  fi
  web_ports_check
  NCPU=${CPUS:-$(nproc --all)}
  RAM_MB=${RAM_MB:-$(awk '/MemTotal/ {print int($2 / 1024)}' /proc/meminfo)}
  say "  $NCPU CPUs, $RAM_MB MiB of RAM"
  [ "$RAM_MB" -lt 1800 ] && warn "less than 2 GiB of RAM: expect trouble with compilers and PostgreSQL"
  [ "$NCPU" -lt 2 ] && warn "one CPU: judging will share it with the web servers (timings will vary)"
  # CPU 0 (0-1 from 6 CPUs up) runs the web servers, PostgreSQL, Valkey and the
  # proxy; every other CPU judges. A single CPU is shared (not recommended).
  if [ "$NCPU" -ge 6 ]; then WEB_CPUS="0 1"; FIRST_JUDGE=2; elif [ "$NCPU" -ge 2 ]; then WEB_CPUS="0"; FIRST_JUDGE=1; else WEB_CPUS="0"; FIRST_JUDGE=0; fi
  JUDGE_CORES=$(seq -s ', ' "$FIRST_JUDGE" $((NCPU - 1)))
  [ "$ROLE" = worker ] && JUDGE_CORES=$(seq -s ', ' $((NCPU >= 2 ? 1 : 0)) $((NCPU - 1)))
  return 0
}

# isolate measures and limits memory and time with control groups v2.
cgroup_v1() {
  local how="add systemd.unified_cgroup_hierarchy=1 to the kernel command line (GRUB_CMDLINE_LINUX in /etc/default/grub, then update-grub) and reboot"
  if [ "$DRY" = 1 ] || [ -n "$RENDER" ]; then
    blocker "control groups v2 are not active: $how (or run this installer with --enable-cgroup-v2)"
    return 0
  fi
  if [ "$ENABLE_CGROUP_V2" = 1 ] && [ -f /etc/default/grub ]; then
    grep -q 'systemd.unified_cgroup_hierarchy=1' /etc/default/grub ||
      sed -i 's/^GRUB_CMDLINE_LINUX="/GRUB_CMDLINE_LINUX="systemd.unified_cgroup_hierarchy=1 /' /etc/default/grub
    update-grub
    die "control groups v2 turned on for the next boot: reboot, then run the same installer command again"
  fi
  die "control groups v2 are not active: $how; or run this installer with --enable-cgroup-v2 to do it (a reboot follows). A VPS that cannot boot with them (OpenVZ, LXC) cannot judge."
}

# --- the release -----------------------------------------------------------------
# fetch sets SRC (a directory laid out like a release tarball: cms, cmsctl,
# config/, deploy/, scripts/, docs/) and VER.
fetch() {
  if [ -n "$RENDER" ]; then
    SRC=${SELF_DIR:?render-only runs from a checkout}; VER=render; return
  fi
  if [ "$FROM_SOURCE" = 1 ]; then
    from_source; return
  fi
  if [ -z "$ARCHIVE" ] && [ -x "$OPT/current/cms" ]; then
    # Installed already: running the installer again reconfigures that
    # release; changing it is cmsctl upgrade's job (backup, rollback).
    local installed
    installed=$("$OPT/current/cms" version | awk '{print $2}')
    if [ "$VERSION_GIVEN" = 1 ] && [ "$VERSION" != "$installed" ]; then
      die "CMS $installed is installed: to change the version run  sudo cmsctl upgrade -version $VERSION  (backup, migrations and automatic rollback)"
    fi
    SRC=$(readlink -f "$OPT/current"); VER=$installed
    step "release $VER (installed; to upgrade: sudo cmsctl upgrade)"
    return
  fi
  if [ -z "$ARCHIVE" ] && [ "$VERSION_GIVEN" = 0 ] && [ -n "$SELF_DIR" ] && [ -x "$SELF_DIR/cms" ] && [ -f "$SELF_DIR/deploy/systemd/cms.target" ]; then
    # Run from an unpacked release: install that one.
    SRC=$SELF_DIR; VER=$("$SRC/cms" version | awk '{print $2}')
    step "release $VER (the unpacked one this script came with)"
    return
  fi
  WORK=$(mktemp -d)
  local tgz sums name
  if [ -n "$ARCHIVE" ]; then
    [ -f "$ARCHIVE" ] || die "no such file: $ARCHIVE"
    name=$(basename "$ARCHIVE")
    [[ "$name" =~ ^cms_(.+)_linux_(amd64|arm64)\.tar\.gz$ ]] || die "$name is not a CMS release tarball (cms_VERSION_linux_ARCH.tar.gz)"
    VER=${BASH_REMATCH[1]}
    [ "${BASH_REMATCH[2]}" = "$ARCH" ] || blocker "$name is for ${BASH_REMATCH[2]}, this machine is $ARCH"
    step "release $VER from $ARCHIVE"
    tgz=$ARCHIVE; sums=$(dirname "$ARCHIVE")/checksums.txt
    [ -f "$sums" ] || die "no checksums.txt next to $ARCHIVE: download it from the same release"
  else
    command -v curl >/dev/null || die "curl is needed (apt-get install curl)"
    if [ "$VERSION" = latest ]; then
      local url
      url=$(curl -fsSL -o /dev/null -w '%{url_effective}' "$RELEASE_URL/latest") || die "cannot reach $RELEASE_URL"
      VER=${url##*/}; VER=${VER#v}
      [[ "$VER" =~ ^[0-9]+\.[0-9]+\.[0-9]+ ]] || die "no release found at $RELEASE_URL (got $url)"
    else
      VER=$VERSION
    fi
    name=cms_${VER}_linux_${ARCH}.tar.gz
    step "release $VER"
    say "  downloading $RELEASE_URL/download/v$VER/$name"
    curl -fsSL -o "$WORK/$name" "$RELEASE_URL/download/v$VER/$name" || die "cannot download $name (is $VER a release for $ARCH?)"
    curl -fsSL -o "$WORK/checksums.txt" "$RELEASE_URL/download/v$VER/checksums.txt" || die "cannot download the checksums of $VER"
    tgz=$WORK/$name; sums=$WORK/checksums.txt
  fi
  local want got
  want=$(awk -v f="$name" '$2 == f || $2 == "*" f {print $1}' "$sums")
  got=$(sha256sum "$tgz" | awk '{print $1}')
  [ -n "$want" ] || die "$name is not listed in checksums.txt"
  [ "$want" = "$got" ] || die "checksum mismatch for $name: expected $want, got $got (download corrupted or tampered with)"
  say "  SHA-256 verified: $got"
  mkdir "$WORK/release"
  tar -xzf "$tgz" -C "$WORK/release" --strip-components=1
  SRC=$WORK/release
}

from_source() {
  local root=${SELF_DIR:?--from-source runs from a checkout}
  [ -f "$root/cmd/cms/main.go" ] || die "--from-source: $root is not a checkout"
  if [ ! -x "$root/bin/cms" ] || [ ! -x "$root/bin/cmsctl" ]; then
    command -v go >/dev/null || die "no bin/cms: build it first (make build) or install Go"
    make -C "$root" build
  fi
  WORK=$(mktemp -d)
  SRC=$WORK/release
  mkdir -p "$SRC"
  cp "$root/bin/cms" "$root/bin/cmsctl" "$root/LICENSE" "$root/NOTICE" "$SRC/"
  cp -r "$root/config" "$root/deploy" "$root/scripts" "$root/docs" "$SRC/"
  VER=$("$SRC/cms" version | awk '{print $2}')
  step "release $VER built from $root"
}

cleanup() { [ -n "${WORK:-}" ] && rm -rf "$WORK"; return 0; }

# --- packages, isolate --------------------------------------------------------------
packages() {
  step "packages"
  export DEBIAN_FRONTEND=noninteractive
  # A fresh machine may have no package lists yet: read them before choosing.
  run apt-get update -qq
  local pkgs="ca-certificates curl openssl gcc g++ libc6-dev python3"
  if available openjdk-21-jdk-headless; then pkgs="$pkgs openjdk-21-jdk-headless"; else pkgs="$pkgs openjdk-17-jdk-headless"; fi
  [ "$LANGS" = full ] && pkgs="$pkgs pypy3 fp-compiler rustc golang-go kotlin mono-mcs ghc"
  if [ "$ROLE" = main ]; then
    pkgs="$pkgs postgresql ufw"
    if available valkey-server; then pkgs="$pkgs valkey-server"; else pkgs="$pkgs redis-server"; fi
    if [ "$WEB" = caddy ]; then
      { real || [ "$DRY" = 1 ]; } && ! available caddy && caddy_repository
      pkgs="$pkgs caddy"
    else
      pkgs="$pkgs nginx certbot python3-certbot-nginx"
    fi
  else
    pkgs="$pkgs ufw"
  fi
  # isolate is built from source: its build dependencies.
  pkgs="$pkgs build-essential git libcap-dev libseccomp-dev libsystemd-dev pkg-config"
  # shellcheck disable=SC2086
  run apt-get install -y -qq --no-install-recommends $pkgs
}

# available: the package can be installed from the configured archives
# (never consulted when only rendering, so the rendered files are stable).
available() { { real || [ "$DRY" = 1 ]; } && "${CMS_INSTALL_APT_CACHE:-apt-cache}" show "$1" >/dev/null 2>&1; }

# caddy_repository adds Caddy's official Debian repository, for systems
# whose archive has no caddy (Ubuntu 22.04).
caddy_repository() {
  local key=/usr/share/keyrings/caddy-stable-archive-keyring.gpg list=/etc/apt/sources.list.d/caddy-stable.list
  say "  caddy is not in this system's archive: adding Caddy's repository (dl.cloudsmith.io/public/caddy/stable)"
  if [ "$DRY" = 1 ]; then
    say "(dry-run) would write $key and $list"
    return 0
  fi
  command -v gpg >/dev/null || apt-get install -y -qq --no-install-recommends gnupg
  curl -fsSL https://dl.cloudsmith.io/public/caddy/stable/gpg.key | gpg --dearmor --yes -o "$key" ||
    die "could not download Caddy's signing key (or use --web nginx)"
  curl -fsSL https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt -o "$list" ||
    die "could not download Caddy's repository definition (or use --web nginx)"
  chmod 644 "$key" "$list"
  apt-get update -qq
}

isolate_setup() {
  step "isolate"
  if isolate --version 2>/dev/null | grep -q 'isolator 2'; then
    say "  isolate 2 already installed"
  else
    run "$SRC/scripts/install-isolate.sh"
  fi
}

# --- user, directories, binaries ---------------------------------------------------
files() {
  step "user, directories and binaries ($OPT/releases/$VER)"
  if ! real; then
    [ -n "$RENDER" ] && mkdir -p "$P/etc/cms"
    say "  would install $OPT/releases/$VER, link $OPT/current, /usr/local/bin/cms and cmsctl"
    return
  fi
  id cms >/dev/null 2>&1 || useradd --system --home-dir /var/lib/cms --create-home --shell /usr/sbin/nologin cms
  install -d -o cms -g cms -m 750 /var/lib/cms /var/lib/cms/blobs /var/lib/cms/ranking /var/lib/cms/backups /var/lib/cms/worker
  install -d -o cms -g cms -m 750 /var/cache/cms
  install -d -m 755 /etc/cms /etc/cms/languages "$OPT/releases"
  local dest=$OPT/releases/$VER
  if [ ! -x "$dest/cms" ] || ! cmp -s "$SRC/cms" "$dest/cms"; then
    rm -rf "$dest.new"
    mkdir -p "$dest.new"
    cp -a "$SRC"/. "$dest.new"/
    rm -rf "$dest"
    mv "$dest.new" "$dest"
    say "  installed $dest"
  fi
  ln -sfn "releases/$VER" "$OPT/current"
  # Minimal systems may lack some of these.
  mkdir -p /usr/local/bin /usr/local/sbin /usr/local/share/doc
  ln -sfn "$OPT/current/cms" /usr/local/bin/cms
  ln -sfn "$OPT/current/cmsctl" /usr/local/bin/cmsctl
  ln -sfn "$OPT/current/scripts/verify-host.sh" /usr/local/sbin/cms-verify-host
  # Earlier installs copied the documentation into a directory.
  [ -d /usr/local/share/doc/cms ] && [ ! -L /usr/local/share/doc/cms ] && rm -rf /usr/local/share/doc/cms
  ln -sfn "$OPT/current/docs" /usr/local/share/doc/cms
  # The shipped languages are updated; languages added here are kept.
  cp -r "$SRC/config/languages/." /etc/cms/languages/
  # Earlier installs copied the binaries into /usr/local/bin; releases older
  # than the previous two are removed.
  local keep
  keep=$(readlink "$OPT/current")
  find "$OPT/releases" -mindepth 1 -maxdepth 1 -type d ! -path "$OPT/$keep" -printf '%T@ %p\n' | sort -rn | tail -n +3 | cut -d' ' -f2- | xargs -r rm -rf
}

# --- secrets (generated once) ------------------------------------------------------
secrets() {
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
  {
    echo "# Generated by scripts/install.sh; keep private. Workers on other machines"
    echo "# need REDIS_PASSWORD and BLOB_TOKEN. The administrator's password is not"
    echo "# kept anywhere (lost: sudo -u cms cmsctl admin-password)."
    echo "SECRET_KEY=$SECRET_KEY"
    echo "DB_PASSWORD=$DB_PASSWORD"
    echo "REDIS_PASSWORD=$REDIS_PASSWORD"
    echo "PUSH_TOKEN=$PUSH_TOKEN"
    echo "BLOB_TOKEN=$BLOB_TOKEN"
    # Installers before first-boot hardening stored it here; kept, not renewed.
    [ -n "${ADMIN_PASSWORD:-}" ] && echo "ADMIN_PASSWORD=$ADMIN_PASSWORD"
  } | write "$SECRETS" 600 || true
}

# --- configuration (only when missing: later edits are kept) -----------------------
configuration() {
  step "configuration"
  local secure=true contest_url="" ranking_url="" yaml
  [ "$LAN" = 1 ] && secure=false
  if [ -n "$DOMAIN" ]; then contest_url="https://$DOMAIN"; ranking_url="https://$RANKING_DOMAIN"; fi
  if [ "$ROLE" = main ] && { real || [ "$DRY" = 1 ]; }; then
    real && pg_cluster create
    kv_port
  fi
  if [ "$ROLE" = main ]; then
    yaml=$(cat <<EOF
# Generated by scripts/install.sh for a $NCPU-CPU machine; edit freely (the
# installer never overwrites this file). Reference: /opt/cms/current/config/cms.example.yaml.
log: {level: info, format: json}
database:
  url: postgres://cms:$DB_PASSWORD@127.0.0.1:$PGPORT/cms?sslmode=disable
  max_conns: 16
redis:
  url: redis://:$REDIS_PASSWORD@127.0.0.1:$REDIS_PORT/0
  pool_size: 32
blob:
  backend: local
  local_dir: /var/lib/cms/blobs
secret_key: "$SECRET_KEY"
languages_dir: /etc/cms/languages
contest_web:
  listen: "127.0.0.1:8888"
  cookie_secure: $secure
  trusted_proxies: ["127.0.0.1", "::1"]
admin_web:
  listen: "127.0.0.1:8889"
  cookie_secure: $secure
  trusted_proxies: ["127.0.0.1", "::1"]
  contest_url: "$contest_url"
ranking_web:
  listen: "127.0.0.1:8890"
  data_dir: /var/lib/cms/ranking
  push_token: "$PUSH_TOKEN"
  public_url: "$ranking_url"
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
    yaml=$(cat <<EOF
# Generated by scripts/install.sh: a worker of the CMS at $MAIN; edit freely
# (the installer never overwrites this file).
log: {level: info, format: json}
redis:
  url: redis://:$REDIS_PASSWORD@$MAIN:$REDIS_PORT/0
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
    say "  kept the existing /etc/cms/cms.yaml"
    real && sync_ports
  else
    echo "$yaml" | write /etc/cms/cms.yaml 640 || true
    real && chgrp cms /etc/cms/cms.yaml
  fi
  return 0
}

# --- systemd units ----------------------------------------------------------------
units() {
  step "systemd units"
  if [ "$ROLE" = main ]; then
    SERVICES="contest-web admin-web ranking-web dispatcher monitor worker"
    [ -n "$PRIVATE_IP" ] && SERVICES="$SERVICES blob-server"
  else
    SERVICES="worker"
  fi
  local changed=0 f s pin
  for f in "$SRC"/deploy/systemd/*; do
    # shellcheck disable=SC2094 # reads the shipped unit, writes /etc/systemd/system
    write "/etc/systemd/system/$(basename "$f")" 644 < "$f" && changed=1
  done
  # CPU pinning: everything but the worker stays on the web CPUs; the worker
  # pins its boxes to worker.cores and its own threads to the web CPUs.
  pin=$(printf '# Generated by scripts/install.sh: keep off the judging cores.\n[Service]\nCPUAffinity=%s\n' "$WEB_CPUS")
  if [ "$ROLE" = main ]; then
    for s in contest-web admin-web ranking-web dispatcher monitor printing blob-server; do
      echo "$pin" | write "/etc/systemd/system/cms-$s.service.d/cpu.conf" 644 && changed=1
    done
    for s in postgresql@.service valkey-server.service redis-server.service caddy.service nginx.service; do
      echo "$pin" | write "/etc/systemd/system/$s.d/cms-cpu.conf" 644 && changed=1
    done
  fi
  [ "$changed" = 1 ] && run systemctl daemon-reload
  return 0
}

# --- PostgreSQL, tuned to this machine ----------------------------------------------
# as_postgres runs a command as the postgres user from / (not from a
# directory it cannot enter, which makes psql complain).
as_postgres() { (cd / && runuser -u postgres -- "$@"); }

# pg_cluster finds the cluster CMS uses: the "main" cluster of the newest
# PostgreSQL whose server is installed (after a distribution upgrade the old
# version's cluster may remain, on 5432, and the new one get 5433). With
# "create" it creates one when there is none. Sets PGVER and PGPORT.
PGVER="" PGPORT=5432
pg_cluster() {
  local v name port best="" mode=${1:-} lib=${CMS_INSTALL_PG_LIB:-/usr/lib/postgresql}
  command -v pg_lsclusters >/dev/null || return 0
  while read -r v name port _; do
    [ "$name" = main ] && [ -x "$lib/$v/bin/postgres" ] && best="$v $port"
  done < <(pg_lsclusters -h 2>/dev/null | sort -V)
  if [ -z "$best" ] && [ "$mode" = create ]; then
    v=$(find "$lib" -mindepth 1 -maxdepth 1 -printf '%f\n' 2>/dev/null | sort -V | tail -1 || true)
    [ -n "$v" ] || die "PostgreSQL is not installed (apt-get install postgresql)"
    say "  no PostgreSQL cluster: creating $v/main"
    pg_createcluster --locale C.UTF-8 "$v" main >/dev/null || die "could not create the PostgreSQL cluster $v/main"
    best="$v $(pg_lsclusters -h "$v" main | awk '{print $3}')"
  fi
  [ -n "$best" ] || return 0
  PGVER=${best% *} PGPORT=${best#* }
}

postgres() {
  [ "$ROLE" = main ] || return 0
  step "PostgreSQL"
  local sb ecs mwm wm conf pgver
  sb=$((RAM_MB / 8)); [ "$sb" -lt 128 ] && sb=128; [ "$sb" -gt 4096 ] && sb=4096
  ecs=$((RAM_MB / 2))
  mwm=$((RAM_MB / 32)); [ "$mwm" -lt 64 ] && mwm=64; [ "$mwm" -gt 1024 ] && mwm=1024
  wm=8; [ "$RAM_MB" -ge 16000 ] && wm=16
  conf=$(cat <<EOF
# Generated by scripts/install.sh for $NCPU CPUs and $RAM_MB MiB of RAM:
# durable, and no parallel query workers (they would take the judging cores).
shared_buffers = ${sb}MB
effective_cache_size = ${ecs}MB
work_mem = ${wm}MB
maintenance_work_mem = ${mwm}MB
max_connections = 100
synchronous_commit = on
wal_compression = on
checkpoint_completion_target = 0.9
max_wal_size = 2GB
min_wal_size = 256MB
random_page_cost = 1.1
effective_io_concurrency = 200
max_worker_processes = $((NCPU > 4 ? NCPU : 4))
max_parallel_workers_per_gather = 0
max_parallel_workers = 0
max_parallel_maintenance_workers = 1
jit = off
log_min_duration_statement = 250ms
listen_addresses = 'localhost'
EOF
)
  if [ -n "$RENDER" ]; then
    echo "$conf" | write /etc/postgresql/cms.conf 644 || true
    return 0
  fi
  if [ "$DRY" = 1 ]; then
    pg_cluster
    pgver=$PGVER
    echo "$conf" | write "/etc/postgresql/${pgver:-VERSION}/main/conf.d/cms.conf" 644 || true
    run createdb -O cms -E UTF8 -T template0 --locale C.UTF-8 cms
    return 0
  fi
  pg_cluster create
  say "  cluster $PGVER/main on port $PGPORT"
  pgver=$PGVER
  mkdir -p "/etc/postgresql/$pgver/main/conf.d"
  local started=0
  if echo "$conf" | write "/etc/postgresql/$pgver/main/conf.d/cms.conf" 644; then
    systemctl restart "postgresql@$pgver-main" && started=1
  else
    systemctl enable --now "postgresql@$pgver-main" >/dev/null 2>&1 && started=1
  fi
  systemctl enable "postgresql@$pgver-main" >/dev/null 2>&1 || true
  if [ "$started" = 0 ] || ! as_postgres psql -p "$PGPORT" -qtAc "SELECT 1" >/dev/null 2>&1; then
    tail -n 20 "/var/log/postgresql/postgresql-$pgver-main.log" >&2 2>/dev/null || true
    die "PostgreSQL $pgver/main does not start (its log is above; journalctl -u postgresql@$pgver-main has more)"
  fi
  as_postgres psql -p "$PGPORT" -qtAc "SELECT 1 FROM pg_roles WHERE rolname = 'cms'" | grep -q 1 ||
    as_postgres psql -p "$PGPORT" -qc "CREATE ROLE cms LOGIN"
  as_postgres psql -p "$PGPORT" -qc "ALTER ROLE cms PASSWORD '$DB_PASSWORD'"
  # From template0 with an explicit locale: the cluster's own default may
  # be SQL_ASCII (created under the C locale).
  as_postgres psql -p "$PGPORT" -qtAc "SELECT 1 FROM pg_database WHERE datname = 'cms'" | grep -q 1 ||
    as_postgres createdb -p "$PGPORT" -O cms -E UTF8 -T template0 --locale C.UTF-8 cms
}

# --- Valkey (or Redis), the job queues -----------------------------------------------
# kv_names: the key-value store this system packages (Valkey, else Redis).
kv_names() {
  if [ -d /etc/valkey ] || command -v valkey-server >/dev/null; then
    KV_DIR=/etc/valkey KV_MAIN=valkey.conf KV_SVC=valkey-server
  else
    KV_DIR=/etc/redis KV_MAIN=redis.conf KV_SVC=redis-server
  fi
}

# port_holder: the program listening on a local TCP port (empty: none).
port_holder() { ss -ltnpH "sport = :$1" 2>/dev/null | sed -n 's/.*users:(("\([^"]*\)".*/\1/p' | head -1 || true; }

# kv_port picks Valkey's port: 6379 unless another program has it (the
# Redis of another application, a container...), then the next free one;
# the other program is left alone.
kv_port() {
  [ "$REDIS_PORT_GIVEN" = 1 ] && return 0
  kv_names
  local p h
  for p in $(seq 6379 6399); do
    h=$(port_holder "$p")
    if [ -z "$h" ] || [ "$h" = "$KV_SVC" ]; then
      [ "$p" != 6379 ] && say "  port 6379 is used by $(port_holder 6379): CMS's $KV_SVC listens on $p"
      REDIS_PORT=$p
      return 0
    fi
  done
  die "ports 6379-6399 are all taken: free one, or choose a port with --redis-port"
}

# sync_ports points an existing cms.yaml at the local PostgreSQL and Valkey
# when their ports changed since it was written (the file is otherwise
# never touched).
sync_ports() {
  [ "$ROLE" = main ] || return 0
  local f=$P/etc/cms/cms.yaml tmp
  tmp=$(mktemp)
  sed -E "s#(postgres://[^@]*@127\.0\.0\.1:)[0-9]+/#\1$PGPORT/#; s#(redis://[^@]*@127\.0\.0\.1:)[0-9]+/#\1$REDIS_PORT/#" "$f" > "$tmp"
  if ! cmp -s "$tmp" "$f"; then
    cat "$tmp" > "$f"
    say "  updated the PostgreSQL ($PGPORT) and Valkey ($REDIS_PORT) ports in /etc/cms/cms.yaml"
  fi
  rm -f "$tmp"
}

# kv_failed shows why the store does not start, then stops.
kv_failed() {
  local h
  journalctl -u "$KV_SVC" -n 25 --no-pager -o cat >&2 2>/dev/null || true
  tail -n 15 "/var/log/${KV_SVC%-server}/$KV_SVC.log" >&2 2>/dev/null || true
  h=$(port_holder "$REDIS_PORT")
  [ -n "$h" ] && [ "$h" != "$KV_SVC" ] && warn "port $REDIS_PORT is used by $h"
  die "$KV_SVC does not start (its messages are above; systemctl status $KV_SVC)"
}

valkey() {
  [ "$ROLE" = main ] || return 0
  step "Valkey"
  local bind="127.0.0.1 -::1" conf changed=0
  [ -n "$PRIVATE_IP" ] && bind="$bind $PRIVATE_IP"
  conf=$(cat <<EOF
# Generated by scripts/install.sh. The queues live here: never evict, and
# keep an append-only log so a restart loses at most a second.
bind $bind
port $REDIS_PORT
protected-mode yes
requirepass $REDIS_PASSWORD
appendonly yes
appendfsync everysec
maxmemory-policy noeviction
save 300 1
EOF
)
  # Threads for network I/O only with more than one web CPU.
  [ "$NCPU" -ge 6 ] && conf="$conf"$'\n'"io-threads 2"
  if [ -n "$RENDER" ]; then
    echo "$conf" | write /etc/valkey/cms.conf 640 || true
    return 0
  fi
  kv_names
  [ "$REDIS_PORT" != 6379 ] && say "  on port $REDIS_PORT"
  if [ "$DRY" = 1 ]; then
    echo "$conf" | write "$KV_DIR/cms.conf" 640 || true
    return 0
  fi
  echo "$conf" | write "$KV_DIR/cms.conf" 640 && changed=1
  chgrp "$(stat -c %G "$KV_DIR/$KV_MAIN")" "$KV_DIR/cms.conf"
  grep -q "^include $KV_DIR/cms.conf" "$KV_DIR/$KV_MAIN" || { echo "include $KV_DIR/cms.conf" >> "$KV_DIR/$KV_MAIN"; changed=1; }
  systemctl enable "$KV_SVC" >/dev/null 2>&1 || true
  # Earlier failed starts may have tripped systemd's restart limit.
  systemctl reset-failed "$KV_SVC" >/dev/null 2>&1 || true
  if [ "$changed" = 1 ]; then
    systemctl restart "$KV_SVC" || kv_failed
  else
    systemctl start "$KV_SVC" || kv_failed
  fi
}

# --- reverse proxy with HTTPS ----------------------------------------------------------
proxy() {
  [ "$ROLE" = main ] || return 0
  step "reverse proxy ($WEB)"
  if [ "$WEB" = caddy ]; then
    local guard="" cf=$P/etc/caddy/Caddyfile
    [ -n "$ADMIN_ALLOW" ] && guard=$(printf '\t@outside not remote_ip %s\n\trespond @outside 403\n' "$ADMIN_ALLOW")
    # A Caddyfile this script did not write (the package's example, or the
    # sites of a Caddy already in use) is kept aside once.
    if real && [ -s "$cf" ] && ! head -1 "$cf" | grep -q '^# Generated by scripts/install.sh' && [ ! -e "$cf.before-cms" ]; then
      cp -p "$cf" "$cf.before-cms"
      say "  the previous Caddyfile is kept as /etc/caddy/Caddyfile.before-cms"
    fi
    if {
      echo "# Generated by scripts/install.sh (server-sent events are streamed as they come)."
      [ -n "$EMAIL" ] && printf '{\n\temail %s\n}\n\n' "$EMAIL"
      if [ "$LAN" = 1 ]; then
        printf 'http://:%s {\n\tencode zstd gzip\n\treverse_proxy 127.0.0.1:8888\n}\n\n' "$C_PORT"
        printf 'http://:%s {\n\tencode zstd gzip\n\treverse_proxy 127.0.0.1:8890\n}\n\n' "$R_PORT"
        printf 'http://:%s {\n%s\n\treverse_proxy 127.0.0.1:8889\n}\n' "$A_PORT" "$guard"
      else
        printf '%s {\n\tencode zstd gzip\n\treverse_proxy 127.0.0.1:8888\n}\n\n' "$DOMAIN"
        printf '%s {\n\tencode zstd gzip\n\treverse_proxy 127.0.0.1:8890\n}\n\n' "$RANKING_DOMAIN"
        printf '%s {\n%s\n\treverse_proxy 127.0.0.1:8889\n}\n' "$ADMIN_DOMAIN" "$guard"
      fi
    } | write /etc/caddy/Caddyfile 644; then
      run systemctl reload-or-restart caddy
    fi
    run systemctl enable caddy
  else
    local allow="" proxyconf l1 l2 l3 n1 n2 n3 nginx
    [ -n "$ADMIN_ALLOW" ] && allow=$(printf '    allow %s;\n    deny all;\n' "$ADMIN_ALLOW")
    # shellcheck disable=SC2016
    proxyconf='    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_http_version 1.1;
    proxy_buffering off;          # server-sent events
    proxy_read_timeout 1h;'
    if [ "$LAN" = 1 ]; then
      l1="listen $C_PORT;" l2="listen $R_PORT;" l3="listen $A_PORT;" n1="_" n2="_" n3="_"
    else
      l1="listen 80;" l2="listen 80;" l3="listen 80;" n1=$DOMAIN n2=$RANKING_DOMAIN n3=$ADMIN_DOMAIN
    fi
    nginx=$(cat <<EOF
# Generated by scripts/install.sh; certbot adds the HTTPS parts.
server {
    $l1
    server_name $n1;
    client_max_body_size 64m;
    location / {
        proxy_pass http://127.0.0.1:8888;
$proxyconf
    }
}
server {
    $l2
    server_name $n2;
    location / {
        proxy_pass http://127.0.0.1:8890;
$proxyconf
    }
}
server {
    $l3
    server_name $n3;
    client_max_body_size 1g;
    location / {
$allow        proxy_pass http://127.0.0.1:8889;
$proxyconf
    }
}
EOF
)
    if echo "$nginx" | write /etc/nginx/sites-available/cms 644; then
      run ln -sf /etc/nginx/sites-available/cms /etc/nginx/sites-enabled/cms
      run rm -f /etc/nginx/sites-enabled/default
      run systemctl reload-or-restart nginx
    fi
    if [ "$LAN" = 0 ] && [ ! -d "/etc/letsencrypt/live/$DOMAIN" ]; then
      local account=(--register-unsafely-without-email)
      [ -n "$EMAIL" ] && account=(-m "$EMAIL")
      run certbot --nginx --non-interactive --agree-tos "${account[@]}" -d "$DOMAIN" -d "$RANKING_DOMAIN" -d "$ADMIN_DOMAIN"
    fi
  fi
}

# --- firewall ---------------------------------------------------------------------
firewall() {
  [ "$FIREWALL" = 1 ] || return 0
  step "firewall"
  local p others=""
  # On a machine that serves other things, a deny-by-default firewall would
  # cut them off: then it is left to the administrator.
  if { real || [ "$DRY" = 1 ]; } && ! ufw status 2>/dev/null | grep -q "Status: active"; then
    others=$(other_listeners)
  fi
  if [ -n "$others" ]; then
    say "  left off: other programs listen on this machine (${others% }),"
    say "  and a deny-by-default firewall would cut them off. CMS needs TCP $(fw_ports) open;"
    say "  allow those and theirs (sudo ufw allow PORT/tcp or PORT/udp), then: sudo ufw enable"
    return 0
  fi
  run ufw default deny incoming
  run ufw default allow outgoing
  run ufw allow OpenSSH
  # SSH on another port too: never lock the administrator out. WireGuard
  # tunnels (the workers' way in) stay open as well.
  for p in $(ssh_ports); do [ "$p" = 22 ] || run ufw allow "$p/tcp"; done
  for p in $(wg_ports); do run ufw allow "$p/udp"; done
  if [ "$ROLE" = main ]; then
    if [ "$LAN" = 1 ]; then
      run ufw allow "$C_PORT/tcp"
      run ufw allow "$R_PORT/tcp"
      if [ -n "$ADMIN_ALLOW" ]; then run ufw allow from "$ADMIN_ALLOW" to any port "$A_PORT" proto tcp; else run ufw allow "$A_PORT/tcp"; fi
    else
      run ufw allow 80/tcp
      run ufw allow 443/tcp
    fi
    if [ -n "$PRIVATE_IP" ]; then
      run ufw allow to "$PRIVATE_IP" port "$REDIS_PORT" proto tcp
      run ufw allow to "$PRIVATE_IP" port 8891 proto tcp
    fi
  fi
  run ufw --force enable
}

# fw_ports: the TCP ports CMS needs open on this machine.
fw_ports() {
  local ports="22"
  if [ "$ROLE" = main ]; then
    if [ "$LAN" = 1 ]; then ports="$ports $C_PORT $R_PORT $A_PORT"; else ports="$ports 80 443"; fi
    [ -n "$PRIVATE_IP" ] && ports="$ports $REDIS_PORT 8891 (from the private network)"
  fi
  echo "$ports"
}

# ssh_ports: the ports SSH listens on (the daemon's, or its systemd socket's).
ssh_ports() {
  {
    ss -ltnpH 2>/dev/null | grep -E '"sshd(-session)?"' | awk '{n = split($4, a, ":"); print a[n]}'
    systemctl show -p Listen --value ssh.socket 2>/dev/null | sed -n 's/.*:\([0-9][0-9]*\) .*/\1/p'
  } | sort -u || true
}

# wg_ports: the UDP ports WireGuard tunnels listen on.
wg_ports() { wg show all listen-port 2>/dev/null | awk '{print $2}' | sort -u || true; }

# other_listeners: what else listens on this machine's addresses, TCP or
# UDP, as "program:port/proto" ("port/proto" for the kernel's own, such as
# NFS). Not counted: SSH and WireGuard (kept open), containers' published
# ports (Docker opens those past ufw), CMS's own services and the usual
# system daemons (DHCP, mDNS, time).
other_listeners() {
  local keep
  keep=" 22/tcp $(ssh_ports | sed 's#$#/tcp#' | tr '\n' ' ') $(wg_ports | sed 's#$#/udp#' | tr '\n' ' ') "
  { ss -ltnpH 2>/dev/null | sed 's/^/tcp /'; ss -lunpH 2>/dev/null | sed 's/^/udp /'; } |
    awk -v keep="$keep" '
      $5 ~ /^(127\.|\[::1\]|\[::ffff:127\.)/ { next }
      {
        n = split($5, a, ":"); port = a[n]; name = ""
        if (match($0, /users:\(\("[^"]*"/)) name = substr($0, RSTART + 9, RLENGTH - 10)
        if (index(keep, " " port "/" $1 " ")) next
        if (name ~ /^(sshd|sshd-session|docker-proxy|caddy|nginx|cms|valkey-server|redis-server|postgres|systemd-resolve|systemd-network|systemd-timesyn|dhclient|NetworkManager|avahi-daemon|chronyd)$/) next
        print (name == "" ? "" : name ":") port "/" $1
      }' | sort -u | tr '\n' ' ' || true
}

# web_ports_check: the web server's ports must be free (or held by the web
# server CMS configures): checked before anything is changed.
web_ports_check() {
  [ "$ROLE" = main ] || return 0
  local p h ports
  if [ "$LAN" = 1 ]; then ports="$C_PORT $R_PORT $A_PORT"; else ports="80 443"; fi
  for p in $ports; do
    h=$(port_holder "$p")
    [ -z "$h" ] || [ "$h" = "$WEB" ] && continue
    if [ "$LAN" = 1 ]; then
      blocker "port $p is used by $h: choose three free ports for the contest, ranking and admin sites with --http-ports (e.g. --http-ports 8000,8001,8002)"
    else
      blocker "port $p is used by $h: HTTPS for a domain needs ports 80 and 443; stop that program, or use --lan with --http-ports"
    fi
  done
}

# lan_url: how the sites are reached on a local network.
lan_url() { if [ "$1" = 80 ]; then echo "http://<this machine>/"; else echo "http://<this machine>:$1/"; fi; }

# --- database schema, first administrator, services ------------------------------------
services() {
  ADMIN_PASSWORD_NEW=""
  if [ "$ROLE" = main ]; then
    step "database schema and first administrator"
    local cmd=(runuser -u cms -- env CMS_CONFIG=/etc/cms/cms.yaml /usr/local/bin/cmsctl bootstrap -admin-username admin -generate-password)
    if real; then
      local out
      out=$("${cmd[@]}")
      grep -v '^admin password' <<<"$out" | sed 's/^/  /'
      ADMIN_PASSWORD_NEW=$(sed -n 's/^admin password (shown only now): //p' <<<"$out")
    else
      run "${cmd[@]}"
    fi
  fi
  step "services"
  local s
  run systemctl enable cms.target
  for s in $SERVICES; do
    run systemctl enable "cms-$s.service"
  done
  # Restarting the target restarts every enabled CMS service (PartOf=).
  run systemctl restart cms.target
}

verify() {
  VERIFY_OK=1
  [ "$SKIP_VERIFY" = 1 ] && return 0
  step "verifying this machine judges correctly (cms-verify-host)"
  if real; then
    /usr/local/sbin/cms-verify-host --config /etc/cms/cms.yaml || VERIFY_OK=0
  else
    run /usr/local/sbin/cms-verify-host --config /etc/cms/cms.yaml
  fi
}

# sites: where the web sites are served.
sites() {
  [ "$ROLE" = main ] || return 0
  if [ "$LAN" = 1 ]; then
    say "  contest:  $(lan_url "$C_PORT")          ranking: $(lan_url "$R_PORT")"
    say "  admin:    $(lan_url "$A_PORT")"
  else
    say "  contest:  https://$DOMAIN/    ranking: https://$RANKING_DOMAIN/"
    say "  admin:    https://$ADMIN_DOMAIN/"
  fi
}

summary() {
  echo
  if [ -n "$RENDER" ]; then
    say "Rendered into $RENDER; judging cores: [$JUDGE_CORES]; web CPUs: $WEB_CPUS"
    return 0
  fi
  if [ "$DRY" = 1 ]; then
    if [ "$BLOCKERS" -gt 0 ]; then say "Dry run: $BLOCKERS problem(s) above would stop the installation; nothing was changed."
    else
      say "Dry run: nothing was changed; run again without --dry-run to install $VER."
      sites
    fi
    return
  fi
  say "CMS $VER installed."
  sites
  say "  judging cores: [$JUDGE_CORES]; web CPUs: $WEB_CPUS"
  if [ -n "${ADMIN_PASSWORD_NEW:-}" ]; then
    echo
    line() { printf '  |  %-60s|\n' "$1"; }
    say "  +--------------------------------------------------------------+"
    line "Administrator: admin   password: $ADMIN_PASSWORD_NEW"
    line "Shown only now and stored nowhere: write it down."
    line "Lost? sudo -u cms cmsctl admin-password"
    say "  +--------------------------------------------------------------+"
  fi
  if [ "${VERIFY_OK:-1}" = 0 ]; then
    echo
    say "cms-verify-host FAILED: fix what it reports and run it again (sudo cms-verify-host)."
    say "Never start a contest on a machine that fails it."
    return 1
  fi
  return 0
}

# --- uninstall --------------------------------------------------------------------------
uninstall() {
  if [ "$PURGE" = 1 ]; then step "removing CMS and its data (--purge)"; else step "removing CMS (its data stays)"; fi
  local u
  run systemctl disable --now cms.target
  for u in /etc/systemd/system/cms-*.service; do
    [ -e "$u" ] && run systemctl disable --now "$(basename "$u")"
  done
  run rm -rf /etc/systemd/system/cms.target /etc/systemd/system/cms-*.service /etc/systemd/system/cms-*.service.d
  for u in postgresql@.service valkey-server.service redis-server.service caddy.service nginx.service; do
    run rm -f "/etc/systemd/system/$u.d/cms-cpu.conf"
  done
  run systemctl daemon-reload
  run rm -f /usr/local/bin/cms /usr/local/bin/cmsctl /usr/local/sbin/cms-verify-host
  run rm -rf /usr/local/share/doc/cms "$OPT"
  run rm -f /etc/nginx/sites-enabled/cms /etc/nginx/sites-available/cms
  local caddy="/etc/caddy/Caddyfile still points at CMS."
  if [ -f /etc/caddy/Caddyfile.before-cms ]; then
    run mv /etc/caddy/Caddyfile.before-cms /etc/caddy/Caddyfile
    run systemctl reload-or-restart caddy
    caddy="/etc/caddy/Caddyfile is back to the one found before CMS."
  fi
  if [ "$PURGE" = 1 ]; then
    if command -v psql >/dev/null; then
      pg_cluster
      run as_postgres dropdb -p "$PGPORT" --if-exists cms
      run as_postgres psql -p "$PGPORT" -qc "DROP ROLE IF EXISTS cms"
    fi
    run rm -f /etc/postgresql/*/main/conf.d/cms.conf /etc/valkey/cms.conf /etc/redis/cms.conf
    for u in /etc/valkey/valkey.conf /etc/redis/redis.conf; do
      [ -f "$u" ] && run sed -i '\|^include /etc/\(valkey\|redis\)/cms.conf|d' "$u"
    done
    run rm -rf /etc/cms /var/lib/cms /var/cache/cms
    id cms >/dev/null 2>&1 && run userdel cms
    echo
    say "CMS and its data were removed. Left installed: PostgreSQL, Valkey, the reverse proxy, isolate and the compilers (apt remove them if unused); $caddy"
  else
    echo
    say "CMS was removed; its data stays: /etc/cms (configuration and secrets), /var/lib/cms (files, backups), the PostgreSQL database cms."
    [ -f /etc/caddy/Caddyfile ] && say "$caddy"
    say "To delete those too: run the installer again with --uninstall --purge (take a backup first)."
  fi
}

main() {
  parse_args "$@"
  trap cleanup EXIT
  # A locale forwarded by SSH but missing here makes apt and PostgreSQL's
  # scripts print pages of warnings (and initdb refuse it): C.UTF-8 always
  # exists.
  export LC_ALL=C.UTF-8 LANGUAGE=
  if [ "$UNINSTALL" = 1 ]; then
    if real && [ "$(id -u)" != 0 ]; then die "run as root"; fi
    uninstall
    return
  fi
  if [ -n "$RENDER" ]; then
    # Only the generated files: no machine checks, no downloads.
    NCPU=${CPUS:-$(nproc --all)}
    RAM_MB=${RAM_MB:-$(awk '/MemTotal/ {print int($2 / 1024)}' /proc/meminfo)}
    if [ "$NCPU" -ge 6 ]; then WEB_CPUS="0 1"; FIRST_JUDGE=2; elif [ "$NCPU" -ge 2 ]; then WEB_CPUS="0"; FIRST_JUDGE=1; else WEB_CPUS="0"; FIRST_JUDGE=0; fi
    JUDGE_CORES=$(seq -s ', ' "$FIRST_JUDGE" $((NCPU - 1)))
    [ "$ROLE" = worker ] && JUDGE_CORES=$(seq -s ', ' $((NCPU >= 2 ? 1 : 0)) $((NCPU - 1)))
  else
    detect
    # No release to plan with on an unsupported architecture.
    if [ "$ARCH" = unsupported ]; then summary; return; fi
  fi
  fetch
  if real || [ "$DRY" = 1 ]; then packages; isolate_setup; fi
  files
  secrets
  configuration
  units
  postgres
  valkey
  proxy
  firewall
  services
  verify
  summary
}

# Everything above only defines functions: with `curl ... | bash` the whole
# script has been read before anything runs.
main "$@"
