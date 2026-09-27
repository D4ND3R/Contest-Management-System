# Getting started: a complete contest from scratch

This guide walks someone who has never used the system from the first login
to the first verdict a contestant sees. Each step says **where** to click,
**what** to type and **what** should appear. If something does not happen
as described, [troubleshooting](faq.md) lists the usual causes.

CMS must be installed (see [deployment](deployment.md) or
[Docker](docker.md)). After installing you have three addresses:

| Site | For | Typical address |
|------|-----|-----------------|
| Contest | contestants | `https://<your domain>/<contest name>/` |
| Administration | organizers | `https://admin.<your domain>/` (or port 8889) |
| Public ranking | the public, a projector | `https://ranking.<your domain>/` (or port 8890) |

Every page has the same shape: a line at the top with the contest, its
phase and the time left; below it a row of links (the menu); tables in the
middle; and at the **foot of every page**, the language, the theme (light,
dark, high contrast or the system's) and the text size. The three apply as
soon as you change them.

## 1. Log in to the administration

1. Open the administration address. Type the user `admin` and the password
   the installer printed (on a development system `admin`/`admin`: the
   first login asks for a new one).
2. You see the **Dashboard**. The first menu row holds the server-wide
   sections: Dashboard, Contests, Task library, Users, Teams,
   Clarifications, Judges, Languages, Backups, Admins, Audit log and
   Server.
3. Top right: **Questions** (with a red number when some are unanswered),
   your user (your account: password and second factor) and **Log out**.

> Tip: create one administrator per person (**Admins**) instead of sharing
> `admin`. Every action is logged with who did it.

## 2. Choose the server's time zone

**Server → Time zone**. Type your zone's name (for example
`America/Mexico_City`, `America/Bogota`, `Europe/Madrid`; the list suggests
as you type) and **Save**.

From then on *every* time the three sites show (contest pages, rankings,
this panel, certificates) is in that zone, and the times you type in forms
are read in it. You can change it whenever you want: the stored moments do
not change, only how they are shown. The foot of every page says which zone
the times are in.

## 3. Create the contest

1. **Contests → New contest**.
2. **Name**: short, no spaces (letters, digits, `.`, `_`, `-`). It is the
   contest's address: `omi2026` → `https://<domain>/omi2026/`.
3. **Title shown to everybody**: the long name ("National Olympiad in
   Informatics 2026"). **Description**: a line shown under it.
4. **Status**: leave it as *draft* while you prepare it (contestants do not
   see it); switch to *published* when it is ready.
5. **Start** and **End**: in the server's zone.
6. **Mode**: *IOI* (scores and subtasks) or *ICPC* (solved and penalty).
7. **Submissions**: the **wait between submissions** starts at 20 seconds
   (it keeps anyone from flooding the judge; the contestant's Submit button
   counts down). The maximum number of submissions per task can stay empty.
8. **Scoreboard**: who sees it, when, whether it freezes at the end and
   which columns it shows (see [rankings](ranking.md)).
9. **All other settings** (folded) holds the rest: allowed languages,
   access, registration, tokens, printing... Usually nothing to change;
   they are explained in [contest settings](contest-settings.md).
10. **Create contest**. You land on the contest's dashboard. A second menu
    row now shows its pages: the contest, Problems, Participants,
    Submissions, Scoreboard, Announcements, Statistics, ..., Settings.

## 4. Create a problem, step by step

The full guide is [creating a problem](creating-a-problem.md); here is the
shortest path for a standard input/output problem.

1. In the contest: **Problems → New task**. A short **name** (`sum`) and a
   **title** ("Sum of two numbers") are enough. **Create task**.
2. You land on the task's **Configuration** window; the **Testcases**
   window is the other tab. A line on top says what is still missing, with
   a link to each part. They can be done in any order and on different
   days. (With a ready problem package, **Import a package** fills
   everything at once.)
3. **Files → Write a statement**: language `en`, the text in Markdown
   (formulas with `$...$`) and **Save**. The preview is on the right.
4. **Testcases** window, **One testcase**: type the input (`1 2`), the
   output (`3`), tick **Public** if contestants should see its result, and
   **Add testcase**. Repeat for each testcase; the codename is proposed (1,
   2, 3...).
5. If you have a correct solution, send it under **Test submissions** (same
   window). Then you can type only the inputs and pick *Output: written by
   the reference solution*; or generate many testcases with a program
   (**Generate with a program**). The testcase list shows the verdict of
   each test submission on each testcase.
6. **Configuration → Options**: limits, checker and **Scoring and
   subtasks**. By default each testcase is worth one point (*Sum*). For
   subtasks choose *GroupMin* and give each subtask its points and its
   testcases. **Save**.
7. The missing line should now only ask for a contest (or say *Ready*).
   Send the solution again to check it gets the full score.

## 5. Register contestants

- One at a time: **Users → New user**, then in the contest **Participants
  → Add users** with their username.
- Many at once: prepare a CSV whose first line is
  `username,password,first_name,last_name` and upload it in **Participants
  → Import users from CSV into this contest**. Check the preview (each row
  with its errors) and confirm. With empty passwords, tick *Generate missing
  passwords*.
- **Generate and print** makes a PDF of usernames, passwords and the
  contest's address to hand out.

## 6. Try it as a contestant

1. Set the contest to *published* and open `https://<domain>/<contest>/`
   in another window (or a private one).
2. Log in with a test user. The **Problems** page lists the problems with
   your score and submissions; each problem is also in the menu
   (`A. sum`).
3. Open the problem: the **Statement** tab; the **Submissions** tab has the
   form to submit and the list of your submissions.
4. Submit the solution (a file, or typed in *Or write the code here*). A
   "Submission sent" notice appears and the submission's row updates by
   itself: *Compiling…*, *Evaluating* with a progress bar, and the final
   verdict with another notice.
5. Open **details**: each subtask is a coloured block (green AC, yellow PA
   partial, red WA or another error) with its score; below, the result of
   each public testcase. Secret testcases are not listed one by one, but
   they count in the blocks.
6. Ask a question in **Clarifications**. In the administration the red
   number appears on **Questions**; answer it (tick *Show the question and
   the answer to everyone in the contest* if it helps everybody). The
   contestant gets a notice without reloading.

## 7. Contest day

Follow the [contest-day manual](contest-day.md): check the judging machine,
take a backup, rehearse with a couple of contestants, and during the
contest watch the dashboard (it refreshes by itself) and the questions.
