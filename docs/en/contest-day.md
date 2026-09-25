# Contest-day runbook

A checklist for the people running the contest. "Admin" means the admin
web site; commands run on the main server unless noted.

## The day before

- [ ] Upgrade if needed (`git pull && make build && sudo scripts/install.sh …`
      with the same options) — never on the contest day itself.
- [ ] Every task validated: the task page's **Validation** report shows
      every reference solution with the expected verdict.
- [ ] Contest settings: start and end time (in UTC on the server; check the
      timezone shown to contestants), languages, submission limits, tokens,
      ranking settings (visibility, freeze minutes).
- [ ] Users imported, passwords printed or sent (**Users → credentials PDF**),
      teams and sites assigned.
- [ ] Take a backup (**Backups → Back up now**) and restore it into a
      scratch database ([drill](backups.md#drill)).

## One hour before

- [ ] `sudo systemctl stop cms-worker && sudo cms-verify-host --config /etc/cms/cms.yaml --languages <the contest's languages>; sudo systemctl start cms-worker`
      on every judging machine. **If it reports `RESULT: FAIL`, do not
      start the contest** on that machine: fix it or stop its worker.
- [ ] **Workers & queues**: every worker alive, queues empty.
- [ ] Rehearsal: log in as a test contestant (or **view as contestant** on a
      participation), submit an accepted and a wrong solution to each
      task, check the verdicts, the scores and the ranking; then delete the
      test participation (or keep it hidden).
- [ ] **Backups**: the schedule shows "every 15m during contests"; a backup
      appeared after the rehearsal.
- [ ] Announcements ready; the questions inbox open in a browser tab (the
      counter in the menu updates live).

## During the contest

**What to watch**

- **Workers & queues** also shows the CPU, memory and free disk of this
  server and of every worker's machine, and the size of the blob store and
  the database: keep an eye on free disk.
- **Workers & queues**: all workers alive; waiting jobs should drain within
  seconds. A queue that keeps growing means not enough judging power (add
  a worker, see [external workers](external-worker.md)) or a stuck worker.
- **Questions** (menu counter): answer privately or to everyone; the quick
  answers save time.
- System alerts (red notices in the admin): evaluation errors after the
  retries, failed backups.
- `journalctl -u 'cms-*' -p warning -f` on the main server.
- ICPC contests: the **Balloons** page (contest page) open for the runners;
  it updates by itself.
- With printing: the **Printing** queue (contest page) open for the staff
  who hand out the pages.

**Extending time**

- For everyone: edit the contest and move the end time; contestants' clocks
  follow within seconds.
- For one participant (e.g. a broken computer): **Participations → the
  participant → extra time** (seconds); with per-user time windows, **delay**
  moves their start.

**Rejudging**

- Wrong testcase or checker: fix the dataset (or create a new one and make
  it live — scores are recomputed automatically), then **Reevaluate** on
  the task: *rescore* (only recompute scores), *reevaluate* (run again),
  *recompile* (compile and run again). The same buttons exist per
  submission, per user and for the whole contest.
- A score that must change by hand (e.g. a jury decision): **Participations
  → the participant → Adjust a score**: points to add (negative to remove)
  and a mandatory reason. Adjustments are kept for good (a correction is
  another adjustment), shown to the contestant with the reason, audited,
  and survive any rejudge; rankings update at once.
- A submission that must not count (cheating, a mistake of the
  organisers): **Invalidate** it on its page with the reason; the
  contestant sees the reason and the score updates at once. It can be
  restored.

**When something breaks**

| Symptom | What happens | What to do |
|---------|--------------|------------|
| A worker crashes or its machine dies | After 10 s without heartbeat its jobs go back to the queue and other workers take them. | Restart it (`systemctl restart cms-worker`); nothing to redo. |
| A job looks stuck (flagged *stuck?* on **Workers & queues**: running for over 2 minutes or on a dead worker) | The monitor requeues it by itself after the job timeout (10 minutes) or 10 s after the worker's heartbeat stops. | Don't wait: **requeue** takes it back and queues it again at once. |
| A job fails 3 times | The submission shows "evaluation failed"; an alert appears. | Look at the alert (usually a checker or a missing toolchain), fix, **Reevaluate** that submission. |
| A web service crashes | systemd restarts it within 2 s; sessions survive. | Check `journalctl -u cms-contest-web`. |
| The VPS reboots | Everything starts again by itself; Valkey keeps the queues (append-only file) and PostgreSQL the data; the dispatcher re-sends whatever was in flight. | Check **Workers & queues** and one submission. The contest clock does not stop: extend the time if the outage was long. |
| The database is lost | — | Restore the last backup (15 minutes old at most) on a new machine: [backups](backups.md). Submissions made after it are lost: announce it and extend the time. |
| The ranking web server is down | Contestants are not affected; the dispatcher pushes the full board when it is back. | `systemctl restart cms-ranking-web`. |

## At the end

- [ ] Freeze: with "freeze the last N minutes" the public ranking stopped
      updating before the end; after the ceremony, **Ranking → unfreeze**.
- [ ] Wait until **Workers & queues** shows no waiting jobs and no pending
      submissions.
- [ ] Answer or close the remaining questions.
- [ ] Export the results: **Ranking → CSV / JSON / Printable PDF** (A4
      landscape, header on every page, filtered by site if one is chosen).
- [ ] Final backup (**Back up now**), download it and store it off the
      server.
- [ ] Archive the contest (contest page → **Archive**, with the
      submissions): one zip that any later installation imports
      ([contest archives](backups.md#contest-archives)).
- [ ] Optional: turn on analysis mode or let contestants download their
      submissions.
