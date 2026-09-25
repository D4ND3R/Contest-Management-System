# Deploying on a VPS (Debian / Ubuntu)

This guide installs every CMS service on one clean machine running Debian
12+ or Ubuntu 22.04+. The reference target is a **2 vCPU KVM VPS with 4 GB
of RAM**: CPU 0 runs the web servers, PostgreSQL, Valkey and the HTTPS
proxy; CPU 1 is reserved for the sandbox, so the queue of submissions never
slows the contest web server down. Bigger machines work the same way (from 6
CPUs up, CPUs 0–1 serve the web and the rest judge); more judging power
comes from [workers on other machines](external-worker.md).

`scripts/install.sh` does everything below; the manual steps are listed so
you know what it changes and can adapt it.

## 0. Before you start

- A machine with KVM (or bare metal) virtualisation: container VPSs
  (OpenVZ, LXC) cannot run the sandbox.
- DNS: three names pointing at the machine, e.g. `cms.example.org`
  (contestants), `admin.cms.example.org` and `ranking.cms.example.org`.
  For a contest on a local network without Internet, use `--lan` instead.
- SSH access as a user with sudo.

## 1. Quick install

```sh
sudo apt-get install -y git make golang   # only to build; or copy bin/ from a release
git clone https://github.com/D4ND3R/Contest-Management-System.git /opt/cms-src
cd /opt/cms-src && make build
sudo scripts/install.sh --domain cms.example.org --email you@example.org \
                        --admin-allow 203.0.113.0/24     # optional: who may open the admin
sudo reboot            # only if the script says cgroup v2 had to be enabled
sudo cms-verify-host --config /etc/cms/cms.yaml
```

Then open `https://admin.cms.example.org/`, log in as `admin` with the
`ADMIN_PASSWORD` stored in `/etc/cms/secrets.env`, and change it (Account →
password; enable two-factor authentication).

Useful options: `--web nginx` (nginx + certbot instead of Caddy), `--lan`
(plain HTTP: contest on port 80, ranking 8080, admin 8081), `--languages
full` (all twelve toolchains instead of C, C++, Python and Java),
`--private-ip 10.8.0.1` (let [external workers](external-worker.md) reach
this server), `--no-firewall`. `--render-only DIR` writes every file the
script would generate under `DIR` without touching the system.

The script can be run again at any time (after `git pull && make build`, to
upgrade): it keeps `/etc/cms/cms.yaml` and the secrets, rewrites the other
generated files only when they change, applies the database migrations and
restarts the services.

## 2. What the script does, step by step

### Packages

`gcc g++ python3 openjdk-17/21-jdk-headless` (plus `pypy3 fp-compiler rustc
golang-go kotlin mono-mcs ghc` with `--languages full`), `postgresql`,
`valkey-server` (or `redis-server` where Valkey is not packaged), `caddy`
(or `nginx certbot python3-certbot-nginx`) and `ufw`.

### isolate and cgroup v2

`scripts/install-isolate.sh` builds isolate 2 from source, installs it
setuid root with the configuration `/usr/local/etc/isolate` (boxes under
`/var/local/lib/isolate`, uids from 60000) and enables `isolate.service`,
which keeps the control group isolate uses. isolate 2 needs the unified
cgroup v2 hierarchy (the default on Debian 11+ and Ubuntu 21.10+); on an
older setup the script adds `systemd.unified_cgroup_hierarchy=1` to the
kernel command line and asks for a reboot.

### User and files

| Path | Content |
|------|---------|
| `/usr/local/bin/cms`, `cmsctl` | the binaries |
| `/usr/local/sbin/cms-verify-host` | `scripts/verify-host.sh` |
| `/etc/cms/cms.yaml` | configuration (group `cms`, mode 640) |
| `/etc/cms/secrets.env` | generated passwords and tokens (root, mode 600) |
| `/etc/cms/languages/` | language definitions |
| `/var/lib/cms/{blobs,ranking,backups,worker}` | data (user `cms`) |
| `/var/cache/cms/` | the worker's testcase cache |

Every service runs as the unprivileged system user `cms`.

### PostgreSQL for 2 vCPUs

`/etc/postgresql/<version>/main/conf.d/cms.conf`: `shared_buffers` = RAM/8
(128 MB–2 GB), `effective_cache_size` = RAM/2, `work_mem 8MB`,
`max_connections 100`, **no parallel query workers** (they would take the
judging core), `jit off` (short queries do not pay it back), `synchronous_commit
on` (no accepted submission is ever lost), `wal_compression on`,
`random_page_cost 1.1` (SSD). The role and database `cms` get a random
password. The services use at most 16 connections each (`database.max_conns`).

### Valkey

`/etc/valkey/cms.conf` (included from `valkey.conf`): listens on localhost
only (plus `--private-ip`), password required, `appendonly yes` with
`appendfsync everysec` and `maxmemory-policy noeviction` — the judging
queues live there and must survive a restart.

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

**CPU pinning.** Drop-ins (`/etc/systemd/system/<unit>.d/cpu.conf`) set
`CPUAffinity=0` for every CMS service but the worker, and for PostgreSQL,
Valkey and the proxy. The worker is not confined: it pins each sandbox to
its judging cores (`worker.cores: [1]` in `cms.yaml`) and its own threads
to the other CPUs.

### HTTPS

With Caddy, `/etc/caddy/Caddyfile` has one site per name, each a
`reverse_proxy` to the local service; Caddy obtains and renews the Let's
Encrypt certificates by itself and streams server-sent events as they
come. `--admin-allow CIDR` answers 403 to anyone else on the admin site.
With nginx, `/etc/nginx/sites-available/cms` proxies with
`proxy_buffering off` (server-sent events) and `certbot --nginx` adds the
certificates. The CMS services listen on 127.0.0.1 only and trust
`X-Forwarded-For` from the proxy (`trusted_proxies`), so IP restrictions
and logs see the contestants' addresses.

### Firewall

`ufw`: deny incoming except SSH, 80 and 443 (on a LAN: 80, 8080 and 8081;
the admin port only from `--admin-allow`). With `--private-ip`, ports 6379
(Valkey) and 8891 (blob server) are open on that address only.

### Database and first administrator

`cmsctl bootstrap` applies the migrations and creates the administrator
`admin` with the generated password. Upgrades apply new migrations the
same way (`cmsctl migrate`).

## 3. After installing

1. `sudo cms-verify-host --config /etc/cms/cms.yaml` must end with
   `RESULT: OK` ([details](verify-host.md)). **Never start a contest on a
   host where it fails.**
2. Log in to the admin, change the password, enable 2FA.
3. Create or import a contest and its tasks ([problem
   packages](problem-package.md)), add the users.
4. Check **Backups** in the admin: a backup is taken every day, every 15
   minutes around a contest ([backups](backups.md)).
5. Read the [contest-day runbook](contest-day.md).

## Monitoring

- `https://admin.../system`: workers, queues, running jobs.
- Every service answers `/healthz`; Prometheus metrics on `/metrics` (web
  services) and on 127.0.0.1:9101–9104 (dispatcher, worker, monitor,
  printing). Useful alerts: `up == 0`, `cms_backup_last_success_timestamp_seconds`
  older than 30 minutes during a contest, queue length growing for minutes.
