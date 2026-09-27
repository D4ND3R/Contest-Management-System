# Administrator's guide

A contest from scratch in the admin panel (`https://admin.<your domain>`).
If you have never used the system, start with
[getting started](getting-started.md), which walks the same path in more
detail. The other guides go deeper: [creating a problem](creating-a-problem.md),
[contest settings](contest-settings.md), [task types](task-types.md),
[problem packages](problem-package.md), [clarifications](clarifications.md),
[contest day](contest-day.md), [backups](backups.md). Technical words are
in the [glossary](glossary.md).

## 0. How the panel is laid out

- **Top**: the contest you are looking at, its phase and the time left; on
  the right **Questions** (with the number pending), your account and
  **Log out**.
- **First menu row**, the whole server: Dashboard, Contests, Task library,
  Users, Teams, Clarifications, Judges, Languages, Backups, Admins, Audit
  log, Server.
- **Second row**, the current contest (the one you are looking at; on the
  home page, the running one or the next): its dashboard, Problems,
  Participants, Submissions, Scoreboard, Announcements, Statistics,
  Appeals, Printing and Balloons (when they apply), Plagiarism,
  Certificates, Sites, Settings.
- **Foot**: language, theme and text size (applied at once), and the time
  zone times are shown in.

## 1. Administrators

**Admins → New admin**. Roles: *all* (everything), *messaging* (questions,
announcements, balloons, printing; read the rest), *task setter* (prepare
tasks: statements, datasets, testcases, graders, test submissions; making a
dataset live, rejudging and deleting stay with *all*), *read only*, and
*leader* for delegation leaders: linked to a team, they see only their
contestants' submissions, sources and the results the contestants see
(**My delegation**), and cannot submit. Each
administrator can turn on a second factor (**My account → Two-factor
authentication**, any TOTP app); an administrator with role *all* can reset
another's lost device. Every change is in the **Audit log**.

## 2. The contest

0. **Server → Time zone**: the zone of every time on every site (and of the
   ones you type in forms). It can change at any moment.
1. **Contests → New contest**: a name (letters, digits, `.`, `_`, `-`; it is
   the address, `https://<domain>/<name>/`), a title, a description, the
   start and the end.
2. Status *draft* while you prepare it (contestants cannot see it,
   administrators can); *published* when it is ready; *archived* after it
   (read-only, off the lists).
3. The form shows three parts — **General**, **Submissions** (the wait
   between submissions, 20 s by default; the maximum number of submissions;
   which scores contestants see) and **Scoreboard** (who sees it, when, the
   freeze, the columns) — and folds the rest under **All other settings**:
   languages, per-contestant windows, practice, appeals, access and
   registration, teams, tokens, printing. Everything is described in
   [contest settings](contest-settings.md).
4. **Sites** (optional): venues with their own start time, used to filter
   the ranking and the balloons.
