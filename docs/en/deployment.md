# Installing CMS on a server

This guide installs every CMS service on one Linux server with a single
command, upgrades it and removes it. The reference target is a **2 vCPU
KVM VPS with 4 GB of RAM**: CPU 0 runs the web servers, PostgreSQL, Valkey
and the HTTPS proxy; CPU 1 is reserved for the sandbox, so the queue of
submissions never slows the contest web server down. Bigger machines work
the same way (from 6 CPUs up, CPUs 0–1 serve the web and the rest judge);
more judging power comes from [workers on other machines](external-worker.md).
To run it in containers instead, see [Docker Compose](docker.md).

## Server requirements

| | Required |
|---|---|
| System | **Linux**: Ubuntu 22.04, 24.04 or 26.04, Debian 12 or 13 (the installer checks) |
| Machine | a **KVM virtual machine** (most VPSs) or a **dedicated server** |
| Access | **root** (sudo) over SSH |
| Kernel | **control groups v2** (the default on those systems; the installer checks, and `--enable-cgroup-v2` turns them on) |
| Architecture | **amd64** (x86-64) or **arm64** (aarch64) |
| Network | a domain with three names (`cms.example.org`, `admin.`, `ranking.`) for HTTPS, or a LAN (plain HTTP) |

**Not supported for judging** (the sandbox cannot measure or confine
programs there, so verdicts would be wrong or insecure):

- **Windows and macOS** (including WSL and Docker Desktop): use them as
  the administrators' or contestants' computers, not as the server.
- **Container VPSs (OpenVZ, LXC, Virtuozzo)**: they share the host's
  kernel and cannot create the sandbox's control groups. Ask for KVM.
- **Serverless and PaaS platforms** (AWS Lambda, Cloud Run, Heroku,
  Vercel...): CMS needs long-running services, a database and root.
- 32-bit or other architectures.

### Sizes by number of contestants

| Contestants | Main server | Judging | Basis |
|-------------|-------------|---------|-------|
| up to ~500 | 2 vCPU, 4 GB RAM, 40 GB SSD | 1 core: 70–80 heavy C++ submissions a minute at the peak | measured ([load tests](../../loadtest/README.md)) |
| up to ~1,000 | 4 vCPU, 8 GB RAM, 80 GB SSD | 3 cores | web measured on one core (pages under 150 ms; let contestants log in over a few minutes before the start) |
| 1,000–3,000 | 8 vCPU, 16 GB RAM, 160 GB SSD, plus [external workers](external-worker.md) | 6 cores and more | estimate: confirm with `make loadtest` on that machine |

Judging is usually the tighter limit: count the submissions of the final
rush (every contestant every 30 seconds in the last minutes is common)
against about 75 a minute per judging core with heavy C++, many more with
light programs. Disk: submissions, testcases and 48 backups; a large
contest with big testcases needs more.

## Install (one line)

On the server, as a user with sudo:

```sh
curl -fsSL https://raw.githubusercontent.com/D4ND3R/Contest-Management-System/main/scripts/install.sh \
  | sudo bash -s -- --domain cms.example.org --email you@example.org
```

The same script is attached to every release, at
`https://github.com/D4ND3R/Contest-Management-System/releases/latest/download/install.sh`
(use that URL in the same command if `raw.githubusercontent.com` is not
reachable from the server).

Without `--domain` it serves plain HTTP on the machine's addresses
(contest on port 80, ranking 8080, admin 8081; `--http-ports` changes
them), for a contest on a local network. It:

1. checks the machine (system, architecture, not a container, control
   groups v2, free web ports) and stops with an explanation, before
   changing anything, if it cannot judge or cannot serve;
2. downloads the latest release for this architecture from GitHub and
   **verifies its SHA-256 checksum** (a corrupted or tampered download
   stops everything);
3. installs the packages, the compilers and isolate;
4. creates the `cms` user, tunes PostgreSQL and Valkey to the CPUs and
   memory it finds, installs the systemd units and pins them to the web CPU;
