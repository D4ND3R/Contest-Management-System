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

## Submissions and tests

- **Maximum size of a submitted file** (empty: the server's
  `contest_web.max_submission_bytes`).
- Maximum submissions and minimum interval between them, per contest and per
  task. **User tests** (run a source on the contestant's own input) have their
  own count and interval limits.
