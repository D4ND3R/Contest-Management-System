# Drill: a rehearsal contest

Run this rehearsal on the real server a few days before the contest, with
the staff who will run it. It takes about 45 minutes and exercises what a
contest needs: three problem types, questions, an announcement, an
invalidated submission, the frozen ranking and the final results. The same
steps are automated in `internal/e2e/drill_test.go` (`TestDrill`, part of
`make test`), so they are known to work; the drill checks your server,
your network and your team.

Tick every box; anything that does not behave as described is a finding to
fix before the contest.

## Preparation (10 minutes)

- [ ] `sudo cms-verify-host` passes ([verify host](verify-host.md)).
- [ ] **Contests → New contest** `drill`: starts now, lasts 30 minutes,
      languages C, C++ and Python, ranking visibility *contestants*, freeze
      the last 10 minutes.
- [ ] **Tasks → Import a package**, three times, into `drill` (zip each
      folder of [`docs/examples/packages/`](../examples/packages/)):
      `batch-suma` (normal input/output), `interactive-adivina`
      (interactive) and `output-only-cuadrados` (output only). Each
      validation report says *Every solution behaves as expected*.
- [ ] **Users → Import CSV** two contestants, `ana` and `beto`, added to
      `drill`; **Participations → Generate and print** their credentials.
- [ ] Two people log in as `ana` and `beto` from the contest network
      (`https://<domain>/drill/`), on the computers contestants will use.

## During the contest (20 minutes)

- [ ] `ana` submits a correct solution to each problem (for `suma` and
      `adivina` one from the package's `solutions/ac_*`; for `cuadrados` the
      four `tests/*.out` as `output_01.txt` … `output_04.txt`): each scores
      100 within seconds, visible on her task pages without reloading.
- [ ] `beto` submits the wrong solutions (`solutions/wa_*`) and only
      `output_01.txt`: 0 points for `suma`, 25 for `adivina` (`wa_uno`
      always answers 1, right on one testcase) and 25 for `cuadrados`; the
      submission pages show the per-testcase results the task's feedback
      allows.
- [ ] `beto` asks a question about `cuadrados`; the staff sees it in
      **Questions** (the menu counter lights up) and answers it publicly:
      both contestants are notified and read the answer.
- [ ] The staff publishes an announcement from the contest's
      **Communication** page: both contestants are notified.
- [ ] The staff invalidates `ana`'s `suma` submission with a reason (the
      submission's page → *Invalidate*): her score drops by 100 and she
      sees the reason on her submission.
- [ ] **Workers & queues** shows no waiting jobs and no system errors.
- [ ] In the last 10 minutes `ana` submits again: the contestants' ranking
      (`/drill/ranking`) says it is frozen and does not show it; the admin
      ranking does.

## After the contest (15 minutes)

- [ ] The contest ends on time for both contestants; late submissions are
      refused.
- [ ] **Ranking → unfreeze**: the contestants' ranking shows the final
      scores.
- [ ] **Ranking → CSV** and **Printable PDF**: `ana` first (200 plus
      whatever she resubmitted), `beto` second (50).
- [ ] **Backups → Back up now**, download the file; restore it into a
      scratch database ([backups](backups.md#drill)).
- [ ] Contest page → **Archive** with submissions; delete the drill contest
      afterwards if you do not want to keep it.

Write down how long each step took and every question the staff had: that
is the list to settle before the real contest.
