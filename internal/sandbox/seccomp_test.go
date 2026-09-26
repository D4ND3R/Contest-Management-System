package sandbox

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

// forbidden is a C program making the system call named by its argument.
const forbidden = `#define _GNU_SOURCE
#include <sched.h>
#include <stdio.h>
#include <string.h>
#include <pthread.h>
#include <unistd.h>
#include <sys/syscall.h>
#include <sys/ptrace.h>
static void *f(void *a) { return a; }
int main(int argc, char **argv) {
	const char *c = argv[1];
	if (!strcmp(c, "unshare")) unshare(CLONE_NEWUSER);
	if (!strcmp(c, "clone_newnet")) syscall(__NR_clone, CLONE_NEWNET | 17, 0, 0, 0, 0);
	if (!strcmp(c, "bpf")) syscall(__NR_bpf, 0, 0, 0);
	if (!strcmp(c, "io_uring")) syscall(__NR_io_uring_setup, 8, 0);
	if (!strcmp(c, "ptrace")) ptrace(PTRACE_TRACEME, 0, 0, 0);
	if (!strcmp(c, "keyctl")) syscall(__NR_keyctl, 0, 0, 0);
	if (!strcmp(c, "mount")) syscall(__NR_mount, "none", "/tmp", "tmpfs", 0, 0);
	if (!strcmp(c, "thread")) { pthread_t t; if (pthread_create(&t, 0, f, 0)) return 3; pthread_join(t, 0); }
	puts("alive");
	return 0;
}
`

// TestSeccompLauncher builds the launcher with the system compiler and
// checks it outside isolate: forbidden calls end with SIGSYS, threads and
// ordinary programs run, and a second build reuses the first.
func TestSeccompLauncher(t *testing.T) {
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		t.Skip("seccomp launcher: linux/amd64 and linux/arm64 only")
	}
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("no C compiler")
	}
	dir := t.TempDir()
	ctx := context.Background()
	l, err := BuildLauncher(ctx, filepath.Join(dir, "launcher"), cc)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := BuildLauncher(ctx, filepath.Join(dir, "launcher"), cc); err != nil || again != l {
		t.Fatalf("rebuild: %s %v", again, err)
	}
	src := filepath.Join(dir, "p.c")
	os.WriteFile(src, []byte(forbidden), 0o644)
	prog := filepath.Join(dir, "p")
	if out, err := exec.Command(cc, "-O2", "-pthread", "-o", prog, src).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v %s", err, out)
	}
	run := func(arg string) (syscall.WaitStatus, string) {
		out, err := exec.Command(l, prog, arg).Output()
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.Sys().(syscall.WaitStatus), string(out)
		}
		if err != nil {
			t.Fatal(err)
		}
		return 0, string(out)
	}
	for _, c := range []string{"unshare", "clone_newnet", "bpf", "io_uring", "ptrace", "keyctl", "mount"} {
		if ws, out := run(c); !ws.Signaled() || ws.Signal() != syscall.SIGSYS {
			t.Errorf("%s: %v %q, want SIGSYS", c, ws, out)
		}
	}
	for _, c := range []string{"thread", "none"} {
		if ws, out := run(c); ws != 0 || out != "alive\n" {
			t.Errorf("%s: %v %q", c, ws, out)
		}
	}
	// A missing program is reported, not a crash.
	if err := exec.Command(l, filepath.Join(dir, "missing")).Run(); err == nil {
		t.Fatal("missing program ran")
	} else if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 127 {
		t.Fatalf("missing program: %v", err)
	}
	// A compiler that cannot build it is an error, not a silent pass.
	if _, err := BuildLauncher(ctx, filepath.Join(dir, "other"), "/bin/false"); err == nil {
		t.Fatal("build with /bin/false succeeded")
	}
}
