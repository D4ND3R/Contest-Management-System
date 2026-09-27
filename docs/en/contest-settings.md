# Contest settings

Everything below is on the contest's **Settings** page of the admin web server
(**Contests → the contest → Settings**, or the last link of the second menu
row). Changes apply at once: contestant pages pick them up within a second,
without restarting anything.

## How the form is laid out

In view, what almost every contest adjusts:

- **General**: name, title, description, status, start and end, time per
  contestant, mode (IOI or ICPC).
- **Submissions**: the **wait between submissions** (20 s in a new contest;
  empty = no wait), the maximum number of submissions per task, the
  maximum file size, which scores contestants see, the compiler's messages,
  testing, questions and printing.
- **Scoreboard**: who sees it, when, the freeze and the columns (see
  [rankings](ranking.md)); **More scoreboard options** (folded): ties,
  medals, freezing at an exact time, hidden users, anonymous, ICPC penalty
  and decimals.

Folded under **All other settings**: schedule (analysis, practice, appeals,
location and motto), languages, access and registration, scoring (score
mode of new tasks, teams, user test limits), tokens and printing.

Below the form: **Emergency controls** (pause submissions), **Extend the
contest**, **Copy this contest**, **Archive**, **Reevaluate the whole
contest** and **Delete**.

## Time zone

The times of the form (start, end, freeze, appeals) are in the **server's
time zone**, chosen on the **Server** page for every contest and the three
sites; the form reminds it under the dates. Changing it does not move the
contests: the same moment is shown in the new zone. A user can have their
own zone (remote contestants): **Time zone** on their page.

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
  administrator approves it. The contest's dashboard and Settings page show how many registrations
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

- **What the contestant sees of each submission**: the verdict (AC, PA
  partial, WA, TLE, ...), the score, **one block per subtask** with its
  points and verdict, and the result of each **public testcase** one by
  one. Non-public testcases are never listed one by one, but they count in
  the blocks. Make at least the first testcase (the examples) of each task
  public.
- **Scores shown to contestants**: as soon as they are known, only after the
  end, or never. Hidden scores also hide the verdicts and the blocks. The
  ranking has its own visibility ([rankings](ranking.md)): hide it too if it
  would give the scores away.
- **Show the compiler's messages** of failed and successful compilations.
- **Tokens** (contest and task rules): with tokens on, a contestant sees
  only the score of the public testcases until they play a token on a
  submission to see its full result. The task's Submissions tab shows the
  tokens available and when the next one comes. Without tokens (the usual)
  the full result is always shown.
- **Wait between submissions**: the contestant's Submit button counts the
  time left; the server refuses a submission sent too early.

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
- **Balloons** (in the contest menu): the staff list of every task
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

Unrestricted participations have no limits. The contest menu links to the
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

## Appeals

**Appeals until** (Settings → Lifecycle): when set, each contestant gets an
**Appeals** page once their contest is over, and can appeal a task
(optionally naming one of their submissions) until that time. The staff
answer on the contest's **Appeals** page (accept or reject, with an answer
the contestant reads); open appeals show on the dashboard. Accepting
records the decision only: correct the result with the usual tools (fix
the dataset and rejudge, or adjust the score), which are audited.

## Certificates

**Certificates** in the contest menu designs one certificate per
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
