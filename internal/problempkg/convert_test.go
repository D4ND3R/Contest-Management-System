package problempkg

import (
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func fileText(t *testing.T, f File) string {
	t.Helper()
	rd, err := f.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()
	b, _ := io.ReadAll(rd)
	return string(b)
}

func names(fs []File) string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Name)
	}
	return strings.Join(out, " ")
}

// TestConvertItaly (SPEC_CLOSE D7): a CMS italy_yaml task becomes a
// package: testcases renamed in order, gen/GEN subtasks, the correttore as
// a CMS-protocol checker, solutions, statement and attachments.
func TestConvertItaly(t *testing.T) {
	zr := zipDir(t, filepath.Join("..", "..", "docs", "examples", "other-formats", "italy-suma"), "isuma/")
	p := Read(zr, options(t))
	if !p.OK() {
		t.Fatalf("errors: %v", p.Errors)
	}
	c := p.Config
	if p.Format != "italy_yaml" || c.Name != "isuma" || c.Type != "batch" || c.TimeLimit != 1 || c.MemoryLimit != 256 ||
		c.InputFile != "" || c.OutputFile != "" || c.Checker != "custom" || c.Scoring != "group_min" {
		t.Fatalf("config %+v", c)
	}
	if len(c.Subtasks) != 3 || c.Subtasks[1].Points != 40 || c.Subtasks[2].Tests.Count != 2 || p.MaxScore != 100 {
		t.Fatalf("subtasks %+v max %v", c.Subtasks, p.MaxScore)
	}
	if len(p.Tests) != 4 || p.Tests[0].Codename != "000" || !p.Tests[0].Public || p.Tests[1].Public {
		t.Fatalf("tests %+v", p.Tests)
	}
	if got := fileText(t, p.Tests[2].Input); got != "100 200\n" {
		t.Fatalf("test 002 input %q", got)
	}
	if names(p.Managers) != "checker.cpp" || names(p.Attachments) != "ejemplo.txt" || len(p.Statements) != 1 || p.Statements[0].Language != "es" {
		t.Fatalf("managers %q attachments %q statements %+v", names(p.Managers), names(p.Attachments), p.Statements)
	}
	var sols []string
	for _, s := range p.Solutions {
		sols = append(sols, s.Name+"="+s.Expected+"/"+s.Language)
	}
	if strings.Join(sols, " ") != "ac_soluzione.cpp=ac/cpp17 any_sbagliata.cpp=any/cpp17" {
		t.Fatalf("solutions %v", sols)
	}
	if len(p.Warnings) == 0 || !strings.Contains(p.Warnings[0].Message, "italy_yaml") {
		t.Errorf("warnings %v", p.Warnings)
	}

	// A grader, output-only and stdin defaults.
	p = Read(zipOf(t, map[string]string{
		"task.yaml":        "name: oo\ntitle: OO\noutput_only: true\ntotal_value: 50\nn_input: 2\n",
		"input/input0.txt": "1\n", "output/output0.txt": "1\n",
		"input/input1.txt": "2\n", "output/output1.txt": "4\n",
		"sol/grader.cpp":          "int main(){}",
		"statement/statement.pdf": "%PDF-1.4",
	}), options(t))
	if p.Config == nil || p.Config.Type != "output_only" || *p.Config.PointsPerTest != 25 || p.Config.InputFile != "" || p.MaxScore != 50 {
		t.Fatalf("output only: %+v %v", p.Config, p.Errors)
	}
	// Missing testcases are reported.
	p = Read(zipOf(t, map[string]string{"task.yaml": "name: x\ntime_limit: 1\nmemory_limit: 64\nn_input: 2\n",
		"input/input0.txt": "1", "output/output0.txt": "1"}), options(t))
	if p.OK() || !strings.Contains(p.Errors[0].Message, "testcase 1") {
		t.Fatalf("missing testcase: %v", p.Errors)
	}
}

// TestConvertPolygon (SPEC_CLOSE D7): a Polygon package becomes a package:
// tests from the patterns, groups as subtasks, the testlib checker,
// tagged solutions and the PDF statement.
func TestConvertPolygon(t *testing.T) {
	zr := zipDir(t, filepath.Join("..", "..", "docs", "examples", "other-formats", "polygon-suma"), "psuma/")
	p := Read(zr, options(t))
	if !p.OK() {
		t.Fatalf("errors: %v", p.Errors)
	}
	c := p.Config
	if p.Format != "polygon" || c.Name != "psuma" || c.Title != "Suma (paquete Polygon)" || c.TimeLimit != 1 || c.MemoryLimit != 256 ||
		c.Checker != "testlib" || c.Scoring != "group_min" || p.MaxScore != 100 {
		t.Fatalf("config %+v max %v", c, p.MaxScore)
	}
	if len(c.Subtasks) != 3 || strings.Join(c.Subtasks[2].Tests.List, ",") != "03,04" || c.Subtasks[2].Points != 60 {
		t.Fatalf("subtasks %+v", c.Subtasks)
	}
	if len(p.Tests) != 4 || !p.Tests[0].Public || fileText(t, p.Tests[3].Output) != "0\n" {
		t.Fatalf("tests %+v", p.Tests)
	}
	var sols []string
	for _, s := range p.Solutions {
		sols = append(sols, s.Name+"="+s.Expected)
	}
	if strings.Join(sols, " ") != "ac_suma.cpp=ac wa_resta.cpp=wa" {
		t.Fatalf("solutions %v", sols)
	}
	langs := map[string]string{}
	for _, s := range p.Statements {
		langs[s.Language] = s.ContentType
	}
	if langs["es"] != "application/pdf" || !strings.HasPrefix(langs["en"], "text/html") {
		t.Fatalf("statements %v", langs)
	}

	// The plain package (tests not generated) is refused with the remedy.
	p = Read(zipOf(t, map[string]string{"problem.xml": `<problem short-name="x"><judging><testset name="tests"><time-limit>1000</time-limit>
<memory-limit>268435456</memory-limit><test-count>2</test-count><input-path-pattern>tests/%02d</input-path-pattern>
<answer-path-pattern>tests/%02d.a</answer-path-pattern></testset></judging></problem>`}), options(t))
	if p.OK() || !strings.Contains(p.Errors[0].Message, "full package") {
		t.Fatalf("missing tests: %v", p.Errors)
	}
	// Points per test without groups; an interactor makes it interactive.
	p = Read(zipOf(t, map[string]string{"problem.xml": `<problem short-name="y"><judging><testset name="tests"><time-limit>2000</time-limit>
<memory-limit>67108864</memory-limit><test-count>2</test-count><tests><test points="10"/><test points="30"/></tests></testset></judging>
<assets><interactor><source path="files/interactor.cpp"/></interactor></assets></problem>`,
		"tests/01": "1", "tests/01.a": "1", "tests/02": "2", "tests/02.a": "2", "files/interactor.cpp": "int main(){}"}), options(t))
	if !p.OK() || p.Config.Type != "interactive" || p.MaxScore != 40 || len(p.Config.Subtasks) != 2 || names(p.Managers) != "interactor.cpp" {
		t.Fatalf("interactive: %+v %v %v", p.Config, p.Errors, p.MaxScore)
	}
}