5. sets up HTTPS with Caddy (certificates from Let's Encrypt) when a
   domain is given, and the firewall;
6. creates the first administrator, **`admin`, with a random password
   printed once at the end** (stored nowhere);
7. runs [`cms-verify-host`](verify-host.md): the security battery and the
   sample solutions are judged twice. **Never start a contest on a server
   where it fails.**

Useful options: `--version 1.2.0` (a given release), `--dry-run` (check
the machine, download and verify the release, print every change, change
nothing), `--web nginx` (nginx + certbot instead of Caddy), `--admin-allow
203.0.113.0/24` (who may open the admin), `--languages full` (all twelve
toolchains instead of C, C++, Python and Java), `--private-ip 10.8.0.1`
(let [external workers](external-worker.md) reach this server),
`--http-ports 8000,8001,8002` (contest, ranking and admin ports on a LAN),
`--tune-host` (performance governor, no turbo boost or transparent huge
pages, for stable times: see [verify-host](verify-host.md)),
`--judge-all-threads`, `--no-firewall`, `--enable-cgroup-v2`. The whole list: `--help`, or the top
of [scripts/install.sh](../../scripts/install.sh).

**On a machine that already serves something** (a home server with
Nextcloud, another web site, containers): a program that already holds a
port CMS's web server needs is reported by name before anything is
changed. On a LAN, pick three free ports:

```sh
sudo bash install.sh --dry-run --lan --http-ports 8000,8001,8002   # check first
sudo bash install.sh --lan --http-ports 8000,8001,8002
```

A domain needs ports 80 and 443 for its certificates, so on such a machine
use `--lan` with `--http-ports`, or put CMS behind the web server that
already has them. A Redis already on 6379 is left alone (see Valkey
below), and so is a Caddyfile the installer did not write: it is kept as
`/etc/caddy/Caddyfile.before-cms`. The firewall is not enabled when other
programs listen on the machine (see Firewall below).

Without Internet access on the server, download `cms_<version>_linux_<arch>.tar.gz`
and `checksums.txt` from the [releases page](https://github.com/D4ND3R/Contest-Management-System/releases),
copy both, and run `sudo bash scripts/install.sh --archive cms_..._linux_amd64.tar.gz`
from the unpacked tarball (the packages still come from the distribution's
mirror).

The installer can be run again at any time: it keeps `/etc/cms/cms.yaml`,
the secrets and the installed release, rewrites the other generated files
only when they change, and restarts the services. It never changes the
version: that is [`cmsctl upgrade`](#upgrade).

### First login

Open the admin site and log in as `admin` with the password the installer
printed. Then enable two-factor authentication (**My account**). If the
password is lost: `sudo -u cms cmsctl admin-password` sets and prints a new
one. Administrators who log in with a well-known default password
(`admin`, `password`, their username...) must choose another one before
anything else.

## Upgrade

```sh
sudo cmsctl upgrade                    # the latest release
sudo cmsctl upgrade -version 1.3.0     # a given one
```

It refuses to run while a contest is in progress (anyone's window is still
open) unless you pass `-force`. Then it downloads the release and verifies
its checksum, takes a backup (kind *upgrade*, listed in the admin's
**Backups**), stops the services, switches `/opt/cms/current` to the new
release, applies the database migrations, starts the services and waits
until every one answers. **If any of that fails, it goes back by itself**:
the previous release, the database restored from that backup, the services
started. The services are down for about a minute; the last three releases
stay in `/opt/cms/releases/`. Going back to an older version is only
possible by restoring a backup taken with it ([backups](backups.md)).

On an [external worker](external-worker.md), run
`sudo cmsctl upgrade -version <the main server's version>` after the main
server: a worker has no database, so it only switches the release and
restarts (judging jobs in flight are re-queued).

## Uninstall

```sh
curl -fsSL https://raw.githubusercontent.com/D4ND3R/Contest-Management-System/main/scripts/install.sh | sudo bash -s -- --uninstall
```

stops and removes the services, the binaries and `/opt/cms`, and keeps the
data: `/etc/cms` (configuration and secrets), `/var/lib/cms` (files and
backups) and the PostgreSQL database `cms`. `--uninstall --purge` deletes
those too, and the `cms` user (take a backup first). PostgreSQL, Valkey,
the proxy, isolate and the compilers stay installed; a Caddyfile kept as
`/etc/caddy/Caddyfile.before-cms` is put back. Add `--dry-run` to see the
list first.

## What the installer does, step by step

### Packages

`gcc g++ python3 openjdk-17/21-jdk-headless` (plus `pypy3 fp-compiler rustc
golang-go kotlin mono-mcs ghc` with `--languages full`), `postgresql`,
`valkey-server` (or `redis-server` where Valkey is not packaged), `caddy`
(or `nginx certbot python3-certbot-nginx`) and `ufw`. Ubuntu 22.04 does not
package Caddy: the installer adds Caddy's official repository
(`/etc/apt/sources.list.d/caddy-stable.list`) there.

### isolate and cgroup v2

`scripts/install-isolate.sh` builds isolate 2 from source, installs it
setuid root with the configuration `/usr/local/etc/isolate` (boxes under
`/var/local/lib/isolate`, uids from 60000) and enables `isolate.service`,
which keeps the control group isolate uses. isolate 2 needs the unified
cgroup v2 hierarchy (the default on Debian 11+ and Ubuntu 21.10+); on an
older setup the installer stops and explains; with `--enable-cgroup-v2` it
adds `systemd.unified_cgroup_hierarchy=1` to the kernel command line, and
you reboot and run it again.

### User and files

| Path | Content |
|------|---------|
| `/opt/cms/releases/<version>/` | the unpacked releases (the last three) |
| `/opt/cms/current` | the release in use (`cmsctl upgrade` switches it) |
| `/usr/local/bin/cms`, `cmsctl` | links to the binaries of the current release |
| `/usr/local/sbin/cms-verify-host` | link to its `scripts/verify-host.sh` |
| `/etc/cms/cms.yaml` | configuration (group `cms`, mode 640) |
| `/etc/cms/secrets.env` | generated passwords and tokens (root, mode 600) |
| `/etc/cms/languages/` | language definitions |
| `/var/lib/cms/{blobs,ranking,backups,worker}` | data (user `cms`) |
| `/var/cache/cms/` | the worker's testcase cache |

Every service runs as the unprivileged system user `cms`.

### PostgreSQL, tuned to the machine

`/etc/postgresql/<version>/main/conf.d/cms.conf`: `shared_buffers` = RAM/8
(128 MB–4 GB), `effective_cache_size` = RAM/2, `maintenance_work_mem` =
RAM/32, `work_mem 8MB` (16 MB from 16 GB of RAM), `max_worker_processes`
= the CPUs, `max_connections 100`, **no parallel query workers** (they
would take the judging cores), `jit off` (short queries do not pay it
back), `synchronous_commit on` (no accepted submission is ever lost),
`wal_compression on`,
`random_page_cost 1.1` (SSD). The role and database `cms` get a random
password. The services use at most 16 connections each (`database.max_conns`).

It uses the `main` cluster of the newest PostgreSQL installed, on that
cluster's port. After a distribution upgrade (say Ubuntu 24.04 to 26.04),
the old version's cluster usually keeps port 5432 and the new one gets
5433; the installer takes the new one and writes its port into
`/etc/cms/cms.yaml`. With no cluster at all it creates one (`C.UTF-8`),
and the database is always created UTF-8, whatever the cluster's default
encoding. If PostgreSQL does not start, the installer stops and shows its
log (`pg_lsclusters` lists the clusters, their ports and state).

### Valkey

`/etc/valkey/cms.conf` (included from `valkey.conf`): listens on localhost
only (plus `--private-ip`), password required, `appendonly yes` with
`appendfsync everysec` and `maxmemory-policy noeviction` — the judging
queues live there and must survive a restart. From 6 CPUs up it uses two
I/O threads.

It listens on port 6379 unless another program already has that port (a
Redis left by another installation, another application's store): then it
takes the next free one and says so ("port 6379 is used by redis-server:
CMS's valkey-server listens on 6380"), writes it into `/etc/cms/cms.yaml`,
and leaves the other program alone. External workers then need
`--redis-port` with that port. If Valkey does not start, the installer
shows Valkey's own messages and what holds the port.

### systemd

`deploy/systemd/` has one unit per service (`cms-contest-web`,
`cms-admin-web`, `cms-ranking-web`, `cms-dispatcher`, `cms-monitor`,
`cms-worker`, `cms-printing`, `cms-blob-server`) and `cms.target`, which
groups them:

```sh
sudo systemctl status cms.target 'cms-*'
sudo systemctl restart cms.target          # every enabled CMS service
journalctl -u cms-contest-web -f           # logs (JSON)
```

Every unit restarts automatically (`Restart=always`, 2 s). The web
services are sandboxed by systemd (read-only system, private /tmp, no new
privileges, writes only under `/var/lib/cms`).

**CPU pinning.** The first physical core (the first two from six cores up)
serves the web: drop-ins (`/etc/systemd/system/<unit>.d/cpu.conf`) set
`CPUAffinity` to its CPUs for every CMS service but the worker, and for
PostgreSQL, Valkey and the proxy. Every other physical core judges with one
CPU (`worker.cores` in `cms.yaml`); with hyperthreading its siblings stay
idle, because a busy sibling slows the judging CPU down
(`--judge-all-threads` uses them too). The worker is not confined: it pins
each sandbox to a judging CPU and its own threads to the other CPUs. The
installer prints the layout: "judging cores: [1, 2, 3]; web CPUs: 0 4; idle
hyperthreads: 5 6 7".

### HTTPS

With Caddy, `/etc/caddy/Caddyfile` has one site per name, each a
`reverse_proxy` to the local service; Caddy obtains and renews the Let's
Encrypt certificates by itself and streams server-sent events as they
come. `--admin-allow CIDR` answers 403 to anyone else on the admin site.
With nginx, `/etc/nginx/sites-available/cms` proxies with
`proxy_buffering off` (server-sent events) and `certbot --nginx` adds the
certificates. The CMS services listen on 127.0.0.1 only and trust
`X-Forwarded-For` from the proxy (`trusted_proxies`), so IP restrictions
and logs see the contestants' addresses. A Caddyfile the installer did
not write is kept once as `/etc/caddy/Caddyfile.before-cms`.

### Firewall

`ufw`: deny incoming except SSH (port 22 and any other port `sshd`
listens on), WireGuard's port if a tunnel is up, 80 and 443 (on a LAN: the three `--http-ports`, by default 80,
8080 and 8081; the admin port only from `--admin-allow`). With
`--private-ip`, Valkey's port (6379) and 8891 (blob server) are open on
that address only.

When `ufw` is not active yet and anything else listens on the machine's
addresses, TCP or UDP (anything but SSH, WireGuard, containers' published
ports, CMS's own services and the usual DHCP, mDNS and time daemons), a
deny-by-default firewall would cut it off: the installer leaves it off,
names each listener with its port (`node:3000/tcp`; `2049/tcp` for the
kernel's own, such as NFS) and the ports CMS needs, so you can allow them
all (`sudo ufw allow PORT/tcp`) before `sudo ufw enable`. Note that Docker publishes container ports past `ufw`.

### Database and first administrator

`cmsctl bootstrap -generate-password` applies the migrations and, the
first time only, creates the administrator `admin` with a random password
that the installer prints once at the end; it is not written anywhere
(`/etc/cms/secrets.env` keeps the database and Valkey passwords and the
tokens). Upgrades apply new migrations with `cmsctl upgrade`.

## After installing

1. `sudo cms-verify-host --config /etc/cms/cms.yaml` must end with
   `RESULT: OK` ([details](verify-host.md)). **Never start a contest on a
   host where it fails.**
2. Log in to the admin with the printed password and enable 2FA.
3. Create or import a contest and its tasks ([problem
   packages](problem-package.md)), add the users.
4. Check **Backups** in the admin: a backup is taken every day, every 15
   minutes around a contest ([backups](backups.md)).
5. Read the [contest-day runbook](contest-day.md).

### Printing

Contestants' print jobs (enabled per contest, see [contest
settings](contest-settings.md#printing)) are sent to CUPS by `cms-printing`,
which runs on the main server. To use a printer:

```sh
sudo apt install cups-client          # or cups, if the printer is attached here
lpstat -p -d                           # destinations CUPS knows
```

then set `printing.printer: <destination>` in `/etc/cms/cms.yaml` (and
`paper_size: Letter` if needed) and `sudo systemctl restart cms-printing`.
Every job prints as one CUPS job: a cover page (user, name, team, site,
file, pages) followed by the document. Without a printer the jobs are marked
printed with "not printed: no printer configured", which is handy for a
rehearsal. `lp` failures are retried three times, then the job shows as
*not printed* on the staff queue, where it can be printed again. Run a
single printing service: when it starts it takes back the jobs it was
printing, so a crash may print a job twice but never loses one.

## Monitoring

- `https://admin.../system`: workers, queues, running jobs.
- Every service answers `/healthz`; Prometheus metrics on `/metrics` (web
  services) and on 127.0.0.1:9101–9104 (dispatcher, worker, monitor,
  printing). Useful alerts: `up == 0`, `cms_backup_last_success_timestamp_seconds`
  older than 30 minutes during a contest, queue length growing for minutes.
- A service that seems stuck: `sudo systemctl kill -s USR1 cms-dispatcher`
  (any unit) writes the stack of every goroutine to its journal without
  stopping it (`journalctl -u cms-dispatcher`); attach it to a bug report.
- Profiling a web server under load: `pprof: true` under `contest_web`,
  `admin_web` or `ranking_web` serves the Go profiler at `/debug/pprof/`
  on the service's own port, only to requests made on the machine itself
  (never through the HTTPS proxy), e.g. `go tool pprof
  http://127.0.0.1:8888/debug/pprof/profile?seconds=20`. Leave it off
  otherwise.

## Security and limits

The web servers send a strict Content-Security-Policy (no inline code, no
foreign origins), `X-Frame-Options: DENY`, `nosniff` and a same-origin
referrer policy; session cookies are `HttpOnly`, `SameSite=Lax` and, with
`cookie_secure: true` (set by the install script when HTTPS is on),
`Secure`. Every form carries a per-session CSRF token and requests from
another origin are refused. Settings in `cms.yaml`:

| Setting | Default | Meaning |
|---------|---------|---------|
| `contest_web.login_rate_limit_per_minute` | 20 | **failed** logins per address and minute (successful ones never count, so a lab behind one NAT address logs in at once); a username also locks after 10 failures a minute from any address |
| `admin_web.login_rate_limit_per_minute` | 20 | the same for administrators; 5 wrong second-factor codes a minute lock the administrator |
| `contest_web.rate_limit_per_minute` | 120 | submissions and user tests per contestant and minute (on top of the contest's own limits) |
| `contest_web.max_submission_bytes` | 1 MiB | a submission request; the task's source limit applies per file |
| `contest_web.max_user_test_bytes` | 8 MiB | a user test (sources and input) |
| `contest_web.max_print_bytes` | 2 MiB | a print job |
| `admin_web.max_upload_bytes` | 1 GiB | an admin upload (packages, testcase archives, contest archives) |
| `*.trusted_proxies` | — | proxies whose `X-Forwarded-For` is believed (the HTTPS proxy) |

Bodies over these limits are refused with 413 before anything is read
into memory or onto disk; plain forms are limited to 64 KiB (contestants)
and 1 MiB (administrators). Password checks (argon2id, 19 MiB each) run at
most one per core at a time (at least two), so a login storm queues
instead of exhausting memory.
