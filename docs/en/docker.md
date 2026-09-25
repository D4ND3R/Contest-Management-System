# Installing with Docker Compose

An alternative to the [one-line installer](deployment.md): the whole CMS
on one Linux machine as containers, from the images published with every
release (`ghcr.io/d4nd3r/contest-management-system/cms` and `/worker`, for
amd64 and arm64). The files are in [`deploy/docker/`](../../deploy/docker/).

Requirements: the same machine as for the installer — Linux on KVM or
dedicated hardware with **control groups v2**, amd64 or arm64 (see
[server requirements](deployment.md#server-requirements)) — with Docker
Engine 24 or later and the compose plugin. Docker Desktop (Windows, macOS)
cannot judge.

## Install

```sh
git clone --depth 1 https://github.com/D4ND3R/Contest-Management-System.git
cd Contest-Management-System/deploy/docker
./setup.sh --domain cms.example.org      # HTTPS for cms., ranking. and admin.cms.example.org
./setup.sh                               # or plain HTTP: contest :80, ranking :8080, admin :8081
```

`setup.sh` writes `.env` the first time (random database and Valkey
passwords, secret key and ranking token; the CPU and memory layout of this
machine), pulls the images, starts the stack, and creates the
administrator `admin` with a random password **printed once**. Run it
again whenever you like: the secrets and the administrator stay. Then judge
the security battery and the sample solutions twice inside the worker:

```sh
docker compose exec worker cms ctl judge-selftest
```

It must end with `RESULT: OK`; never start a contest if it does not.

## What runs

| Service | Image | Notes |
|---------|-------|-------|
| `postgres` | postgres:16 | durable commits, no parallel workers, memory from `.env` |
| `valkey` | valkey 8 | append-only, never evicts (the judging queues) |
| `init` | cms | applies the migrations before anything else starts |
| `contest-web`, `admin-web`, `ranking-web`, `dispatcher`, `monitor` | cms | on `WEB_CPUS` |
| `worker` | worker | the sandbox, on `JUDGE_CPUS` (one judging slot per CPU) |
| `caddy` | caddy 2 | HTTPS with automatic certificates, or plain HTTP |
| `printing` | cms | only with `docker compose --profile printing up -d` |

The configuration without secrets is `cms.yaml` (edit freely, then
`docker compose up -d`); the secrets and the machine layout are in `.env`
(keep it private). Data lives in named volumes: `pgdata`, `valkeydata`,
`blobs` (submissions, testcases), `ranking`, `backups`, `caddy`
(certificates).

**The worker is not privileged.** It gets what isolate needs and nothing
else: `CAP_SYS_ADMIN` (namespaces and mounts; Docker's default seccomp
profile then allows them), `CAP_NET_ADMIN` (the loopback interface of each
box), no AppArmor confinement (Docker's profile forbids mounts) and a
private cgroup namespace, whose cgroup v2 tree the entrypoint makes
writable and hands to isolate. The worker itself then runs as the
unprivileged user `cms`, like the other containers.

## Backups

The admin web server takes backups into the `backups` volume (every day,
every 15 minutes around a contest; **Backups** in the admin, or
`docker compose exec admin-web cms ctl dump`). Keep copies off the machine:

```sh
docker compose cp admin-web:/var/lib/cms/backups ./backups   # copy them out
```

To restore one ([details](backups.md)):

```sh
docker compose stop contest-web admin-web ranking-web dispatcher monitor worker
docker compose cp ./cms-backup-....tar.zst admin-web:/var/lib/cms/backups/
docker compose run --rm init ctl restore -force /var/lib/cms/backups/cms-backup-....tar.zst
docker compose up -d
```

## Upgrade

```sh
./setup.sh --version 1.3.0     # or set CMS_VERSION in .env
```

pulls the new images and recreates the containers; `init` applies the
migrations before the services start. Take a backup first (above), and do
not upgrade during a contest. Going back to an older version is only
possible by restoring a backup taken with it.

## Uninstall

```sh
docker compose down            # stops and removes the containers; the volumes (data) stay
docker compose down -v         # also deletes every volume: database, files, backups
```

Lost the administrator's password? `docker compose run --rm init ctl admin-password`.
