# Verifying a judging host

**Do not start a contest on a machine where `scripts/verify-host.sh`
reports `RESULT: FAIL`.** Run it on every machine with a worker after
installing, after every kernel, isolate or CMS upgrade, and on the day of
the contest before opening the doors.

```sh
sudo systemctl stop cms-worker        # its jobs would disturb the timings
sudo scripts/verify-host.sh --config /etc/cms/cms.yaml
sudo systemctl start cms-worker
```

Options: `--languages c11,cpp17,python3` (judge only the contest's
languages; default: every language with a toolchain), `--runs N` (default
2), `--cms PATH` (the cms binary), `--isolate PATH`, `--skip-judge` (only
the environment checks), `--box` / `--box-offset` (isolate boxes to use;
they must not belong to a running worker).

## What it checks

| Check | FAIL when | Fix it prints |
|-------|-----------|---------------|
| privileges | not root | run with sudo |
| kernel | older than 5.4 | a current distribution |
| isolate | missing, not setuid root, no configuration, unsafe `box_root` | `scripts/install-isolate.sh`, `chown`/`chmod` |
| control groups | cgroup v1 with isolate 2, missing controllers (cpuset, memory, pids), `isolate.service` stopped | kernel parameter, `systemctl enable --now isolate.service` |
| sandbox | `isolate --cg --init` / `--run` fails | the isolate error |
| CPUs, SMT, turbo, governor, swap, NTP, ASLR, huge pages | never (warnings) | how to make times stable |
| judge self-test | any verdict unexpected, any host check failing, any verdict different between runs | see below |

Warnings do not block the contest but make running times less stable
(hyperthreading, turbo boost, frequency scaling, swap, address space
randomisation, transparent huge pages); on a dedicated judging machine fix
them all. On a VPS most of them are not visible from inside the VM.

`sudo cms-host-tuning enable` (or the installer's `--tune-host`) sets the
performance governor, turns turbo boost and transparent huge pages off, now
and at every boot; `sudo cms-host-tuning status` shows the state. Address
space randomisation and SMT are changed only when set to `off` in
`/etc/cms/host-tuning.conf`: turning ASLR off weakens every program on the
machine, so do it only on a dedicated judging machine. These settings also
affect any other service on the machine.

**Hyperthreading.** Two hyperthreads of one physical core share its
execution units: a program on one slows down whatever runs on the other.
Since 0.3 the installer judges on one CPU per physical core and leaves the
siblings idle (it prints them: "idle hyperthreads: 5 6 7"), and an existing
`cms.yaml` still on the old layout is moved to it. The self-test prints a
`WARNING` when two configured judging CPUs are siblings.
`--judge-all-threads` judges on every hyperthread (more throughput, less
stable times).

## The judge self-test

`cms ctl judge-selftest` (also usable alone) judges, through the same code
as the workers and on the configured judging cores:

- the **security battery**: fork bombs (1 and 64 allowed processes), reading
  host files, network access, writing outside the box, sleeping forever,
  memory and output hogs, huge stderr, `#include </dev/random>` and
  `</dev/zero>` at compile time, threads with and without allowance,
  `kill(-1)`, stack overflow, privilege escalation. Besides the verdict it
  checks the host: no process of the sandbox users survives, no file
  appears outside the box, a listener on the host receives no connection;
- the **sample solutions** (AC, WA, TLE, MLE, RE, CE) in every language
  whose toolchain is installed.

Everything is judged `--runs` times (2 by default) and every verdict must
be the same in every run (both time limits count as TLE). A security case
failing means the sandbox is not safe; a verdict changing between runs
means the timings are not stable enough to judge fairly.

A security case passes on any outcome that shows the sandbox held, and
runs are compared on that. The fork bombs, for example, may be stopped by
the time limit or by the memory limit: every fork the process limit refuses
still allocates the child's kernel structures, charged to the box and freed
only after an RCU grace period, so with 64 processes forking in a loop they
can reach the memory limit first. Either way the whole box is killed and no
process survives, which is checked.

The installer pauses `cms-worker` while it runs the verification, so that
queued jobs do not disturb the timings.
