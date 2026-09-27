# Creating a problem

A problem is built **in parts, in any order and on different days**: the
name first, the statement another day, then the testcases one by one, the
reference solution, the subtasks... No zip or package is needed (though you
can use one, see [problem packages](problem-package.md)).

## Vocabulary

| Word | What it is |
|------|------------|
| **Task** (problem) | What contestants solve: name, title, statement, submission rules. |
| **Dataset** | How a task is judged: type, limits, testcases, checker, scoring. A task can have several; the one that judges submissions is the **live dataset**. |
| **Testcase** | An input and its expected output, with a codename (`1`, `2`, `g07`...). A **public** testcase shows its result to the contestant. |
| **Subtask** | A group of testcases with their own points. |
| **Checker** | The program or rule that decides whether an output is right. |
| **Manager** | A helper file of the dataset: own checker, grader, stub, interactor, headers. |
| **Reference solution** | An author's solution sent with the **task tester**: judged like a submission, it never counts. |

More terms in the [glossary](glossary.md).

## 1. Create the task

- From a contest: **Problems → New task**; or from the **Task library**
  (without a contest, to add it later).
- **Name**: short and without spaces (`sum`, `paths`); used in addresses and
  in the name of the file contestants send.
- **Title**: the name people read ("Sum of two numbers"). Empty means the
  name.

Creating it also creates its *Default* dataset (live): *Batch* (standard
input and output), 1 second, 256 MiB, one point per testcase.

## 2. The setup list

The task page starts with **Setup**: a numbered list of what a task needs,
each item linking to where it is done:

1. **Name and title**.
2. **Statement** (how many languages).
3. **Testcases** (how many the live dataset has).
4. **Type, limits and checker** (missing when the configuration needs a
   manager not uploaded yet, for example an own checker).
5. **Scoring and subtasks** (the maximum score).
6. **Reference solution with the full score**: at least one task tester
   run got the maximum on the live dataset.
7. **In a contest**.

Done items are green, missing ones yellow. Below, tabs lead to each part of
the page: Statements, Datasets and testcases, Examples, Attachments, Task
tester, Settings.

## 3. The statement

**Statements → Write a statement**: pick the language (`en`, `es`...),
write in **Markdown** or **LaTeX** and save; the preview is alongside.

- Formulas: `$a+b$` inline, `$$\sum_{i=1}^n a_i$$` on their own line.
- Usual sections: `## Input`, `## Output`, `## Subtasks`, `## Examples`
  (examples are inserted by themselves, see below).
- Contestants read the statement on the page and download it as a PDF; the
  PDF carries the limits and the examples.
- You can also **upload** a ready PDF, Markdown, LaTeX or HTML file.
- If you change a statement during the contest, contestants get a notice
  and the statement reloads by itself.

Details in [statements](statements.md).

## 4. Testcases

On the task page, **Datasets and testcases → Default** (or any dataset).
The dataset page has tabs: *Type, limits and checker*, *Scoring and
subtasks*, *Managers*, *Add testcases*, *Testcases*.

### One at a time

**Add testcases → One testcase**:

1. **Codename**: the next number is proposed; you can type another one
   (letters, digits, `_`, `.`, `-`). An existing codename replaces that
   testcase.
2. **Input**: type it in the box or choose a file.
3. **Output**, one of three:
   - *typed or uploaded*: type it or choose a file;
   - *written by the reference solution*: the system runs your solution (a
     task tester run, picked from the list) on that input and keeps what it
     prints. *Batch* tasks only;
   - *empty*: for interactive tasks or checkers that only read the input.
4. **Public**: the contestant sees this testcase's result one by one.
   Other testcases only count in the subtask blocks.
5. **Add testcase**.

Text typed in the boxes is stored with Unix line ends and a final newline.

### With a generator

**Add testcases → Generate with a program** makes many testcases at once:

1. A **generator**: a program (in any configured language) that reads **one
   line of parameters** on standard input and writes **a whole input** on
   standard output.
2. **Parameters, one testcase per line**: each non-empty line is a
   testcase. The generator runs once per line (up to 500 lines).
3. **Codename prefix** and **first number**: prefix `g`, first number 8 and
   3 lines make `g08`, `g09`, `g10`.
4. **Output**: *written by the reference solution* (the usual) or *empty*.

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

