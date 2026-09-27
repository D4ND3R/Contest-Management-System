# Operations: alerts, point-in-time recovery, read replica, failover, rehearsals

What keeps a large contest running when something breaks: alerts that page
someone, a database that can be taken back to any moment, a replica for the
heavy reads and for failover, and rehearsals that replay a real contest
through the real judging pipeline.

## Alerts

The monitor service checks the system every few seconds. An alert is shown
to the administrators (a red notification and the **Alerts** section of the
Judges page) when a problem lasts, and once again when it is resolved:

| Alert | When |
| --- | --- |
| `queue_backlog` | more than `queue_depth` jobs wait to be judged, for 2 minutes |
| `no_workers` | a contest is running and no worker is alive, for 30 seconds |
| `judging_latency` | the median time from submission to score over the last 10 minutes exceeds `judging_latency`, for 2 minutes |
| `disk_space` | a disk of this server or of a worker has less than `disk_free_percent` free |
| `timing_drift` | a worker's judging cores measure time differently from the others (see [calibration](evaluation.md)) |
| `wal_archiving` | PostgreSQL cannot archive its WAL (point-in-time recovery would not be possible) |

Thresholds and an optional webhook in `cms.yaml`:

```yaml
monitor:
  alerts:
    queue_depth: 500
    judging_latency: 3m
    disk_free_percent: 10
    # Every alert is POSTed as JSON {"text": ..., "content": ...}: Slack,
    # Mattermost and Discord incoming webhooks, ntfy, ... Or CMS_ALERTS_WEBHOOK.
    webhook_url: https://hooks.example.org/...
```

The webhook URL is a secret (whoever has it can post to your channel):
keep it in `/etc/cms/cms.yaml` or the environment, never in Git.

Installations that already run Prometheus and Alertmanager can load
[`deploy/prometheus/alerts.yml`](../../deploy/prometheus/alerts.yml): the
same checks, plus server errors, slow pages, stale backups and services
that do not answer, on the metrics every service exports.

## Continuous archiving and point-in-time recovery

The daily backups (see [backups](backups.md)) take the whole contest back to
the moment they were taken. Continuous archiving goes further: PostgreSQL
hands every finished WAL segment to `cms ctl wal-archive`, and a
**base backup** plus the archived segments restore the database to any
moment, for instance to one second before somebody deleted the wrong task.

Segments go to `<backup dir>/wal` and, when backups have an S3 destination,
to its `wal/` prefix too. Archiving the same segment twice is harmless; a
different file under an existing name is refused (the `wal_archiving`
alert fires).

### Setting it up (Debian/Ubuntu, PostgreSQL installed by the installer)

PostgreSQL runs the archive command as the `postgres` user, which must not
read CMS's secrets. Give it a configuration of its own with only what the
commands need:

```sh
sudo install -d -o postgres -g postgres -m 700 /var/lib/postgresql/cms-archive
sudo tee /etc/postgresql/cms-wal.yaml >/dev/null <<'EOF'
# Read by PostgreSQL's archive and restore commands (user postgres).
database:
  url: postgres:///postgres?host=/var/run/postgresql
backup:
  dir: /var/lib/postgresql/cms-archive
EOF
sudo chown root:postgres /etc/postgresql/cms-wal.yaml
sudo chmod 640 /etc/postgresql/cms-wal.yaml

V=$(ls /etc/postgresql | sort -n | tail -1)
sudo tee /etc/postgresql/$V/main/conf.d/cms-archive.conf >/dev/null <<'EOF'
wal_level = replica
archive_mode = on
archive_command = '/usr/local/bin/cms ctl wal-archive -config /etc/postgresql/cms-wal.yaml %p %f'
# At most one minute of work lost even when little is written.
archive_timeout = 60
EOF
sudo systemctl restart postgresql
```

Then take a first base backup, and one every day (a systemd timer or cron,
as `postgres`):

