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
