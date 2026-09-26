# Adding a worker on another machine

A worker compiles and runs submissions. The main server already runs one
(on its judging core); for a bigger contest add machines that only judge.
A remote worker needs two things from the main server:

- **Valkey** (port 6379): the job queues;
- the **blob server** (port 8891, `cms blob-server`): testcases, managers
  and submissions, and a place to upload executables and outputs.

It never talks to PostgreSQL. Both ports carry contest data and must only
be reachable over a private network. The simplest is WireGuard between the
machines; a provider's private network (VPC) works too.

## 1. Private network (WireGuard)

On both machines: `sudo apt-get install -y wireguard` and
`wg genkey | tee private.key | wg pubkey > public.key`.

Main server, `/etc/wireguard/wg0.conf`:

```ini
[Interface]
Address = 10.8.0.1/24
ListenPort = 51820
PrivateKey = <main private key>

[Peer]           # one block per worker
PublicKey = <worker public key>
AllowedIPs = 10.8.0.2/32
```

Worker, `/etc/wireguard/wg0.conf`:

```ini
[Interface]
Address = 10.8.0.2/24
PrivateKey = <worker private key>

[Peer]
PublicKey = <main public key>
Endpoint = cms.example.org:51820
AllowedIPs = 10.8.0.1/32
PersistentKeepalive = 25
```

On both: `sudo systemctl enable --now wg-quick@wg0`; on the main server also
`sudo ufw allow 51820/udp`. Check with `ping 10.8.0.1` from the worker.

## 2. Main server: listen on the private address

```sh
sudo bash /opt/cms/current/scripts/install.sh --domain cms.example.org --private-ip 10.8.0.1   # same options as before
```

This makes Valkey also listen on 10.8.0.1, sets `blob_server.listen:
10.8.0.1:8891` (for a `cms.yaml` written before, add it by hand), enables
`cms-blob-server` and opens 6379 and 8891 on 10.8.0.1 only. Take note of
`REDIS_PASSWORD` and `BLOB_TOKEN` in `/etc/cms/secrets.env`.

## 3. Worker machine

```sh
curl -fsSL https://raw.githubusercontent.com/D4ND3R/Contest-Management-System/main/scripts/install.sh | sudo bash -s -- \
     --role worker --version <the main server's version: cms version> --main 10.8.0.1 \
     --redis-password <REDIS_PASSWORD> --blob-token <BLOB_TOKEN> --worker-name judge-2
```

If the main server's Valkey is not on 6379 (its installer says so when
another program had that port; `redis.url` in its `/etc/cms/cms.yaml`
shows it), add `--redis-port <that port>`.

The installer ends by running `cms-verify-host`, which must pass. Keep
every worker on the main server's version: after `sudo cmsctl upgrade` on
the main server, run `sudo cmsctl upgrade -version <the same>` on each
worker (a worker has no database: it only switches the release and
restarts).

The worker's `cms.yaml` uses `redis.url: redis://:<password>@10.8.0.1:6379/0`
and the `http` blob backend (`blob.http.url: http://10.8.0.1:8891`) with a
4 GiB local testcase cache; it judges on every CPU but the first. Only
`cms-worker` is enabled, the firewall only allows SSH.

Within seconds the worker appears in the admin under **Workers & queues**
and starts taking jobs. Give every worker a distinct `--worker-name`.

## Notes

- Hardware: every judging machine should have the same CPU model and
  settings, or the same solution may get different times on different
  workers. `cms-verify-host` warns about turbo boost, SMT and frequency
  scaling; fix them on dedicated machines.
- A worker that stops (crash, network, reboot) loses nothing: after
  `monitor.heartbeat_timeout` its jobs go back to the queue and another
  worker takes them.
- To stop a worker for maintenance: `sudo systemctl stop cms-worker` (it
  finishes its running jobs first; anything left is requeued).
- Blobs read by a worker are verified against their SHA-256; the blob
  server only accepts the token and never deletes anything.
