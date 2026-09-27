package webkit

import "testing"

func TestLettersAndPercent(t *testing.T) {
	for i, want := range map[int]string{0: "A", 1: "B", 25: "Z", 26: "AA", 27: "AB", 51: "AZ", 52: "BA", -1: ""} {
		if got := Letter(i); got != want {
			t.Errorf("Letter(%d) = %q, want %q", i, got, want)
		}
	}
	if Percent(int64(1), int64(3)) != "33" || Percent(int64(1), int64(40)) != "2.5" || Percent(int64(5), int64(0)) != "0" {
		t.Fatalf("percent: %s %s", Percent(int64(1), int64(3)), Percent(int64(1), int64(40)))
	}
}
