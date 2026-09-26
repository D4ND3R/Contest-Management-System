package webkit

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestIconsExist: every icon a template or a Go file names is drawn (an
// unknown name would silently render nothing).
func TestIconsExist(t *testing.T) {
	re := regexp.MustCompile(`icon "([a-z0-9-]+)"|Icon: "([a-z0-9-]+)"`)
	seen := 0
	for _, root := range []string{"../../web/templates", "../../internal"} {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !(strings.HasSuffix(path, ".html") || strings.HasSuffix(path, ".go")) {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, m := range re.FindAllStringSubmatch(string(data), -1) {
				name := m[1] + m[2]
				seen++
				if Icon(name) == "" {
					t.Errorf("%s: unknown icon %q", path, name)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if seen < 50 {
		t.Fatalf("only %d icon uses found", seen)
	}
	for _, n := range IconNames() {
		if s := string(Icon(n)); !strings.HasPrefix(s, `<svg class="ic" viewBox="0 0 24 24"`) || strings.Contains(s, "<script") {
			t.Errorf("icon %s: %s", n, s)
		}
	}
}

func TestLettersAndInitials(t *testing.T) {
	for i, want := range map[int]string{0: "A", 1: "B", 25: "Z", 26: "AA", 27: "AB", 51: "AZ", 52: "BA", -1: ""} {
		if got := Letter(i); got != want {
			t.Errorf("Letter(%d) = %q, want %q", i, got, want)
		}
	}
	for in, want := range map[string]string{"Ana López": "AL", "ana": "AN", "Los Compiladores Unidos": "LC", "x": "X", "": "", "ñandú": "ÑA", "team_42": "T4"} {
		if got := Initials(in); got != want {
			t.Errorf("Initials(%q) = %q, want %q", in, got, want)
		}
	}
	if AvatarClass("ana") != AvatarClass("ana") || !strings.HasPrefix(AvatarClass("x"), "c") {
		t.Fatal("avatar class is not stable")
	}
	if Percent(int64(1), int64(3)) != "33" || Percent(int64(1), int64(40)) != "2.5" || Percent(int64(5), int64(0)) != "0" {
		t.Fatalf("percent: %s %s", Percent(int64(1), int64(3)), Percent(int64(1), int64(40)))
	}
}

func TestDonutAndLineChart(t *testing.T) {
	d := string(Donut("3/5", "solved", 3, 1, 1))
	if strings.Count(d, `stroke-dasharray`) != 3 || !strings.Contains(d, ">3/5</text>") || !strings.Contains(d, `class="d1"`) {
		t.Fatalf("donut: %s", d)
	}
	if e := string(Donut("0/0", "<b>", 0, 0, 0)); strings.Contains(e, "dasharray") || strings.Contains(e, "<b>") {
		t.Fatalf("empty donut: %s", e)
	}
	c := string(LineChart([]string{"10:00", "10:15", "10:30"}, Series{Class: "s1", Area: "a1", Values: []float64{0, 4, 2}},
		Series{Class: "s2", Values: []float64{1, 7, 3}}))
	if strings.Count(c, "<polyline") != 2 || strings.Count(c, "<polygon") != 1 || !strings.Contains(c, ">10:30</text>") {
		t.Fatalf("chart: %s", c)
	}
	// The axis reaches the maximum with round steps (7 → 0, 2, 4, 6, 8).
	if !strings.Contains(c, ">8</text>") || strings.Contains(c, ">7</text>") {
		t.Fatalf("axis: %s", c)
	}
	if z := string(LineChart(nil, Series{Class: "s1"})); !strings.HasPrefix(z, `<svg class="chart"`) {
		t.Fatalf("empty chart: %s", z)
	}
}
