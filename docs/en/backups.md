# Backups and restore

A CMS backup is **one file** holding the whole database and every stored
file (statements, attachments, testcases, managers, submissions, outputs).
Every part is hashed, and a restore only commits when all hashes match.

## What a backup contains

The file (`cms-backup-<UTC time>-<kind>.tar.zst`) is a zstd-compressed tar
archive:

| Member | Content |
|--------|---------|
| `cms-backup.json` | format version, time, CMS version, applied migrations, tables and columns |
| `db/<table>/<n>` | the table's rows (PostgreSQL `COPY` text format, in 8 MiB chunks) |
| `db/sequences.json` | the id sequences |
| `blobs/<sha256>` | every stored file, named after its SHA-256 |
| `manifest.json` | size and SHA-256 of every other member, rows per table, file count |

The database part is read from a single consistent snapshot; files are
immutable (content-addressed), so they are copied after the snapshot is
released and never block the database. Next to each archive a small
`.json` file records who took it, its duration, its size and the SHA-256 of
the whole file.

## Scheduled backups

The admin web server takes them (one at a time even with several admin
servers). In the configuration file:

```yaml
backup:
  dir: /var/lib/cms/backups
  interval: 24h           # when no contest is running (0 = off)
  contest_interval: 15m   # from 30 min before a contest starts to 30 min after it ends
  keep: 48                # scheduled backups kept; older ones are deleted
  max_rate: 32MiB         # read throttle per second (0 = unlimited)
  s3:                     # optional off-site copy of every backup
    endpoint: s3.example.org
    bucket: cms-backups
    access_key: ...
    secret_key: ...
    use_ssl: true
    prefix: backups/
```

`max_rate` keeps the contest web server fast on a small machine: a backup
reads at most that many bytes per second from PostgreSQL and the file
store. With the default (32 MiB/s) a 1 GiB installation is backed up in
about half a minute.

A failed backup (or a failed S3 copy) shows up in the admin panel as a
system alert and in the logs; the metric
`cms_backup_last_success_timestamp_seconds` lets your monitoring alert when
backups stop.

## From the admin panel

**Backups** (top menu) shows the schedule, the backup in progress and every
backup with its size, duration, S3 status and SHA-256. Full administrators
can take one immediately (**Back up now**), download it and delete it. Every
one of these actions is in the audit log (a backup contains the password
hashes: keep the files safe). `cmsctl upgrade` takes one of kind *upgrade*
before switching releases ([upgrade](deployment.md#upgrade)).

## From the command line

```sh
cmsctl dump                      # into backup.dir, listed in the admin panel
cmsctl dump -o /mnt/usb/cms.tar.zst
cmsctl dump -o - | ssh backup-host 'cat > cms.tar.zst'
cmsctl backups                   # list backup.dir
cmsctl backup-verify FILE        # check every hash (and the whole-file digest when the .json is next to it)
cmsctl restore FILE              # into an empty database
cmsctl restore -force FILE       # replace a database that already has data
```

### Restoring

1. Stop every CMS service (`systemctl stop 'cms-*'`); the ranking web
   server may keep running.
2. Point the configuration at the target database and file store (a new
   machine: install CMS first, see the deployment guide).
3. `cmsctl restore FILE`. The command
   - refuses a database that already holds data unless `-force` is given
     (then everything in it is dropped first);
   - applies the migrations the backup was taken with, loads every table in
     one transaction, stores every file (checking its SHA-256), checks the
     manifest, restores the sequences and the foreign keys, and only then
     commits;
   - applies the migrations that are newer than the backup (a backup taken
     with an older CMS version restores into a newer one).
4. Start the services again.

A damaged or truncated file is rejected before anything is committed.
Restoring into a newer schema than the backup's needs `-force`; a backup
from a newer CMS version than the installed one is refused.

### Drill

Before a contest, restore the latest backup into a scratch database to
make sure it works:

```sh
createdb cms_drill
CMS_DATABASE_URL=postgres://cms@localhost/cms_drill CMS_BLOB_DIR=/tmp/drill-blobs \
  cmsctl restore /var/lib/cms/backups/<latest>.tar.zst
dropdb cms_drill; rm -rf /tmp/drill-blobs
```

## Contest archives

A backup restores a whole installation. To keep **one contest** (to
archive it, move it to another server, or reuse it next year), write its
archive: a zip that any installation of the same CMS version or a newer one
imports as a new contest.

| Member | Content |
|--------|---------|
| `tables/<table>.jsonl` | the contest's rows, one JSON object per line, every column |
| `blobs/<sha256>` | every file those rows reference |
| `results.csv` | the final results table (for people; not imported) |
| `cms-contest.json` | format, CMS version, migrations, rows per table, file count |

The rows are the contest settings, sites, certificate template, tasks with **every** dataset
(statements, attachments, testcases, managers, the live dataset),
participants with their users and teams (password hashes included: keep
the file safe), announcements, questions and private messages, and,
unless left out, the submissions with their files, results, per-testcase
evaluations, tokens, per-task scores and manual score adjustments. Left out
on purpose: compiled executables (a reevaluation compiles again), user
tests, print jobs, balloons, the audit log and the administrators
(references to them are emptied).

- **Admin panel**: *Archive* on the contest's Settings page downloads it (with or
  without submissions); *Import a contest archive* at the bottom of the
  contests page creates the new contest.
- **Command line**:

  ```sh
  cmsctl contest-export final-2026 final-2026.zip            # -submissions=false for settings, tasks and participants only
  cmsctl contest-import final-2026.zip
  cmsctl contest-import -name final-2027 -task-suffix -2027 -status draft final-2026.zip
  ```

On import every row gets a new id and every reference is rewritten. Users
and teams that already exist (same username, same team code) are reused
as they are, not overwritten. The contest name and the task names must be
free (task names are unique in an installation: use a task name suffix);
otherwise nothing is written. By default the new contest is *archived*
(read-only, off the contest list) when the archive has submissions and a
*draft* otherwise. Files are checked against their SHA-256; a damaged file,
or an archive written by a newer CMS version, is refused before anything
is written. Both the download and the import are in the audit log.
