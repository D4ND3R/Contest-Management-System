# Creating a problem

Every task has **two windows**, as tabs on top of its page:

- **Configuration**: importing a zip that fills everything, the task's
  **options** (type, limits, checker, scoring... that is, its
  `problem.yaml`, edited as a form) and its **files** one by one.
- **Testcases**: adding testcases by hand or importing them, seeing them in
  a list and sending **test submissions** against them.

You can do it all at once with a zip, or in parts, in any order and on
different days.

## Vocabulary

| Word | What it is |
|------|------------|
| **Task** (problem) | What contestants solve: name, title, statement, submission rules. |
| **Dataset** | How a task is judged: options, judging files and testcases. A task can have several; the one that judges submissions is the **live dataset**. Both windows show the live dataset unless you pick another. |
| **Testcase** | An input and its expected output, with a codename (`1`, `2`, `g07`...). A **public** testcase shows its result to the contestant. |
| **Subtask** | A group of testcases with their own points. |
| **Checker** | The program or rule that decides whether an output is right. |
| **Judging file** (manager) | An own checker, grader, stub, interactor, manager or header. |
| **Test submission** | A solution sent by an organizer: judged like a submission, it never counts. The **reference solution** is a correct test submission. |

More terms in the [glossary](glossary.md).

## 1. Create the task

- From a contest: **Problems → New task**; or from the **Task library**
  (without a contest, to add it later).
- **Name**: short and without spaces (`sum`, `paths`); used in addresses and
  in the name of the file contestants send.
- **Title**: the name people read ("Sum of two numbers").

Its **Configuration** window opens, with a live *Default* dataset: *Batch*
(standard input and output), 1 second, 256 MiB, one point per testcase.

On top of both windows, one line says **what is still missing**: a
statement, testcases, files the options ask for, a scoring, a reference
solution with the full score, a contest. Each item links to where it is
done; when nothing is missing it says *Ready*.

## 2. The Configuration window

### Import a package (zip)

**Import a package**: choose the zip and **Check the package**. A package
has `problem.yaml`, `statement/`, `tests/`, the checker, `graders/`,
`attachments/` and `solutions/` ([format](problem-package.md)); CMS
(`task.yaml`) and Polygon packages work too.

The preview shows what it carries (type, limits, subtasks, testcases,
statements, files, solutions) and any problem it has, **changing nothing**.
**Fill the task** replaces the task's options, statements, files, examples
and testcases with the package's. With *Run the reference solutions*
ticked, the solutions in `solutions/` are sent as test submissions and
their results appear in the Testcases window.

If the package names the task like another existing task, this one keeps
its own name (the preview says so).

### Options (problem.yaml)

One form with everything `problem.yaml` describes, in groups:

- **Problem**: name, title and **task type**: *Batch* (standard
  input/output or files, with or without a grader), *OutputOnly*,
  *TwoSteps*, *Communication*, *Interactive*. The chosen type shows its
  options.
- **Limits**: time (CPU seconds), wall time (empty: twice the time limit,
  or the limit plus 1 s), memory (MiB), output (MiB), processes, source
  size.
- **Checker** (Batch, OutputOnly, TwoSteps): *ignore whitespace* (the
  usual), *exact*, *reals with tolerance* (absolute and relative), or an
  **own checker** (CMS protocol or testlib) uploaded under Files.
- **Scoring and subtasks** (with the maximum score computed):
  - *Sum*: points per testcase (10 testcases × 10 points = 100);
  - *GroupMin*: each subtask is worth its points only if **all** its
    testcases are right (the IOI rule);
  - *GroupMul*: the subtask's points are multiplied by the worst outcome
    of its testcases (checkers with partial scores);
  - *GroupThreshold*: the subtask scores when each testcase passes a
    threshold.

  The subtask editor has one row per subtask: **points** and **which
  testcases** (names matching a regular expression such as `^g.*`, a list
  ticked by hand, or "the next N testcases"); the last, empty row adds one.
  Below it shows the total and which testcases are in no subtask. With
  GroupMin or GroupMul, **short-circuit** stops judging a subtask as soon
  as a testcase scores 0.
- **Submissions and results**: submission files (`sum.%l`: `%l` becomes the
  language's extension), primary statements, score mode (best per subtask,
  best submission, the last...), feedback, checker messages, precision and
  allowed languages.
- **Contest rules** (folded): tokens and the task's submission limits.

**Save** stores it all together. Submissions already judged keep their
result until you reevaluate them (the *Reevaluate* section at the bottom).

**Edit problem.yaml as text** (folded) shows the same options in the
package format; edit them there and **Save problem.yaml**. They are checked
like a package's: an error (an unknown key, a limit out of range, a subtask
that matches no testcase) changes nothing and is shown above the text.
`public_tests` says which testcases are public. **Download problem.yaml**
gets the file.

### Files

A table with every file of the task and what each one is: statements (with
**edit**, **PDF**, **delete**), judging files (checker, grader, stub,
interactor, manager, headers) and attachments for contestants. A file the
options ask for and that is missing (for instance `checker` with an own
checker) shows in yellow.

To upload, choose one or several files and **Upload**. *It is* says what
they are:

- **known by its name** (the usual): `checker.cpp`, `grader.cpp`,
  `stub.py`, `interactor.cpp`, `manager.c` and headers (`.h`) are judging
  files; `es.md`, `en.pdf`... are statements in that language; anything
  else is an attachment;
- **a statement** (with its language in *Language of a statement*), **a
  judging file** or **an attachment**, to decide yourself.

A file with the same name is replaced. **Write a statement** opens the
Markdown/LaTeX editor with a preview ([statements](statements.md)). If you
change a statement during the contest, contestants get a notice and the
page reloads by itself.

### Datasets (folded)

The task's datasets: which one is **live**, **compare with live**, **make
live**, **delete**; **rename** the one shown, **New dataset** (empty or a
copy) and **import a package as a new dataset**. See section 5.

## 3. The Testcases window

### Add testcases

**One testcase**:

1. **Codename**: the next number is proposed; you can type another one
   (letters, digits, `_`, `.`, `-`). An existing codename replaces that
   testcase.
2. **Input**: type it in the box or choose a file.
3. **Output**, one of three:
   - *typed or uploaded*;
   - *written by the reference solution*: the system runs a test
     submission (picked from the list; those with the full score first) on
     that input and keeps what it prints. *Batch* only;
   - *empty*: for interactive tasks or checkers that only read the input.
4. **Public**: the contestant sees this testcase's result.
5. **Add testcase**.

Typed text is stored with Unix line ends and a final newline.

**Generate with a program** makes many testcases at once:

1. A **generator**: a program (in any configured language) that reads **one
   line of parameters** on standard input and writes **a whole input** on
   standard output.
2. **Parameters, one testcase per line**: the generator runs once per
   non-empty line (up to 500).
3. **Codename prefix** and **first number**: prefix `g`, first number 8
   and 3 lines make `g08`, `g09`, `g10`.
4. **Output**: *written by the reference solution* or *empty*.

A Python example (`n` and a seed per line, e.g. `1000 7`):

```python
import random
n, seed = map(int, input().split())
random.seed(seed)
print(n)
print(*[random.randint(1, 10**9) for _ in range(n)])
```

A C++ example (one line with `n`):

```cpp
#include <cstdio>
#include <random>
int main() {
    long long n; std::scanf("%lld", &n);
    std::mt19937_64 rng(n);
    std::printf("%lld %lld\n", (long long)(rng() % 2000000001) - 1000000000,
                               (long long)(rng() % 2000000001) - 1000000000);
}
```

Everything runs on the **judges** (in the same sandbox as submissions),
never on the administration server. A generator gets at least 10 s, 1 GiB
of memory and 256 MiB of output; the reference solution runs with the
dataset's limits. Meanwhile the list shows each pending testcase ("the
generator is writing the input…", "the reference solution is writing the
output…") and refreshes by itself; if something fails the testcase shows as
*failed* with the reason, and **forget the failed ones** clears it.

**Import testcases from a zip**: upload a zip and say how inputs and
outputs are named (`*.in`/`*.out`, `input*.txt`/`output*.txt`...). Pairs
are matched by the part that replaces the `*`. *Overwrite existing*
replaces testcases with the same codename.

### The testcase list

A table with each testcase: number, **codename** (a link to its page, with
the input and output in view and **previous**/**next**), input and output
sizes (links to download them), **public** (click to change), **example**
(*use as example* adds it to the statement) and **delete**. **Delete all
testcases** empties the list (before importing another set, for instance).

On the right, one column for each of the **last four test submissions**,
with each testcase's verdict (AC, PA, WA, TLE, MLE, RE...) and time;
hovering shows the checker's message. A package's solutions show with
their names (`wa_resta.c`).

### Test submissions

Pick the language, upload the file and **Test**. The submission is judged
on every dataset of the task like any submission, but never appears in
rankings or statistics. The page returns to the list, which refreshes by
itself until the submission is judged. The test submission table shows the
score of each one (green full, yellow partial, red zero); the number opens
the whole detail.

Use them to check the task is right (the missing line asks for one with
the full score), to write the outputs of new testcases, and to see wrong or
slow solutions fail as expected. If you change the testcases, **send it
again**: its earlier result was computed with the testcases of then.

### Examples

At the bottom of the window: the examples every statement of the task
shows (on the page and in the PDF). Add them from a testcase (**use as
example**) or by typing them; each can carry a Markdown explanation, and
**up**/**down** order them.

## 4. Put it in the contest

If you created it from the contest it is already there. Otherwise, in the
contest: **Problems → Add an existing task**. The list's order gives the
letters (A, B, C...); the arrows change it. During the contest, **close
submissions** stops submissions to a single task.

## 5. Changing a task already used

Changing the options or testcases of the live dataset affects new
submissions; those already judged keep their result until you reevaluate
them. To prepare a change without touching the current results:

1. Under **Datasets**, **New dataset** copying the current one.
2. The **Dataset** selector on top of the windows shows the copy (a notice
   reminds you it is not live); change its options, files or testcases
   there. Tick *Judge new submissions on this dataset in the background* to
   compare.
3. **Compare with live** shows which scores would change.
4. **Make live** when you are sure: scores are recomputed.