```sh
sudo -u postgres cms ctl basebackup -config /etc/postgresql/cms-wal.yaml
```

Check: `sudo -u postgres psql -c 'SELECT archived_count, failed_count FROM pg_stat_archiver'`
shows segments being archived, and the System page shows no
`wal_archiving` alert. Keep the base backups of the last days and the
segments newer than the oldest one kept; older segments can be deleted.

### Restoring to a moment

1. Stop CMS everywhere: `sudo systemctl stop cms.target` on the main
   server and `sudo systemctl stop cms-worker` on the workers.
2. Stop PostgreSQL and keep the damaged data aside:

   ```sh
   sudo systemctl stop postgresql
   sudo mv /var/lib/postgresql/$V/main /var/lib/postgresql/$V/main.before-pitr
   sudo -u postgres install -d -m 700 /var/lib/postgresql/$V/main
   sudo -u postgres tar -xzf /var/lib/postgresql/cms-archive/base/<the newest before the moment>/base.tar.gz \
       -C /var/lib/postgresql/$V/main
   ```

3. Tell PostgreSQL where the segments are and where to stop:

   ```sh
   sudo tee /etc/postgresql/$V/main/conf.d/cms-recovery.conf >/dev/null <<'EOF'
   restore_command = '/usr/local/bin/cms ctl wal-restore -config /etc/postgresql/cms-wal.yaml %f %p'
   recovery_target_time = '2030-07-01 11:41:59+00'
   recovery_target_action = 'pause'
   EOF
   sudo -u postgres touch /var/lib/postgresql/$V/main/recovery.signal
   sudo systemctl start postgresql
   ```

4. Look at the data (`sudo -u postgres psql cms`). Too early or too late:
   stop, repeat from step 2 with another time. Right: finish the recovery
   with `SELECT pg_wal_replay_resume();`, then remove `cms-recovery.conf`.
5. Empty the job queues and check the audit chain, then start CMS:

   ```sh
   sudo -u cms cmsctl queue-drain
   sudo -u cms cmsctl audit-verify
   sudo systemctl start cms.target      # and cms-worker on the workers
   ```

   `queue-drain` matters: the recovered database hands out again the
   submission ids created after the moment, and jobs still queued for the
   old submissions would land on the new ones. The dispatcher enqueues
   again everything the database still needs judged. (`cmsctl restore`
   of a logical backup empties the queues itself.)

This procedure was verified end to end: archive through `cms ctl
wal-archive`, base backup, recovery through `cms ctl wal-restore` stopping
just before a destructive statement, data back.

## Read replica

The heavy reads that tolerate a second of delay can go to a PostgreSQL
streaming replica: the scoreboard sent to the ranking servers, the task
statistics, result exports and the plagiarism report. The contest site
and everything that writes keep using the primary (a contestant must see
their own submission immediately).

1. On the replica machine, with the same PostgreSQL version, clone the
   primary (a replication user with a password in `pg_hba.conf` on the
   primary):

   ```sh
   sudo systemctl stop postgresql
   sudo -u postgres rm -rf /var/lib/postgresql/$V/main
   sudo -u postgres pg_basebackup -h PRIMARY -U replicator -D /var/lib/postgresql/$V/main -R -X stream -P
   sudo systemctl start postgresql
   ```

2. On the main server, in `cms.yaml` (or `CMS_DATABASE_REPLICA_URL`):

   ```yaml
   database:
     url: postgres://cms:...@primary/cms
     replica_url: postgres://cms:...@replica/cms
   ```

3. Restart CMS. Each service's `/healthz` reports the replica as a check
   of its own.

If the replica stops, the pages that read from it fail until it is back or
`replica_url` is removed: judging and the contest site are not affected.

## Several contest web servers

The contest web server keeps nothing of its own: sessions are signed
cookies checked against the database, and rate limits, live notifications
and results go through Valkey. When one machine is not enough for the
contestants, run `cms contest-web` on more machines with the same
`cms.yaml` (the same `secret`, database and Valkey, and the blobs through
the blob server or S3, as for [an external worker](external-worker.md)),
and list them all in the proxy:

