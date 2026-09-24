package sandbox

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// compileC builds a static C program on the host for sandbox tests.
func compileC(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	c := filepath.Join(dir, "p.c")
	os.WriteFile(c, []byte(src), 0o644)
	out := filepath.Join(dir, "p")
	if b, err := exec.Command("gcc", "-O2", "-static", "-o", out, c).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v %s", err, b)
	}
	return out
}

func testSlot(t *testing.T) *Slot {
	iso := TestIsolate(t)
	s := NewSlot(iso, 0, 0, []int{900, 901, 902, 903})
	t.Cleanup(func() { s.Close(context.Background()) })
	return s
}

func TestIsolateRunBasics(t *testing.T) {
	s := testSlot(t)
	ctx := context.Background()
	exe := compileC(t, `#include <stdio.h>
int main(){int a,b; if(scanf("%d %d",&a,&b)!=2) return 3; printf("%d\n",a+b); fprintf(stderr,"dbg\n"); return 0;}`)
	b, err := s.Box(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.CopyIn(exe, "prog", 0o755); err != nil {
		t.Fatal(err)
	}
	b.WriteFile("in.txt", []byte("2 40\n"), 0o644)
	res, err := b.Run(ctx, &Spec{Args: []string{"./prog"}, Stdin: "in.txt", Stdout: "out.txt", Stderr: "err.txt",
		Limits: Limits{CPUTime: time.Second, WallTime: 3 * time.Second, Memory: 64 << 20, FileSize: 1 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success() || res.Memory <= 0 || res.WallTime <= 0 {
		t.Fatalf("result %+v", res)
	}
	out, trunc, err := b.ReadFile("out.txt", 100)
	if err != nil || string(out) != "42\n" || trunc {
		t.Fatalf("out = %q %v %v", out, trunc, err)
	}
	if e, _, _ := b.ReadFile("err.txt", 100); string(e) != "dbg\n" {
		t.Fatalf("stderr = %q", e)
	}

	// The next box is pristine.
	b2, err := s.Box(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(b2.Dir())
	if len(ents) != 0 {
		t.Fatalf("box not wiped: %d entries", len(ents))
	}
	b2.CopyIn(exe, "prog", 0o755)
	res, err = b2.Run(ctx, &Spec{Args: []string{"./prog"}, Limits: Limits{CPUTime: time.Second}})
	if err != nil || res.Status != StatusNonZero || res.ExitCode != 3 {
		t.Fatalf("nonzero exit: %+v %v", res, err)
	}
}

func TestOutputRefusesSymlinks(t *testing.T) {
	s := testSlot(t)
	ctx := context.Background()
	exe := compileC(t, `#include <unistd.h>
int main(){ symlink("/etc/hostname","out.txt"); mkfifo("fifo.txt",0666); return 0; }`)
	b, _ := s.Box(ctx, 0)
	b.CopyIn(exe, "prog", 0o755)
	res, err := b.Run(ctx, &Spec{Args: []string{"./prog"}, Limits: Limits{CPUTime: time.Second}})
	if err != nil || !res.Success() {
		t.Fatalf("%+v %v", res, err)
	}
	// isolate itself removes special files after the run; either way the
	// worker must never read through the link.
	if data, _, err := b.ReadFile("out.txt", 100); err == nil || !(errors.Is(err, ErrUnsafeFile) || errors.Is(err, os.ErrNotExist)) {
		t.Fatalf("symlink must not be followed: data=%q err=%v", data, err)
	}
	if _, _, err := b.ReadFile("fifo.txt", 100); err == nil {
		t.Fatal("fifo must not be readable as output")
	}
	// Second layer: even if a symlink survives, OpenOutput refuses it.
	os.Symlink("/etc/hostname", b.Path("planted"))
	if _, _, err := b.ReadFile("planted", 100); !errors.Is(err, ErrUnsafeFile) {
		t.Fatalf("planted symlink must be refused, got %v", err)
	}
}

func TestFreshBoxAfterTampering(t *testing.T) {
	s := testSlot(t)
	ctx := context.Background()
	exe := compileC(t, `#include <sys/stat.h>
#include <stdio.h>
int main(){ mkdir("d",0700); FILE*f=fopen("d/x","w"); fputs("x",f); fclose(f); chmod("d",0); 
  FILE*g=fopen("/tmp/leak","w"); fputs("secret",g); fclose(g); chmod("/box",0); return 0; }`)
	b, _ := s.Box(ctx, 0)
	b.CopyIn(exe, "prog", 0o755)
	if res, err := b.Run(ctx, &Spec{Args: []string{"./prog"}, Limits: Limits{CPUTime: time.Second}}); err != nil || !res.Success() {
		t.Fatalf("%+v %v", res, err)
	}
	for i := 0; i < 2; i++ { // both physical boxes behind logical box 0
		var err error
		b, err = s.Box(ctx, 0)
		if err != nil {
			t.Fatalf("box after tampering: %v", err)
		}
		if _, err := os.Stat(filepath.Join(b.Root, "tmp")); !os.IsNotExist(err) {
			t.Fatal("private /tmp must be wiped between runs")
		}
	}
	// The next program must not see the previous /tmp content.
	exe2 := compileC(t, `#include <stdio.h>
int main(){ FILE*g=fopen("/tmp/leak","r"); puts(g? "LEAK" : "clean"); return 0; }`)
	b.CopyIn(exe2, "prog", 0o755)
	res, err := b.Run(ctx, &Spec{Args: []string{"./prog"}, Stdout: "o", Limits: Limits{CPUTime: time.Second}})
	if err != nil || !res.Success() {
		t.Fatalf("%+v %v", res, err)
	}
	if out, _, _ := b.ReadFile("o", 100); strings.TrimSpace(string(out)) != "clean" {
		t.Fatalf("second program saw %q", out)
	}
}

func BenchmarkRunTrivial(b *testing.B) {
	iso := TestIsolate(b)
	s := NewSlot(iso, 0, 1, []int{950, 951})
	s.SetMaintenanceCores(Complement([]int{1}))
	defer s.Close(context.Background())
	dir := b.TempDir()
	os.WriteFile(filepath.Join(dir, "p.c"), []byte("int main(){return 0;}"), 0o644)
	exec.Command("gcc", "-O2", "-static", "-o", filepath.Join(dir, "p"), filepath.Join(dir, "p.c")).Run()
	ctx := context.Background()
	for b.Loop() {
		box, err := s.Box(ctx, 0)
		if err != nil {
			b.Fatal(err)
		}
		box.CopyIn(filepath.Join(dir, "p"), "p", 0o755)
		if _, err := box.Run(ctx, &Spec{Args: []string{"./p"}, Limits: Limits{CPUTime: time.Second, Memory: 64 << 20}}); err != nil {
			b.Fatal(err)
		}
	}
}

func TestMemoryAccountingIsPerRun(t *testing.T) {
	s := testSlot(t)
	ctx := context.Background()
	hog := compileC(t, `#include <stdlib.h>
#include <string.h>
int main(){ for(int i=0;i<200;i++){ char*p=malloc(1<<20); if(!p) return 1; memset(p,1,1<<20);} return 0; }`)
	small := compileC(t, `int main(){return 0;}`)
	lim := Limits{CPUTime: 2 * time.Second, WallTime: 5 * time.Second, Memory: 64 << 20}
	for i := 0; i < 3; i++ {
		b, err := s.Box(ctx, 0)
		if err != nil {
			t.Fatal(err)
		}
		b.CopyIn(hog, "p", 0o755)
		res, err := b.Run(ctx, &Spec{Args: []string{"./p"}, Limits: lim})
		if err != nil || res.Status != StatusMemory {
			t.Fatalf("hog: %+v %v", res, err)
		}
		b, _ = s.Box(ctx, 0)
		b.CopyIn(small, "p", 0o755)
		res, err = b.Run(ctx, &Spec{Args: []string{"./p"}, Limits: lim})
		if err != nil || res.Status != StatusOK || res.Memory > 16<<20 {
			t.Fatalf("run after an OOM must start clean: %+v %v", res, err)
		}
	}
}

func TestPrivateDevShm(t *testing.T) {
	s := testSlot(t)
	ctx := context.Background()
	exe := compileC(t, `#include <stdio.h>
int main(){ FILE*f=fopen("/dev/shm/cms_shm_probe","w"); if(!f) return 1; fputs("x",f); fclose(f); return 0; }`)
	b, _ := s.Box(ctx, 0)
	b.CopyIn(exe, "p", 0o755)
	res, err := b.Run(ctx, &Spec{Args: []string{"./p"}, Limits: Limits{CPUTime: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("/dev/shm/cms_shm_probe"); err == nil {
		os.Remove("/dev/shm/cms_shm_probe")
		t.Fatal("the sandbox wrote to the host /dev/shm")
	}
	if res.Status != StatusOK {
		t.Fatalf("a private /dev/shm must be writable: %+v", res)
	}
}