5. Reusing last year's contest: **Copy this contest** at the bottom of its
   Settings page (tasks and settings, optionally the participants), or
   import its [archive](backups.md#contest-archives).

## 3. Contestants

- **Users → Import CSV**: columns `username, password, first_name,
  last_name, email, institution, country, region, timezone, team` (code),
  `site, ip, hidden, unrestricted, delay_time, extra_time` (header row
  required, any subset and order). The preview lists every row with its errors before anything is
  created; tick *and add to* to enrol them in the contest at once.
  *Generate missing passwords* fills the empty ones.
- **Participations** of the contest: overrides per contestant (password,
  allowed IPs, extra or delayed time, hidden, unrestricted), bulk actions,
  approval of registrations, **Generate and print** the credentials sheet
  (PDF, 1–8 per page, with the contest address).
- **Teams**: code, name, flag and institution; in team contests the members
  share submissions and limits.

## 4. Tasks

Each task has two windows (tabs on top of its page); everything is
explained step by step in [creating a problem](creating-a-problem.md):

1. **Configuration**:
   - **Import a package**: a zip (this system's format, CMS italy_yaml or
     Polygon) fills the options, the files and the testcases after a
     preview; its reference solutions are sent as test submissions.
   - **Options**, which are the task's `problem.yaml` as a form: name,
     title, task type and its options (below), limits, checker, **scoring
     and subtasks** (*Sum*, *GroupMin*, *GroupMul*, *GroupThreshold*; the
     subtask editor asks for the points and the testcases of each subtask:
     a regular expression over the codenames, a count or a list; with
     GroupMin/GroupMul, **Short-circuit** skips the rest of a subtask once
     a testcase scores 0, see [evaluation](evaluation.md)), submission
     files (`sol.%l` — `%l` becomes the language extension), languages
     (none ticked = the contest's), score mode (best per subtask as at the
     IOI since 2017, best submission, tokened and last, or the last
     submission that compiled), precision, feedback, checker messages,
     tokens and submission limits. **Edit problem.yaml as text** edits the
     same options in the package format.
   - **Files**: statements, judging files (checker, graders, stubs,
     headers, interactor, manager) and attachments, uploaded one by one
     (each is known by its name); the files the options need and are
     missing show in yellow ([statements](statements.md)).
   - **Datasets**: the *live* one scores submissions; a copy can be
     prepared, judged in the background, **compared with live** and **made
     live**.
2. **Testcases**: add them one by one (input typed or uploaded; output
   typed, uploaded, empty or written by the reference solution), with a
   **generator** run once per line of parameters, or from a zip; the list
   of testcases with the verdict of the last four **test submissions** on
   each one; the test submission form (any source, judged without counting
   anywhere); and the statement's **Examples**. The **Validation report**
   judges a package's reference solutions and says whether each gets the
   verdict its name announces.

A package can also create a new task: **Task library → Import a problem
package** ([problem packages](problem-package.md)).

### Each problem type, step by step

| Type | Task type and options | Judging files to upload |
|------|-----------------------|--------------------|
| Standard input/output | *Batch*, compilation *alone*, comparison *ignore whitespace* (or *exact*, *reals with tolerance* with absolute/relative tolerance) | none |
| Custom checker | *Batch*, comparison *custom checker (CMS protocol)* or *(testlib)* | `checker` (binary), `checker.cpp` or `checker.c`; `testlib.h` for testlib |
| Files instead of stdin/stdout | *Batch*, **Input file** / **Output file** (e.g. `input.txt`, `output.txt`) | none |
| Function to implement (grader) | *Batch*, compilation *grader* | `grader.c`, `grader.cpp`, `grader.java`, `grader.py`… one per language, plus headers (`task.h`) |
| Output only | *Output only*; optionally *Missing outputs take the best previous result of each testcase*; output names `output_%s.txt` | none (contestants upload one file per testcase or a zip) |
| Two steps | *Two steps* | `manager.cpp` (or `.c`, binary) |
| Communication (manager + N processes) | *Communication*: processes, contestant I/O (*FIFO paths as arguments* or standard I/O), limits per process or in total, compilation *stub* | `manager` (binary, `.c`, `.cpp`), stubs `stub.c`, `stub.cpp`, `stub.py`… |
| Interactive (ICPC style) | *Interactive*: interactor time and memory limits | `interactor` (binary, `.c`, `.cpp`; `testlib.h` if it uses testlib) |

The protocols (arguments, exit codes, what the checker or interactor prints)
are in [task types](task-types.md). Always judge a correct and a wrong
solution with a test submission (or reference solutions) before the contest.

## 5. Before the contest

Follow [contest day](contest-day.md): verify the judging host, take a
backup and restore it, rehearse with a short contest
([drill](drill.md)), check that every task's validation report is green.

## 6. During the contest

- The contest **dashboard** (its page, and the home page while it runs)
  refreshes by itself every 20 seconds: at the top what needs someone
  (unanswered questions, submissions that could not be judged, stuck jobs,
  registrations to approve, the end approaching, a frozen ranking); then,
  in tables, each problem (who solved it, partial scores, submissions), the
  verdict counts, the health of judges, queue, database, disks and
  backups, the top of the scoreboard and the latest events.
- **Judges**: queues, jobs in flight, stuck jobs (requeue), system errors,
  CPU/memory/disk.
- **Clarifications** and **Announcements**: answer one person or everyone,
  general announcements and private messages ([clarifications](clarifications.md)).
  Contestants get them at once.
- **Submissions**: filters (task, user, verdict, language, score, dates),
  source with highlighting, diff between two submissions, download as zip;
  **reevaluate** a submission, user, task or contest (recompile,
  re-evaluate or only rescore); **invalidate** a submission with a reason
  (restorable).
- **Suspicious submissions** (sources that start programs, open sockets,
  make raw system calls..., or programs the seccomp filter killed) are
  flagged: a tag and a filter in the submissions, a notification on the
  dashboard; see [security](security.md).
- **Participation** page: extra time, manual score adjustment with a reason
  (audited), sessions, "view as the contestant".
- **Extend the contest** or **pause submissions** from its Settings page;
  **close submissions** of a single task in Problems; **Balloons** and
  **Printing** for ICPC and on-site contests.

## 7. After the contest

- **Ranking**: unfreeze after the ceremony; export CSV, JSON or the
  printable PDF.
- **Statistics** per task; **Plagiarism** report per task with a
  side-by-side view.
- **Certificates**: design the template, download them all, let
  contestants download theirs ([contest settings](contest-settings.md#certificates)).
- **Archive** the contest (one zip, importable later) and take a final
  backup ([backups](backups.md)).
