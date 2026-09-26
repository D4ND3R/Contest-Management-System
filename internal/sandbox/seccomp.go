package sandbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Seccomp (SPEC_IOI H3): isolate confines a program with namespaces,
// cgroups and resource limits; a seccomp filter is the second wall. A tiny
// launcher, compiled by the worker with the system C compiler, installs a
// filter that kills the process (SIGSYS, reported as StatusSecurity) on
// the system calls a contest program never needs — namespaces, BPF,
// io_uring, keyrings, ptrace, mounts, modules, clocks — and then executes
// the program. Everything else, threads included, keeps working for every
// language; clone3 answers ENOSYS so libc falls back to the clone it can
// inspect. The filter is inherited by every process the program starts.

// launcherSource is the C source of the launcher (see D86).
const launcherSource = `/* cms-seccomp: installs a seccomp filter that kills the process on the
   system calls a contest program never needs (kernel attack surface:
   namespaces, BPF, io_uring, keyrings, ptrace, mounts, modules...), then
   executes the program. Written for the CMS; built by the worker. */
#define _GNU_SOURCE
#include <errno.h>
#include <stddef.h>
#include <stdio.h>
#include <string.h>
#include <unistd.h>
#include <sys/prctl.h>
#include <sys/syscall.h>
#include <linux/audit.h>
#include <linux/filter.h>
#include <linux/seccomp.h>

#ifndef SECCOMP_RET_KILL_PROCESS
#define SECCOMP_RET_KILL_PROCESS 0x80000000U
#endif

#if defined(__x86_64__)
#define ARCH AUDIT_ARCH_X86_64
#elif defined(__aarch64__)
#define ARCH AUDIT_ARCH_AARCH64
#else
#error "unsupported architecture"
#endif

#define LOAD(off) BPF_STMT(BPF_LD | BPF_W | BPF_ABS, (off))
#define KILL BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_KILL_PROCESS)
#define ALLOW BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ALLOW)
#define DENY(nr) BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, (nr), 0, 1), KILL

/* Namespace flags of clone(2) (the low word of its first argument). */
#define NS_FLAGS 0x7E020000U

int main(int argc, char **argv) {
	struct sock_filter f[] = {
		LOAD(offsetof(struct seccomp_data, arch)),
		BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, ARCH, 1, 0),
		KILL,
		LOAD(offsetof(struct seccomp_data, nr)),
#if defined(__x86_64__)
		/* The x32 ABI would bypass the numbers below. */
		BPF_JUMP(BPF_JMP | BPF_JGE | BPF_K, 0x40000000U, 0, 1),
		KILL,
#endif
		/* clone3 cannot be inspected: glibc falls back to clone. */
#ifdef __NR_clone3
		BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, __NR_clone3, 0, 1),
		BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ERRNO | ENOSYS),
#endif
		/* clone: threads and processes yes, new namespaces no. */
		BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, __NR_clone, 0, 4),
		LOAD(offsetof(struct seccomp_data, args[0])),
		BPF_JUMP(BPF_JMP | BPF_JSET | BPF_K, NS_FLAGS, 0, 1),
		KILL,
		ALLOW,
		DENY(__NR_unshare), DENY(__NR_setns),
		DENY(__NR_mount), DENY(__NR_umount2), DENY(__NR_pivot_root), DENY(__NR_chroot),
#ifdef __NR_open_tree
		DENY(__NR_open_tree), DENY(__NR_move_mount), DENY(__NR_fsopen), DENY(__NR_fsconfig),
		DENY(__NR_fsmount), DENY(__NR_fspick),
#endif
#ifdef __NR_mount_setattr
		DENY(__NR_mount_setattr),
#endif
		DENY(__NR_bpf), DENY(__NR_perf_event_open), DENY(__NR_userfaultfd),
#ifdef __NR_io_uring_setup
		DENY(__NR_io_uring_setup), DENY(__NR_io_uring_enter), DENY(__NR_io_uring_register),
#endif
		DENY(__NR_keyctl), DENY(__NR_add_key), DENY(__NR_request_key),
		DENY(__NR_ptrace), DENY(__NR_process_vm_readv), DENY(__NR_process_vm_writev), DENY(__NR_kcmp),
#ifdef __NR_pidfd_getfd
		DENY(__NR_pidfd_getfd),
#endif
		DENY(__NR_init_module), DENY(__NR_finit_module), DENY(__NR_delete_module),
		DENY(__NR_kexec_load),
#ifdef __NR_kexec_file_load
		DENY(__NR_kexec_file_load),
#endif
		DENY(__NR_reboot), DENY(__NR_swapon), DENY(__NR_swapoff), DENY(__NR_acct),
		DENY(__NR_settimeofday), DENY(__NR_clock_settime), DENY(__NR_clock_adjtime), DENY(__NR_adjtimex),
		DENY(__NR_syslog), DENY(__NR_vhangup), DENY(__NR_quotactl),
		DENY(__NR_name_to_handle_at), DENY(__NR_open_by_handle_at),
#if defined(__x86_64__)
		DENY(__NR_iopl), DENY(__NR_ioperm), DENY(__NR_uselib), DENY(__NR_modify_ldt),
#endif
		ALLOW,
	};
	struct sock_fprog prog = { .len = (unsigned short)(sizeof(f) / sizeof(f[0])), .filter = f };
	if (argc < 2) {
		fputs("usage: cms-seccomp PROGRAM [ARGS...]\n", stderr);
		return 125;
	}
	if (prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0) != 0 ||
	    prctl(PR_SET_SECCOMP, SECCOMP_MODE_FILTER, &prog, 0, 0) != 0) {
		fprintf(stderr, "cms-seccomp: cannot install the filter: %s\n", strerror(errno));
		return 125;
	}
	execvp(argv[1], argv + 1);
	fprintf(stderr, "cms-seccomp: cannot execute %s: %s\n", argv[1], strerror(errno));
	return 127;
}
`

