package checkers

import (
	"bytes"
	"strings"
	"testing"
)

func white(a, b string) bool {
	ok, err := CompareWhite(strings.NewReader(a), strings.NewReader(b))
	if err != nil {
		panic(err)
	}
	return ok
}

func TestCompareWhite(t *testing.T) {
	yes := [][2]string{
		{"1 2 3\n", "1 2 3\n"},
		{"1 2 3\n", "1   2\t3"},
		{"1 2 3\n", "  1 2 3  \r\n\n\n"},
		{"a\nb\n\n\n", "a\nb"},
		{"", ""},
		{"", "\n \n\t\n"},
		{"x", "x\n"},
	}
	for _, c := range yes {
		if !white(c[0], c[1]) {
			t.Errorf("white(%q, %q) = false, want true", c[0], c[1])
		}
	}
	no := [][2]string{
		{"1 2 3\n", "1 2 3 4\n"},
		{"1 2\n3\n", "1 2 3\n"}, // line structure matters
		{"1\n\n2\n", "1\n2\n"},  // blank lines in the middle matter
		{"a\n", "a\nb\n"},
		{"a\nb\n", "a\n"},
		{"10\n", "010\n"},
		{"", "0"},
	}
	for _, c := range no {
		if white(c[0], c[1]) {
			t.Errorf("white(%q, %q) = true, want false", c[0], c[1])
		}
	}
}

func TestCompareWhiteLongLines(t *testing.T) {
	long := strings.Repeat("123456789 ", 200000) // 2 MB single line
	if !white(long+"\n", strings.ReplaceAll(long, " ", "  ")) {
		t.Fatal("long lines must compare equal")
	}
	if white(long, long+"x") {
		t.Fatal("difference at the end of a long line missed")
	}
}

func TestCompareExact(t *testing.T) {
	big := bytes.Repeat([]byte("abc"), 100000)
	cases := []struct {
		a, b []byte
		want bool
	}{
		{nil, nil, true},
		{[]byte("x"), []byte("x"), true},
		{[]byte("x\n"), []byte("x"), false},
		{big, big, true},
		{big, append(append([]byte{}, big...), 'x'), false},
		{big, big[:len(big)-1], false},
	}
	for i, c := range cases {
		got, err := CompareExact(bytes.NewReader(c.a), bytes.NewReader(c.b))
		if err != nil || got != c.want {
			t.Errorf("case %d: got %v %v", i, got, err)
		}
	}
}

func TestCompareFloat(t *testing.T) {
	f := func(a, b string, abs, rel float64) bool {
		ok, err := CompareFloat(strings.NewReader(a), strings.NewReader(b), abs, rel)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if !f("3.14159265\n2\n", "3.1415927 2.0", 1e-6, 0) {
		t.Error("within absolute tolerance")
	}
	if f("3.14159265", "3.1416", 1e-6, 0) {
		t.Error("outside absolute tolerance")
	}
	if !f("1000000", "1000001", 0, 1e-5) {
		t.Error("within relative tolerance")
	}
	if f("1 2", "1 2 3", 1, 1) || f("1 2 3", "1 2", 1, 1) {
		t.Error("token count must match")
	}
	if !f("YES 1.0", "YES 1.0000001", 1e-6, 0) || f("YES 1.0", "NO 1.0", 1e-6, 0) {
		t.Error("non-numeric tokens must match exactly")
	}
	if f("1", "nan", 1, 1) || f("1", "inf", 1e300, 0) {
		t.Error("NaN/Inf never match")
	}
}

func TestCompareDispatch(t *testing.T) {
	o, err := Compare(WhiteDiff, strings.NewReader("1\n"), strings.NewReader("1"), Params{})
	if err != nil || o != Correct {
		t.Fatalf("%+v %v", o, err)
	}
	o, _ = Compare(Exact, strings.NewReader("1\n"), strings.NewReader("1"), Params{})
	if o != Wrong {
		t.Fatalf("%+v", o)
	}
	if _, err := Compare("magic", nil, nil, Params{}); err == nil {
		t.Fatal("unknown comparator must fail")
	}
}

func TestParseCMSChecker(t *testing.T) {
	o, err := ParseCMSChecker([]byte("0.5\n"), []byte("translate:partial\n"))
	if err != nil || o.Score != 0.5 || o.Message != MsgPartial {
		t.Fatalf("%+v %v", o, err)
	}
	o, _ = ParseCMSChecker([]byte("1"), nil)
	if o.Message != MsgCorrect {
		t.Fatalf("default message %q", o.Message)
	}
	o, _ = ParseCMSChecker([]byte("0.0"), []byte("Wrong answer on line 3\nmore"))
	if o.Score != 0 || o.Message != "Wrong answer on line 3" {
		t.Fatalf("%+v", o)
	}
	for _, bad := range []string{"", "abc", "1.5", "-0.1", "nan"} {
		if _, err := ParseCMSChecker([]byte(bad), nil); err == nil {
			t.Errorf("score %q accepted", bad)
		}
	}
}

func TestParseTestlib(t *testing.T) {
	cases := []struct {
		code   int
		stderr string
		score  float64
		fail   bool
	}{
		{0, "ok 3 numbers", 1, false},
		{1, "wrong answer expected 3, found 4", 0, false},
		{2, "wrong output format", 0, false},
		{3, "FAIL bad answer file", 0, true},
		{7, "points 0.25", 0.25, false},
		{7, "points 40 partially correct", 0.4, false},
		{16 + 30, "partially correct", 0.3, false},
		{42 + 100, "", 0, true},
	}
	for _, c := range cases {
		o, err := ParseTestlib(c.code, []byte(c.stderr))
		if (err != nil) != c.fail {
			t.Errorf("code %d: err %v", c.code, err)
			continue
		}
		if !c.fail && o.Score != c.score {
			t.Errorf("code %d: score %v want %v", c.code, o.Score, c.score)
		}
	}
}

func BenchmarkWhiteDiff10MB(b *testing.B) {
	var sb strings.Builder
	for sb.Len() < 10<<20 {
		sb.WriteString("123456 7890123 456\n")
	}
	s := sb.String()
	b.SetBytes(int64(len(s)))
	for b.Loop() {
		if ok, _ := CompareWhite(strings.NewReader(s), strings.NewReader(s)); !ok {
			b.Fatal("mismatch")
		}
	}
}
