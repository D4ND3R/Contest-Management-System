# Example problem packages

One package per problem type, in the format of
[docs/en/problem-package.md](../../en/problem-package.md)
([español](../../es/paquete-de-problema.md)). Zip a folder and import it
from **Tasks → Import a package**; the reference solutions in `solutions/`
are judged and every one must behave as its name says. They are also the
fixtures of `internal/problempkg` and `internal/e2e` tests.

| folder | type | shows |
|--------|------|-------|
| `batch-suma` | batch | subtasks (group_min), public testcase, attachments, statements in two languages, ac/pa/wa/tle solutions |
| `interactive-adivina` | interactive | interactor, C and Python solutions |
| `output-only-cuadrados` | output_only | output files per testcase, merge of previous outputs, full and partial solutions as folders |
| `communication-suma` | communication | manager and stubs in graders/ |
| `two-steps-binario` | two_steps | manager compiled with the submission, a cheating solution |