// LauncherDir is where boxes see the launcher's directory.
const LauncherDir = "/cms-launcher"

// BuildLauncher compiles the launcher into dir (once per source version)
// with cc and checks that it runs; it returns the launcher's path.
func BuildLauncher(ctx context.Context, dir, cc string) (string, error) {
	sum := sha256.Sum256([]byte(launcherSource))
	path := filepath.Join(dir, "cms-seccomp-"+hex.EncodeToString(sum[:6]))
	if st, err := os.Stat(path); err == nil && st.Mode()&0o111 != 0 {
		return path, checkLauncher(ctx, path)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if cc == "" {
		cc = "cc"
	}
	src := filepath.Join(dir, "cms-seccomp.c")
	if err := os.WriteFile(src, []byte(launcherSource), 0o644); err != nil {
		return "", err
	}
	defer os.Remove(src)
	tmp := path + ".tmp"
	var out bytes.Buffer
	// Static first (no loader, fastest start); dynamic if the static C
	// library is missing (the boxes see /lib).
	for _, static := range []bool{true, false} {
		args := []string{"-O2", "-o", tmp, src}
		if static {
			args = append([]string{"-static"}, args...)
		}
		cctx, cancel := context.WithTimeout(ctx, time.Minute)
		cmd := exec.CommandContext(cctx, cc, args...)
		out.Reset()
		cmd.Stdout, cmd.Stderr = &out, &out
		err := cmd.Run()
		cancel()
		if err == nil {
			if err := os.Chmod(tmp, 0o755); err != nil {
				return "", err
			}
			if err := checkLauncher(ctx, tmp); err != nil {
				os.Remove(tmp)
				return "", err
			}
			return path, os.Rename(tmp, path)
		}
	}
	os.Remove(tmp)
	return "", fmt.Errorf("compile the seccomp launcher with %s: %s", cc, bytes.TrimSpace(out.Bytes()))
}

// ErrNoSeccomp means the launcher could not install its filter.
var ErrNoSeccomp = errors.New("sandbox: seccomp filter unavailable")

// checkLauncher runs /bin/true through the launcher.
func checkLauncher(ctx context.Context, path string) error {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, path, "/bin/true").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %v: %s", ErrNoSeccomp, err, bytes.TrimSpace(out))
	}
	return nil
}
