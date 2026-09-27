# How submissions are judged quickly and fairly

This page explains what the judge does to keep results fast during a
contest and how to check that every judging core measures time the same
way.

## Queues and priorities

Every job waits in one of five queues, served in this order:

1. **evaluate** — running the testcases of a live submission (so that a
   submission already compiled finishes first);
2. **compile** — compiling a live submission;
3. **deferred** — the remaining jobs of a submission whose contestant
   already sent a newer one to the same task (see below);
4. **user tests** — the *Testing* page;
5. **background** — reevaluations and datasets that are not live.

A worker always takes the first job of the highest non-empty queue. The
**Judges** page (*Workers and queues*) shows the length of each queue, the
jobs being run and the slots of every worker.

### Latest submissions first

When a contestant submits to a task while an earlier submission of theirs
to that task is still being judged, the older one is marked *superseded*.
Its jobs still waiting are moved, when a worker reaches them, to the
**deferred** queue: they are judged after everybody's latest submissions.
One contestant sending ten submissions in a row therefore cannot delay the
others, and every submission is still judged in full (the task score takes
the best of them, as usual).

## Queue position and waiting time

While a submission waits for a worker, the result card on the task page
says how many submissions are ahead of it and how long results take right
now (the median time from submission to score of the latest 200 judged
submissions). The card refreshes itself every 10 seconds while it waits
and stops once the submission is being judged.

## Short-circuit evaluation

In a *GroupMin* subtask (and *GroupMul*), one testcase scoring 0 makes the
whole subtask 0: the other testcases cannot change it. With **Short-circuit**
ticked on the dataset page (score section), the dispatcher marks those
testcases as *skipped* as soon as the zero arrives, and the workers do not
run the ones still waiting. The score is the same as with a full
evaluation; the time saved goes to the other contestants.

Skipped testcases appear in the details as *Skipped: another testcase of
the subtask failed*. A testcase that belongs to several subtasks is skipped
only when every one of them already has a zero. The option is off by
default; leave it off when contestants should see the result of every
testcase (for example when feedback is full and the contest wants it).

## Compilation cache

A successful compilation is remembered under a hash of everything it
depends on: the language and its commands, the task type and its
parameters, the submitted files and the graders or headers of the dataset.
An identical submission later (the same code submitted again, a
reevaluation, another dataset with the same graders) takes the executables
directly, without a compile job. Entries unused for a week are dropped by
the blob garbage collector.

A reevaluation with **recompile** clears the cache and really compiles:
use it after upgrading a compiler on the workers.

## Time limits per language

A language can scale the tasks' time limits with `time_multiplier` in its
file (none by default); see [languages](languages.md).

## Workers download the testcases ahead of time

Every minute the dispatcher publishes the testcases, checkers and graders
of the live datasets of the contests running now or starting within three
hours. Each worker downloads those missing from its cache, one at a time,
in the background (up to 80% of `worker.cache_max_bytes`), so the first
submissions of a contest do not wait for the files. The **Judges** page
shows each worker's progress (*cache 120/130*). If a worker's cache is too
small for a contest's testcases, raise `worker.cache_max_bytes`.

## Calibrating the judging machines

The same program must take the same time on every judging core, or a
contestant's verdict depends on which core ran it. Run on each worker
machine, with its worker stopped:

```sh
sudo systemctl stop cms-worker
sudo cms ctl calibrate -config /etc/cms/cms.yaml
sudo systemctl start cms-worker
```

It compiles a CPU-bound benchmark once and runs it several times (`-runs`,
5 by default) on every judging core through the sandbox, then prints the
median CPU time of each core, how far it is from the machine's median and
how much its runs varied. Cores more than 3% away (`-tolerance`) are
marked SLOW or FAST; a core whose runs vary a lot is marked *noisy*. The
usual causes are turbo boost, the CPU frequency governor and hyperthreads
sharing a physical core: `sudo cms-host-tuning enable` sets them up, or
remove the core from `worker.cores`.

The result is also stored (for 30 days) and shown on the **Judges** page,
where the machines are compared with each other: a machine more than the
tolerance away from the median of all machines is marked, and a warning
says how much slower the slowest machine is than the fastest. Use
`-publish=false` to only print the result, and `-box-offset` if the default
isolate boxes (500 onwards) overlap a running worker's.
