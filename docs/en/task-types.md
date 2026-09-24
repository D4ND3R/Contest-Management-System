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

## OutputOnly
Contestants submit `output_<codename>.txt` for each testcase
(`output_pattern` changes the name). Same checkers as Batch.

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

## Checkers
- `white_diff` (default): line-by-line, ignoring the amount of whitespace
  between tokens and trailing blank lines (CMS semantics).
- `exact`: byte-for-byte.
- `float`: token-by-token; numbers may differ by `float_abs_tol` or
  `float_rel_tol × |expected|`.
- `custom` (CMS protocol): manager `checker` (binary) or `checker.cpp`
  (compiled once per worker), run as `checker input correct contestant`;
  stdout's first line is the score in [0,1], stderr's first line the message
  (`translate:success|wrong|partial` are standard messages).
- `testlib`: same executable conventions, run as
  `checker input contestant correct`; exit code 0 = accepted, 1/2/4 = wrong,
  3 = checker failure (system error), 7 = partial (`points X` in the
  message: a fraction when ≤ 1, a percentage otherwise), 16+n = n percent.
