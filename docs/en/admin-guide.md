# Administrator's guide

A contest from scratch in the admin panel (`https://admin.<your domain>`).
Every page is in English or Spanish (language switch at the bottom). The
other guides go deeper: [contest settings](contest-settings.md),
[task types](task-types.md), [problem packages](problem-package.md),
[contest day](contest-day.md), [backups](backups.md).

## 1. Administrators

**Admins → New admin**. Roles: *all* (everything), *messaging* (questions,
announcements, balloons, printing; read the rest) and *read only*. Each
administrator can turn on a second factor (**My account → Two-factor
authentication**, any TOTP app); an administrator with role *all* can reset
another's lost device. Every change is in the **Audit log**.

## 2. The contest

1. **Contests → New contest**: a name (letters, digits, `.`, `_`, `-`; it is
   the address, `https://<domain>/<name>/`), a description, the start and
   end in the contest's time zone.
2. Status *draft* while you prepare it (contestants cannot see it,
   administrators can); *published* when it is ready; *archived* after it
   (read-only, off the lists).
3. The rest, section by section — languages, per-contestant time windows,
   practice, IOI or ICPC, teams, tokens, feedback, score visibility, access
   and registration, submission limits, questions, printing — is described
   in [contest settings](contest-settings.md). Every field shows its
   meaning next to it.
4. **Sites** (optional): venues with their own start time, used to filter
   the ranking and the balloons.
5. Reusing last year's contest: **Copy this contest** at the bottom of its
   page (tasks and settings, optionally the participants), or import its
   [archive](backups.md#contest-archives).

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

The quickest way is a **problem package**: **Tasks → Import a package**,
drop the zip, check the preview (type, limits, subtasks, testcases,
statements, reference solutions) and confirm. The reference solutions are
judged at once and the validation report says whether each gets the
verdict its name announces. Packages from CMS (italy_yaml) and Polygon are
converted. The format and one example per type are in
[problem packages](problem-package.md).

By hand, **Tasks → Create task** and then, on the task page:

1. **General**: title, statements (PDF or HTML per language, one marked
   primary), attachments (files contestants download), submission files
   (`sol.%l` — `%l` becomes the language extension), languages (none
   ticked = the contest's).
2. **Scoring and feedback**: score mode (best per subtask as at the IOI
   since 2017, best submission, or tokened and last), precision, feedback
   (full, or only the first failure per subtask).
3. **Datasets → New dataset** (copy an existing one or start empty). On the
   dataset page:
   - **Task type** and its options (below), **Limits** (time, memory,
     output, source size).
   - **Score type**: *Sum* (points per testcase), *GroupMin* (a subtask
     scores only if all its testcases pass), *GroupMul*, *GroupThreshold*;
     the subtask editor asks for the points and the testcases of each
     subtask (a regular expression over the codenames, a count or a list).
   - **Testcases**: one by one (input, output, public) or **From a zip
     archive** with name patterns (`*.in`/`*.out`, `input*`/`output*`).
   - **Managers**: checker, graders, stubs, headers, interactor — the page
     lists the files the chosen configuration still needs (*Missing
     managers for this configuration*).
   - **Make live** when it is right: submissions are scored with the live
     dataset; a second dataset can judge new submissions in the background
     to compare before switching (*autojudge*).
4. **Task tester**: submit any source as an administrator and see the
   verdict per testcase without it counting anywhere; **Validation report**
   judges the reference solutions on every dataset.
5. Add the task to the contest (the contest page lists its tasks in order).

### Each problem type, step by step

| Type | Task type and options | Managers to upload |
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
solution with the task tester (or reference solutions) before the contest.

## 5. Before the contest

Follow [contest day](contest-day.md): verify the judging host, take a
backup and restore it, rehearse with a short contest
([drill](drill.md)), check that every task's validation report is green.

## 6. During the contest

- **Workers & queues**: queues, jobs in flight, stuck jobs (requeue),
  system errors, CPU/memory/disk.
- **Questions** and **Communication** (announcements, private messages);
  answers can be public.
- **Submissions**: filters (task, user, verdict, language, score, dates),
  source with highlighting, diff between two submissions, download as zip;
  **reevaluate** a submission, user, task or contest (recompile,
  re-evaluate or only rescore); **invalidate** a submission with a reason
  (restorable).
- **Participation** page: extra time, manual score adjustment with a reason
  (audited), sessions, "view as the contestant".
- **Extend the contest** for everybody from the contest page; **Balloons**
  and **Printing** for ICPC and on-site contests.

## 7. After the contest

- **Ranking**: unfreeze after the ceremony; export CSV, JSON or the
  printable PDF.
- **Statistics** per task; **Plagiarism** report per task with a
  side-by-side view.
- **Certificates**: design the template, download them all, let
  contestants download theirs ([contest settings](contest-settings.md#certificates)).
- **Archive** the contest (one zip, importable later) and take a final
  backup ([backups](backups.md)).
