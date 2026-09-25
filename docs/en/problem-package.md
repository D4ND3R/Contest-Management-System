# Problem packages

A problem package is a zip with everything a task needs. The admin panel
imports it (**Tasks → Import a package**), shows what it found and every
problem per file before creating anything, runs the reference solutions and
reports whether each gets the verdict its name announces. Any task can be
exported back in the same format (**Export as a package** on the task or
dataset page), so a task moves between installations unchanged.

Complete examples, one per problem type, are in
[`docs/examples/packages/`](../examples/packages/): zip one of the folders
and import it.

## Layout

```
problem.yaml             configuration (required)
statement/es.pdf         one statement per language: <lang>.pdf|.html|.md|.txt
statement/en.html
tests/01.in              testcases: <name>.in + <name>.out (or .ans)
tests/01.out
checker.cpp              optional: checker, interactor, manager
interactor.cpp             (binary, .c or .cpp; testlib.h may sit next to them)
manager.cpp
graders/grader.cpp       graders, stubs and headers, per language
graders/stub.py
attachments/sample.zip   files contestants can download
solutions/ac_main.cpp    reference solutions: <expected verdict>_<name>.<ext>
solutions/tle_brute.py
```

The files may also sit inside one folder (`suma/problem.yaml`, …), as when
a folder is zipped. Files added by archivers (`__MACOSX`, `.DS_Store`) are
ignored; any other unknown file is reported as a warning and ignored.

Testcase names may contain letters, digits, `_`, `.` and `-`; they are
sorted by name (subtasks by count follow this order).

## problem.yaml

Unknown keys are errors, so typos never go unnoticed. Times are in seconds,
sizes in MiB (source size in KiB).

| key | default | meaning |
|-----|---------|---------|
| `format` | 1 | format version |
| `name` | — | task name, used in URLs (letters, digits, `_ . -`) |
| `title` | name | shown to contestants |
| `type` | `batch` | `batch`, `output_only`, `interactive`, `communication`, `two_steps` |
| `time_limit` | — | CPU seconds (required except for `output_only`) |
| `memory_limit` | — | MiB (required except for `output_only`) |
| `wall_time_limit` | max(2×TL, TL+1 s) | wall-clock seconds |
| `output_limit` | 64 | MiB |
| `process_limit` | 1 | processes/threads |
| `source_size_limit` | none | KiB |
| `languages` | the contest's | allowed languages (ids of `config/languages/`) |
| `submission_format` | `<name>.%l` | submitted files; `%l` = language extension |
| `primary_statements` | none | statement languages marked as official |
| `feedback` | `full` | `full` or `restricted` (first failure per subtask) |
| `score_mode` | `max_subtask` | `max_subtask`, `max`, `max_tokened_last` |
| `score_precision` | 0 | decimals |
| `dataset` | `Default` | dataset description |

Per type:

| key | types | meaning |
|-----|-------|---------|
| `input_file`, `output_file` | batch | file I/O instead of stdin/stdout |
| `compilation` | batch, communication | batch: `alone` or `grader` (graders/grader.<ext>); communication: `stub` (default) or `alone` |
| `checker` | batch, output_only, two_steps | `white_diff` (default), `exact`, `float`, `custom` (CMS protocol), `testlib` |
| `float_tolerance` | same | `{absolute: 1e-6, relative: 1e-6}` |
| `checker_time_limit`, `checker_memory_limit` | same | custom checker limits (10 s, 1024 MiB) |
| `output_pattern`, `merge_previous` | output_only | file names (`output_%s.txt`), keep the best previous output of missing files |
| `manager` | two_steps | manager source name (`manager`) |
| `processes`, `user_io`, `limits_mode` | communication | 1–4 processes; `fifos` or `std_io`; `per_process` or `total` |
| `manager_time_limit`, `manager_memory_limit` | communication | manager limits |
| `interactor_time_limit`, `interactor_memory_limit` | interactive | interactor limits (never charged to the contestant) |

Scoring:

```yaml
scoring: sum             # every testcase is worth points_per_test
points_per_test: 10      # default: 100 split evenly among the testcases

scoring: group_min       # group_min, group_mul or group_threshold
subtasks:
  - points: 30
    tests: "1_.*"        # a regex over testcase names,
  - points: 30
    tests: [2_01, 2_07]  # a list of names,
  - points: 40
    tests: 5             # or the next N testcases in name order
    threshold: 0.5       # group_threshold only
public_tests: ["1_01"]   # regexes of testcases whose results contestants see
```

A testcase in no subtask is reported as a warning (it never counts).

## Checkers, interactors and managers

- `checker` (binary, `checker.c` or `checker.cpp`) is used with
  `checker: custom` (CMS protocol: `checker input correct_output
  contestant_output`, score in [0, 1] on stdout, message on stderr) or
  `checker: testlib` (testlib argument order and exit codes).
