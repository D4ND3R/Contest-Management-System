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
