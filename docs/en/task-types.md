# Task types and checkers

Dataset `task_type` and `task_type_params` (JSON) select how submissions are
compiled and evaluated.

## Batch
The program reads a testcase and writes the answer.

| param | default | meaning |
|---|---|---|
| `compilation` | `alone` | `grader`: compile the manager `grader.<ext>` (plus headers) with the submission |
| `input_file` / `output_file` | stdin / stdout | file names for file-based I/O |
| `checker` | `white_diff` | `exact`, `white_diff`, `float`, `custom`, `testlib` |
| `float_abs_tol`, `float_rel_tol` | 0 | tolerances of the `float` comparator |
| `checker_time_limit_ms`, `checker_memory_bytes` | 10 s, 1 GiB | custom checker limits |

Graders are provided per language as managers (`grader.c` + `task.h`,
`grader.cpp`, `grader.java`, `grader.py`, …); the contestant implements the
functions. Graders work with standard I/O or with `input_file` /
`output_file`.

## OutputOnly
Contestants submit `output_<codename>.txt` for each testcase
(`output_pattern` changes the name), one by one or all together in a zip
archive (every file in the archive must be one of the expected names).
Partial submissions are allowed: missing outputs score zero, unless
`merge_previous` is enabled, in which case each missing output is taken
from the contestant's previous submission that scored best on that
testcase. When the task's submission format is empty, the expected names
are derived from the live dataset's testcases. Same checkers as Batch.

## TwoSteps
The submission is compiled with the manager `manager.<ext>` into one
executable, run twice: `argv[1]="0"` reads the testcase and writes a
message; `argv[1]="1"` reads only the message (in another sandbox) and
writes the answer, which is checked.

## Communication
A manager (`manager` binary or `manager.cpp`, run sandboxed) talks to
`num_processes` contestant processes (each in its own sandbox) through a
pair of FIFOs per process. Manager: `argv = u2m_0 m2u_0 [u2m_1 m2u_1 ...]`,
stdin = testcase input, stdout = score in [0,1], stderr = message.
Contestant process: `argv = m2u u2m [index]` (`user_io: fifos`) or
stdin/stdout on the FIFOs (`user_io: std_io`). `compilation: stub` compiles
the manager-provided `stub.<ext>` with the submission.

Limits: every contestant process gets the task's time and memory limits
(`limits_mode: per_process`, default); with `limits_mode: total` the sum of
the processes' CPU time and of their peak memory must also fit. Stubs exist
per language (`stub.c`, `stub.cpp`, `stub.py`, `stub.java`, …).
`manager_time_limit_ms` / `manager_memory_bytes` bound the manager.

## Interactive
ICPC / Codeforces style: the manager `interactor` (binary, `.c` or `.cpp`,
testlib allowed with `testlib.h` as another manager) talks to ONE
contestant process. The interactor's standard output is the contestant's
standard input and vice versa (anonymous pipes between two sandboxes).
It runs as `interactor <input> <output> <answer>` in its own sandbox with
its own limits (`interactor_time_limit_ms`, default 10 s + TL;
`interactor_memory_bytes`, default 1 GiB); its CPU time is never charged
to the contestant. It decides the verdict with testlib exit codes (0
accepted, 1/2/4 wrong answer, 3 judge failure, 7 / 16+n partial) and the
first line of its stderr is the message.

Verdicts: a contestant that exceeds a limit gets that verdict (a program
that does not flush blocks both sides and ends on the wall-clock limit:
TLE); a crash is a runtime error; otherwise the interactor decides —
including when the contestant ends too early (the interactor sees
end-of-file) or was killed by SIGPIPE because the interactor had already
quit. An interactor that crashes or reports a failure is a system error.

## Checkers
- `white_diff` (default): line-by-line, ignoring the amount of whitespace
  between tokens and trailing blank lines (CMS semantics).
- `exact`: byte-for-byte.
- `float`: token-by-token; numbers may differ by `float_abs_tol` or
  `float_rel_tol × |expected|`.
- `custom` (CMS protocol): manager `checker` (binary), `checker.cpp` or `checker.c`
  (compiled once per worker), run as `checker input correct contestant`;
  stdout's first line is the score in [0,1], stderr's first line the message
  (`translate:success|wrong|partial` are standard messages).
- `testlib`: same executable conventions, run as
  `checker input contestant correct`; exit code 0 = accepted, 1/2/4 = wrong,
  3 = checker failure (system error), 7 = partial (`points X` in the
  message: a fraction when ≤ 1, a percentage otherwise), 16+n = n percent.
