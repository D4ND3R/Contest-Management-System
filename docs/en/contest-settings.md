# Contest settings

Everything below is on the contest page of the admin web server (**Contests →
the contest**). Changes apply at once: contestant pages pick them up within a
second, without restarting anything.

## Lifecycle

- **Status**: *draft* contests do not exist for contestants (their pages
  answer 404; administrators can still preview them with *view as
  contestant*). *Published* is the normal state. *Archived* contests are
  read-only: contestants can log in and look at their submissions, but
  cannot submit, test or ask, and the contest is not listed.
- **Copy this contest** makes a draft with the same settings, sites and
  tasks (statements, attachments, datasets, testcases), optionally with the
  participants; never with submissions.
- **Extend the contest** moves the end for everybody at once; open pages
  update their clocks. For one contestant use *extra time* on the
  participation.
- **Practice after the contest**: after the end contestants keep submitting;
  those submissions are marked *unofficial* and never count in the ranking.

## Access

| Setting | Effect |
|---------|--------|
| Password login | Contestants log in with username and password. |
| Restrict by IP | Each participation only works from its IP addresses/subnets. |
| Automatic login by IP | A contestant whose participation has a single IP is logged in without a password from it. |
| Block simultaneous logins | A new login closes the previous session. |
| Block hidden users | Hidden participations cannot log in. |
| Accounts | *created by the organizers* (default); *self-registration, approved by an admin*; *self-registration with an invitation code*. |
| Invitation code | Needed with the code mode (6–100 characters). |
| Minimum password length | 4 to 128 (default 8); applies to passwords contestants choose. |
| Session duration (minutes) | Sessions end this long after login (empty: 24 hours). |

### Self-registration

With a self-registration mode the login page shows **Register**. The form asks
for a username (3–32 letters, digits, `.`, `_`, `-`), name, optional email and
institution, and a password with at least the minimum length, letters and
digits, different from the username.

- **With approval**: the account is created but cannot log in until an
  administrator approves it. The contest page shows how many registrations
  wait; **Participations** marks them *waiting for approval* with
  **approve** / **reject** buttons. Rejecting deletes the participation (the
  account stays, without this contest). Pending registrations are not in the
  ranking.
- **With invitation code**: a correct code admits the contestant at once and
  logs them in. Change the code to stop further registrations.

Registration is open while the contest is published and has not ended (or
while practice is on). An existing username cannot be registered again: the
organizers add that user to the contest instead.

## Results and feedback

- **Scores shown to contestants**: as soon as they are known, only after the
  end, or never. Hidden scores also hide the testcase details. The ranking has
  its own visibility ([rankings](ranking.md)): hide it too if it would give
  the scores away.
- **Show the compiler's messages** of failed and successful compilations.
- **Tokens** (contest and task rules): with tokens a contestant sees the full
  result of a submission during the contest. The contest page shows the tokens
  available and when the next one comes.

## ICPC contests

With the scoring mode **ICPC (solved + penalty)**:

- A submission is *accepted* when it gets the task's full score. Contestants
  see only a verdict: **Accepted**, **Wrong answer**, **Time limit
  exceeded**, **Memory limit exceeded**, **Runtime error**, **Output limit
  exceeded** (the first testcase that failed decides) or **Compilation
  failed**; never scores nor testcase details. Their overview shows the
  accepted tasks and the rejected attempts.
- The ranking orders by tasks solved, then penalty: minutes from the start to
  each accepted submission plus the *ICPC penalty per rejected attempt*
  (compilation errors do not count). Freeze and unfreeze as in
  [rankings](ranking.md).
- **Balloons** (link on the contest page): the staff list of every task
  solved by a team, oldest first, with the site and the solver, the first
  solve of each task marked. It updates by itself; the *delivered* button
  moves a balloon to the delivered list (with who and when; *undo* brings it
  back). Filter by site to hand out balloons room by room. Staff with the
  *messaging* role can mark deliveries; hidden participations get no
  balloons.

## Printing

With **Printing** checked (Access and features), contestants get a
*Printing* page during the contest (not in practice or analysis): they send a
PDF or a plain text file (UTF-8, typically their source code, printed in a
monospaced font with line numbers and a header with their username, the file
name and page numbers). The pages are counted when the file is sent, and the
limits apply at once:

| Setting | Effect |
|---------|--------|
| Max. print jobs per user | Jobs a contestant may send (failed or cancelled ones do not count). |
| Max. pages per job | Longer documents are refused. |
| Max. pages per contestant | Pages in all (empty: no limit). |

Unrestricted participations have no limits. The contest page links to the
staff **Printing** queue: *printed, to deliver* (with **delivered**, and
**reprint** for a lost copy), *waiting for the printer* (with **cancel**),
*not printed* (with the reason and **print again**) and *delivered* (with who
and when, **undo**); every document can be opened as PDF. It updates by
itself and filters by site. The contestant's page follows the state of each
job live. Setting up the printer: [deployment](deployment.md#printing).

## Submissions and tests

- **Maximum size of a submitted file** (empty: the server's
  `contest_web.max_submission_bytes`).
- Maximum submissions and minimum interval between them, per contest and per
  task. **User tests** (run a source on the contestant's own input) have their
  own count and interval limits.

## Certificates

**Certificates** on the contest page designs one certificate per
contestant (A4 landscape) from the final, unfrozen ranking:

- a **title**, a **text** and a **footer** with placeholders: `{name}`,
  `{first_name}`, `{last_name}`, `{username}`, `{institution}`, `{team}`,
  `{site}`, `{contest}` (the contest description, or its name), `{rank}`,
  `{participants}`, `{score}` (problems solved in ICPC contests),
  `{max_score}`, `{award}` and `{date}` (the *Date* field: a date, or a
  place and a date). Paragraphs are separated by blank lines; one starting
  with `#` is printed large and bold, one with `##` bold, and a paragraph
  that its placeholders leave empty (`## {award}` without an award) is
  skipped;
- **awards** by rank, one per line, `Name: last rank`, ranks growing
  (`Gold medal: 3`, `Silver medal: 8`, `Bronze medal: 15`, `Honourable
  mention: 25`); ties share the rank and the award;
- who receives one: everybody in the ranking (hidden contestants never), or
  only from a **minimum score**, or **only contestants with an award**;
- up to three or four **signatures** (`Name | Role` per line) and a
  **logo** (PNG or JPEG, up to 2 MiB) at the top.

*Preview the first one* shows a page; *Download all* gives one PDF with a
page per contestant, and each participation page has the contestant's
own. Both downloads are in the audit log. With **contestants download
their own certificate**, each contestant sees a *Download your
certificate* link on the contest page once their contest time is over;
turn it on after the closing ceremony if ranks should stay secret until
then.
