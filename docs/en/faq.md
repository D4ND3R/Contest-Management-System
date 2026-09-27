# Troubleshooting

Common symptoms, their cause and what to do. If yours is not here, look at
**Judges** (judges and queues) and the **Audit log** in the
administration, and at the services' logs (`journalctl -u 'cms-*'` with
systemd, `docker compose logs` with Docker).

## Pages do not update by themselves

Pages get news through a connection kept open (*Server-Sent Events*). If a
proxy cuts or buffers it, the page notices within a minute and switches by
itself to asking every 8 seconds, so results still arrive, a little later.

- With Caddy or nginx in front, the `/<contest>/events` path must not be
  compressed or buffered. In nginx: `proxy_buffering off;` and
  `proxy_read_timeout 1h;` for that path (the server also sends
  `X-Accel-Buffering: no`). The installer's Caddyfile already does this.
- An antivirus or corporate proxy on contestants' computers can block the
  connection: the polling mode keeps working.

## Submissions stay at "Compiling…" or "Evaluating"

- **Judges**: are any judges *alive*? If not, start the judge service
  (`systemctl start cms-worker` or the `worker` container).
- If there are judges but the queue grows, they are too few for the load:
  add judges ([external worker](external-worker.md)).
- A *stuck* job (longer than its limit) is requeued by itself; you can also
  **requeue** it by hand from Judges.
- A submission with a **system error** (the contestant's page says
  *Evaluation failed (the organizers were notified)*) shows on the
  Dashboard; **reevaluate** it once the problem is fixed.

## Every submission ends in a system error

Almost always the sandbox: run the machine check ([verify the
host](verify-host.md)) and `cmsctl judge-selftest`. Check that `isolate` is
installed and that the judge's work directory exists.

## Times look wrong

Every time is shown in the **server's time zone** (**Server** in the
administration); the foot of every page says which one. Change it there:
the contest, the rankings and the panel follow at once. A user can have
their own zone (remote contestants) on their page.

## A contestant cannot log in

- Is the contest *published* and the user registered (**Participants**)?
- Is there an IP restriction and they come from another address?
- Are simultaneous logins blocked (another computer still open)?
- Generate a new password on their participation.

## A task does not give the expected score

- The task's **Setup** list says what is missing.
- Send the reference solution with the **Task tester** and open the
  submission: the per-testcase detail says what fails.
- If you changed testcases or subtasks, **reevaluate** (dataset page).

## An upgrade failed with a database error

`cmsctl upgrade` takes a backup before migrating and goes back to the
previous version if anything fails. Keep the whole message and the backup
file; see [deployment](deployment.md#upgrade).