- `interactor` (interactive tasks) runs as `interactor input output answer`,
  talks to the contestant through its standard input/output and decides the
  verdict with testlib exit codes.
- `manager` (communication tasks) talks to the contestant's processes
  through FIFOs and writes the score on stdout; `manager.<ext>` (two-step
  tasks) is compiled with the submission.
- Graders, stubs and headers go in `graders/` (`grader.cpp`, `stub.py`,
  `task.h`, …); `testlib.h` may go at the root or in `graders/`.

Missing files are reported before importing (for example `checker: testlib`
without a checker).

## Reference solutions and validation

Every file in `solutions/` starts with the verdict it must get:

| prefix | the solution must |
|--------|-------------------|
| `ac_` | get the full score |
| `pa_` | get more than 0 and less than the maximum |
| `wa_`, `tle_`, `mle_`, `re_` | not get the full score, with a wrong answer / time limit / memory limit / runtime error on some testcase |
| `ce_` | fail to compile |
| `any_` | nothing (it is run and shown) |

The language comes from the extension: the first language of the task (or,
without `languages`, of the configuration) that uses it. For output-only
tasks a solution is a folder (`solutions/ac_all/output_01.txt`, …) or a zip.

After importing, the solutions run through the task tester on every dataset
(they are never submissions) and **Validation report** shows, per solution
and dataset, ✓ or ✗ with what happened (`ac`, `pa 30/100 wa`, `tle`, …).
A checker, interactor or manager that does not compile appears as a system
error. The task is outside every contest unless one was chosen, and a
package imported as a new dataset is not live: publish once the report is
all ✓. **Run them again** re-judges the solutions (e.g. after changing
limits).

## Import options

- **A new task**, optionally at the end of a contest. A task with the same
  name is reported; import the package as a dataset of it instead, or
  rename it.
- **A new dataset (not live) of an existing task**: limits, type, scoring,
  testcases and managers; the task's statements, attachments and settings
  are kept.

Nothing is written until the preview is confirmed; the import is one
transaction.

## Packages from other systems

Two other formats are converted on import, from the admin panel and from
`cmsctl task-import` alike; the preview says **converted from the … format**
and lists what could not be converted as warnings. Examples are in
[`docs/examples/other-formats/`](../examples/other-formats/).

**CMS italy_yaml** (a folder with `task.yaml`):

| italy_yaml | becomes |
|------------|---------|
| `task.yaml`: `name`, `title`, `time_limit` (s), `memory_limit` (MiB), `infile`/`outfile` (default `input.txt`/`output.txt`; empty = standard input/output), `output_only`, `public_testcases` (`all` or a list of indexes), `n_input` | the same settings |
| `input/inputN.txt`, `output/outputN.txt` | testcases `000`, `001`, … |
| `gen/GEN` lines `# ST: points` (or `score_type_parameters` pairs) | subtasks (GroupMin; GroupMul when `score_type` says so) |
| no subtasks | Sum with `total_value` / `n_input` points per testcase (100 in total by default) |
| `check/checker` or `cor/correttore` (binary or `.c`/`.cpp`) | custom checker (the CMS protocol: outcome on stdout, message on stderr) |
| `check/manager` | Communication task with that manager |
| `sol/grader.*`, `sol/stub.*`, headers | graders / stubs per language |
| `sol/soluzione.*` (or `solution`, `sol`) | reference solution expected to be accepted; other sources in `sol/` run without an expected verdict |
| `statement/statement.pdf` or `testo/testo.pdf` | statement in `primary_language` (Italian by default) |
| `att/*` | attachments |

**Polygon** (a full package with `problem.xml`, downloaded with the
generated tests, or after running `doall.sh`):

| Polygon | becomes |
|---------|---------|
| testset `tests`: time limit (ms), memory limit (bytes), input/output file | the same settings, in seconds and MiB |
| `tests/01`, `tests/01.a`, … | testcases `01`, `02`, …; tests marked as samples are public |
| test groups with points | subtasks (GroupMin) with the group's tests |
| points per test without groups | points per testcase (one subtask per test when they differ) |
| checker source (testlib) | `testlib` checker; the `.h` resources (`testlib.h`) go with it |
| interactor | Interactive task |
| solutions tagged `main`/`accepted`, `wrong-answer`, `time-limit-exceeded`, `memory-limit-exceeded`, … | reference solutions with the matching expected verdict (the rest run without one) |
| statements | one per language, the PDF when there is one (HTML statements come without their images) |

A package without its generated tests is rejected with a message saying
so: download the **full** package from Polygon.

## Export

The export writes `problem.yaml` from the task and the chosen dataset (the
live one by default), the statements, testcases, managers (checker,
interactor and manager at the root, the rest in `graders/`), attachments and
the newest version of every reference solution imported with a package.
Importing it again gives an identical task.