While they are made, the **Testcases** section lists each pending testcase
("the generator is writing the input…", "the reference solution is writing
the output…") and refreshes by itself. If something fails (the generator
does not compile, runs out of time, the solution crashes), the testcase is
shown as *failed* with the reason; **forget the failed ones** clears them.

Everything runs on the **judges** (in the same sandbox as submissions),
never on the administration server. A generator gets at least 10 s, 1 GiB
of memory and 256 MiB of output; the reference solution runs with the
dataset's limits.

### Many testcases from a zip

**Many testcases from a zip archive** (folded): upload a zip and say how
inputs and outputs are named (`*.in`/`*.out`, `input*.txt`/`output*.txt`...).
Pairs are matched by the part that replaces the `*`.

### In the testcase table

Each testcase shows whether it is public (click to change), links to
download its input and output, **use as example** (shown in the statement,
on the page and in the PDF) and **delete**.

## 5. Type, limits and checker

In the dataset's **Type, limits and checker** tab:

- **Time limit** (CPU seconds), **wall time limit** (empty: twice the time
  limit, or the limit plus 1 s), **memory** (MiB), **output** (MiB),
  **processes**, **source size**.
- **Task type**: *Batch* (standard input/output or files, with or without a
  grader), *Output only*, *Two steps*, *Communication*, *Interactive*. Each
  shows its options; what to upload and how they work is in
  [task types](task-types.md) and in the table of the
  [admin guide](admin-guide.md#each-problem-type-step-by-step).
- **Checker** (Batch, Output only, Two steps): *ignore whitespace* (the
  usual), *exact*, *reals with tolerance* (absolute and relative), or an
  **own checker** (CMS protocol or testlib) uploaded under **Managers**.
- **Managers**: upload the files the configuration asks for (the page says
  which are missing).

**Save**. Submissions already judged keep their result until you reevaluate
them (the button at the top of the dataset page).

## 6. Scoring and subtasks

In the **Scoring and subtasks** tab:

- *Sum*: points per testcase (e.g. 10 testcases × 10 points = 100).
- *GroupMin*: each subtask is worth its points only if **all** its
  testcases are right (the IOI rule).
- *GroupMul*: the subtask's points are multiplied by the worst outcome of
  its testcases (useful with checkers that give partial scores).
- *GroupThreshold*: the subtask scores when each testcase passes a
  threshold.

The subtask editor has one row per subtask: **points** and **which
testcases** (names matching a regular expression such as `^g.*`, a list
ticked by hand, or "the next N testcases"); the last, empty row adds a
subtask. Below it shows the total and which testcases are in no subtask.

With GroupMin or GroupMul, **short-circuit** stops judging a subtask as
soon as a testcase scores 0 (same score, less judging time; the skipped
testcases show as *skipped*).

What the contestant sees: **one block per subtask** with its points and
verdict (green, yellow or red) and the result of each **public testcase**.

## 7. The reference solution

**Task tester** (task page): pick the language, upload the file and
**Test**. It is judged on every dataset of the task like a submission, but
never appears in rankings or statistics. The table shows its result on each
dataset.

Use it to:

- check the task is right (the setup list asks for a run with the full
  score);
- write the testcases' outputs (section 4);
- try wrong or slow solutions and see them fail as expected.

If you change the testcases afterwards, **send it again**: its earlier
result was computed with the testcases of then.

## 8. Examples, attachments and submission rules

- **Examples**: shown in every statement of the task. Add them from a
  testcase (**use as example**) or by typing them; each can carry a
  Markdown explanation.
- **Attachments**: files contestants download (a sample grader, a header,
  data).
- **Settings** (at the bottom of the task page): submission files
  (`sum.%l`: `%l` becomes the language's extension), allowed languages,
  score mode (best per subtask, best submission, the last...), feedback,
  tokens, the task's submission limits, and whether contestants see the
  checker's own messages.

## 9. Put it in the contest

If you created it from the contest it is already there. Otherwise, in the
contest: **Problems → Add an existing task**. The list's order gives the
letters (A, B, C...); the arrows change it. During the contest, **close
submissions** stops submissions to a single task.

## 10. Changing a task already used

To change testcases or scores of a task that already has submissions
without touching the current results:

1. On the task page, **New dataset** copying the current one.
2. Change what is needed in the copy; tick *Judge new submissions on this
   dataset in the background* to compare.
3. **Compare with live** shows which scores would change.
4. **Make live** when you are sure: scores are recomputed.
