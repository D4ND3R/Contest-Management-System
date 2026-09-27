package langs

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadRepositoryLanguages(t *testing.T) {
	r, err := Load(filepath.Join("..", "..", "config", "languages"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.All()) == 0 {
		t.Fatal("no languages loaded")
	}
	for _, l := range r.All() {
		if l.CompileLimits.Time == 0 || l.CompileLimits.Memory == 0 || l.CompileLimits.Processes == 0 {
			t.Errorf("%s: compile limits not defaulted: %+v", l.ID, l.CompileLimits)
		}
	}
	cpp, ok := r.Get("cpp17")
	if !ok || cpp.SourceExtension() != ".cpp" || !cpp.IsHeader("task.h") || !cpp.IsSource("a.cc") {
		t.Fatalf("cpp17 = %+v", cpp)
	}
}

func TestExpand(t *testing.T) {
	got := Expand([]string{"g++", "-o", "{executable}", "{sources}", "-DMAIN={main}", "-Xmx{memory_mb}m"},
		Vars{Sources: []string{"grader.cpp", "sol.cpp"}, Main: "grader", Executable: "grader", Memory: 256 << 20})
	want := []string{"g++", "-o", "grader", "grader.cpp", "sol.cpp", "-DMAIN=grader", "-Xmx256m"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestEnvList(t *testing.T) {
	l := &Language{Env: map[string]string{"PATH": "/opt/bin", "JAVA_OPTS": "x"}}
	env := strings.Join(l.EnvList(), " ")
	if !strings.Contains(env, "PATH=/opt/bin") || !strings.Contains(env, "HOME=/tmp") || !strings.Contains(env, "JAVA_OPTS=x") {
		t.Fatalf("env = %s", env)
	}
}

func TestLoadRejectsInvalid(t *testing.T) {
	cases := map[string]string{
		"missing run":   "id: x\nname: X\nsource_extensions: [.x]\nexecutable: a\n",
		"bad extension": "id: x\nname: X\nsource_extensions: [x]\nexecutable: a\nrun: [a]\n",
		"unknown key":   "id: x\nname: X\nsource_extensions: [.x]\nexecutable: a\nrun: [a]\nfoo: 1\n",
		"empty compile": "id: x\nname: X\nsource_extensions: [.x]\nexecutable: a\nrun: [a]\ncompile: [[]]\n",
		"invalid id":    "id: a b\nname: X\nsource_extensions: [.x]\nexecutable: a\nrun: [a]\n",
		"negative time": "id: x\nname: X\nsource_extensions: [.x]\nexecutable: a\nrun: [a]\ntime_multiplier: -1\n",
		"huge time":     "id: x\nname: X\nsource_extensions: [.x]\nexecutable: a\nrun: [a]\ntime_multiplier: 11\n",
	}
	for name, body := range cases {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "x.yaml"), []byte(body), 0o644)
		if _, err := Load(dir); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	dir := t.TempDir()
	ok := "id: x\nname: X\nsource_extensions: [.x]\nexecutable: a\nrun: [a]\n"
	os.WriteFile(filepath.Join(dir, "a.yaml"), []byte(ok), 0o644)
	os.WriteFile(filepath.Join(dir, "b.yaml"), []byte(ok), 0o644)
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("duplicate ids accepted: %v", err)
	}
}

// TestTimeMultiplier (SPEC_IOI §3): a language may scale the time limits.
func TestTimeMultiplier(t *testing.T) {
	var none *Language
	for _, c := range []struct {
		l      *Language
		in, ms int64
	}{{none, 1000, 1000}, {&Language{}, 1000, 1000}, {&Language{TimeMultiplier: 1}, 1000, 1000},
		{&Language{TimeMultiplier: 1.5}, 1000, 1500}, {&Language{TimeMultiplier: 3}, 333, 999}, {&Language{TimeMultiplier: 1.1}, 7, 8}, {&Language{TimeMultiplier: 1.1}, 1000, 1100},
		{&Language{TimeMultiplier: 2}, 0, 0}} {
		if got := c.l.ScaleMs(c.in); got != c.ms {
			t.Errorf("%v.ScaleMs(%d) = %d, want %d", c.l, c.in, got, c.ms)
		}
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "j.yaml"), []byte("id: j\nname: J\nsource_extensions: [.j]\nexecutable: a\nrun: [a]\ntime_multiplier: 2\n"), 0o644)
	r, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if l, _ := r.Get("j"); !l.Multiplied() || l.ScaleMs(1500) != 3000 {
		t.Fatalf("loaded %+v", l)
	}
}
