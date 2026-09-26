# How the judge defends itself

A submission is untrusted code that runs on your server. The CMS stops
what it tries in layers, and tells the staff when a submission looks like
an attack.

## The sandbox

Every compilation and every run happens in an [isolate](https://github.com/ioi/isolate)
box, a fresh one each time:

- its own namespaces: no network (only a private loopback), its own
  process list, a private file system where the system directories are
  read-only and only the box is writable;
- an unprivileged user per box, control-group limits on CPU time, memory
  and processes (one process for most tasks), limits on file sizes, open
  files and the stack;
- a private `/dev/shm` and `/tmp`, removed after the run.

## The seccomp filter

On top of isolate, the worker runs every program through a small launcher
that installs a **seccomp filter** before executing it. The filter kills
the program the moment it makes a system call no contest program needs,
the ones that reach the parts of the kernel where most sandbox escapes
were found:

- new namespaces (`unshare`, `setns`, `clone` with namespace flags);
- `bpf`, `io_uring`, `userfaultfd`, `perf_event_open`;
- keyrings (`keyctl`, `add_key`), `ptrace`, other processes' memory;
- mounts, `chroot`, `pivot_root`, kernel modules, `kexec`, `reboot`,
  swap, clocks, `syslog`, raw I/O ports.

Threads keep working in every language (`clone3` answers "not
implemented", so the C library falls back to `clone`, which the filter
can inspect), and so do the compilers. The filter is inherited by every
process the program starts and cannot be removed.

A program killed by the filter gets the verdict **Security violation**
("the program made a forbidden system call"); a compilation stopped by it
fails with "Compilation stopped: forbidden system call".

The worker compiles the launcher with the system C compiler when it
starts (the installer installs `gcc` and `libc6-dev`). The setting
`worker.seccomp` (or `CMS_WORKER_SECCOMP`) chooses:

| Value | Meaning |
|---|---|
| `auto` (default) | use it; without a C compiler, run without it and warn |
| `on` | refuse to start without it |
| `off` | never use it |

**Judges** (admin) shows *seccomp* or *no seccomp* next to each worker,
the dashboard warns when a worker runs without it, and `cms ctl
judge-selftest` checks that forbidden calls end in a security violation
([verifying a host](verify-host.md)).

## Suspicious submissions

When a submission arrives, its source is scanned for code that attacks
the judge rather than solves the task:

| Flag | Examples |
|---|---|
| starts other programs | `fork`, `system`, `exec*`, `subprocess`, `ProcessBuilder`, `Command::new` |
| uses the network | sockets, `java.net`, `std::net`, `"net"` |
| raw system calls | `syscall(...)`, inline `asm` with `syscall`, `asm!` |
| reads system files | strings naming `/proc`, `/sys`, `/etc`, `/root`, `/home`, `/var` |
| includes a file from outside the submission | `#include </etc/...>`, `#include "../..."` |
| inspects other processes | `ptrace`, `process_vm_readv` |
| loads or generates native code | `dlopen`, `PROT_EXEC`, `ctypes`, `System.loadLibrary`, `DllImport` |
| binary data in a source file | NUL bytes, invalid UTF-8 |

and the sandbox adds **forbidden system call** when the filter killed the
program (during compilation or on a testcase).

A flag never changes a score: the submission is judged as usual, and the
sandbox has already stopped whatever it tried. Flags tell the staff where
to look:

- the submission page lists them with the file, line and code, or the
  testcase;
- the contest's **Submissions** show a *suspicious* tag, and the status
  filter has **flagged**;
- the contest dashboard's notifications count them.

If a submission deserves it, **invalidate** it with a reason (it stays
visible and no longer counts) and deal with the contestant.

## Uploads

- Contest banners are checked from their bytes: PNG, JPEG, GIF or WebP,
  never SVG (it can carry scripts).
- HTML statements are converted to the CMS's own model: scripts and
  anything active are dropped.
- Zip archives (output-only submissions, packages) are read with size
  limits per file, so a "zip bomb" is refused.
- Every page is served with a strict Content Security Policy (no inline
  scripts), CSRF tokens on every form, and `nosniff`.