```
contest.example.org {
    reverse_proxy 10.0.0.11:8888 10.0.0.12:8888 {
        lb_policy least_conn
        health_uri /healthz
    }
}
```

A contestant can land on any of them, from one request to the next. The
admin and ranking servers are separate processes and scale on their own
(the ranking server is fed by the pusher; several ranking servers are
listed in `dispatcher.ranking_urls`).

## Failover

When the primary database machine is lost:

1. Promote the replica: `sudo -u postgres psql -c 'SELECT pg_promote()'`.
2. Point CMS at it: `database.url` becomes the replica's address and
   `replica_url` is removed; restart the services
   (`sudo systemctl restart cms.target`; workers do not use the database).
3. Configure archiving on the new primary (the section above) and build a
   new replica when the old machine is back. Never start the old primary
   again as a primary: two primaries means two diverging contests.

Submissions stored before the failure and not yet judged are not lost: the
job queues are in Valkey and the dispatcher's sweep enqueues anything
missing. What the old primary had written but not yet sent to the replica
(streaming replication is asynchronous: usually well under a second) is
lost; the audit log's hash chain (`cmsctl audit-verify`) shows where the
history ends.

Valkey keeps the queues on disk (append-only file) and survives restarts.
To survive losing its machine, run a replica and three Sentinels (on three
machines, e.g. the main server, the database replica and a worker) and
point CMS at the Sentinels:

```
# on the replica machine, valkey.conf
replicaof 10.0.0.1 6379
masterauth <the Valkey password>
requirepass <the Valkey password>

# sentinel.conf on each of the three machines (valkey-sentinel)
sentinel monitor cms 10.0.0.1 6379 2
sentinel auth-pass cms <the Valkey password>
sentinel down-after-milliseconds cms 5000
sentinel failover-timeout cms 30000
```

```yaml
# cms.yaml of every service and worker
redis:
  url: redis://:<the Valkey password>@unused/0
  sentinels: ["10.0.0.1:26379", "10.0.0.2:26379", "10.0.0.3:26379"]
  sentinel_master: cms
```

When the primary dies the Sentinels promote the replica and every service
follows it by itself. Replication is asynchronous, so the last instant of
queue activity can be lost: jobs are idempotent and the dispatcher's sweep
enqueues again every submission not judged, so nothing is lost for good.
Without Sentinels, a lost Valkey machine means installing a new one and
restarting the services, with the same sweep.

## Rehearsals

A rehearsal replays the submissions of a past contest into a rehearsal
contest through the real pipeline, with the original rhythm (or faster),
and measures how long judging took:

1. Import the past contest (a contest archive, see [backups](backups.md)),
   or use a contest of this installation.
2. Clone it from the admin panel (**Clone**, with its participants) and
   give the clone the time window of the rehearsal.
3. Replay:

   ```sh
   sudo -u cms cmsctl replay -from ioi2029 -to ioi2029-rehearsal -speed 10
   ```

   Contestants are matched by username and tasks by position; each
   submission gets the same files, language and relative time (divided
   by `-speed`). `-limit N` stops after N submissions. At the end, with
   `-wait`, it prints how many were judged and the median, 95th percentile
   and maximum time from submission to score.

Watch the System page and the alerts while it runs: a rehearsal at the
expected speed with every worker is the best test of a new installation.

## Chaos test

`go test -run TestChaos ./internal/dispatcher` (developers, with `make
test`'s services) judges submissions while dispatchers and workers are
killed and restarted at random, and checks that every submission ends
with exactly the right score, no evaluation is lost or duplicated and no
system error appears. It runs in the normal test suite.

## See also

- [Contest configuration in Git](contest-config.md)
- [Provisioning with Ansible](ansible.md)
- [Contest-day runbook](contest-day.md)
