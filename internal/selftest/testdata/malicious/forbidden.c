/* Makes the system call named on standard input: each is refused by the
   seccomp filter, which kills the program (Security violation). Without
   the filter most of them only fail inside the sandbox; the filter removes
   the kernel code they reach from the attack surface. */
#define _GNU_SOURCE
#include <sched.h>
#include <stdio.h>
#include <string.h>
#include <unistd.h>
#include <sys/syscall.h>
#include <sys/ptrace.h>

int main(void) {
	char c[64] = "";
	if (scanf("%63s", c) != 1) return 2;
	if (!strcmp(c, "unshare")) unshare(CLONE_NEWUSER);
	if (!strcmp(c, "clone_newnet")) syscall(__NR_clone, CLONE_NEWNET | 17, 0, 0, 0, 0);
	if (!strcmp(c, "bpf")) syscall(__NR_bpf, 0, 0, 0);
	if (!strcmp(c, "io_uring")) syscall(__NR_io_uring_setup, 8, 0);
	if (!strcmp(c, "ptrace")) ptrace(PTRACE_TRACEME, 0, 0, 0);
	if (!strcmp(c, "keyctl")) syscall(__NR_keyctl, 0, 0, 0);
	if (!strcmp(c, "perf")) syscall(__NR_perf_event_open, 0, 0, -1, -1, 0);
	puts("5");
	return 0;
}
