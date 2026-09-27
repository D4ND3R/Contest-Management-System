package ranking

import (
	"fmt"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
)

func rowsOf(totals ...float64) *Ranking {
	r := &Ranking{}
	for i, t := range totals {
		r.Rows = append(r.Rows, Row{Username: fmt.Sprintf("u%02d", i), Total: t})
	}
	r.sort()
	return r
}

// TestPlaces: ties share a place; unofficial and hidden rows take none and
// do not push the others down.
func TestPlaces(t *testing.T) {
	r := rowsOf(100, 90, 90, 80, 70)
	r.Rows[1].Unofficial = true // one of the 90s
	r.Rows[4].Hidden = true
	r.places()
	got := []int{}
	for _, row := range r.Rows {
		got = append(got, row.Place)
	}
	if fmt.Sprint(got) != "[1 0 2 3 0]" || !r.Unofficial {
		t.Fatalf("places %v (unofficial %v)", got, r.Unofficial)
	}
}

// TestMedals (IOI rule): at most 1/12 gold, 1/4 gold+silver, 1/2 medals,
// ties never split (the whole tie group is left out if it does not fit),
// no medal without points, unofficial participants are not counted.
func TestMedals(t *testing.T) {
	cases := []struct {
		totals []float64
		want   string
	}{
		// 24 contestants: 2 gold, 4 silver, 6 bronze.
		{[]float64{240, 230, 220, 210, 200, 190, 180, 170, 160, 150, 140, 130, 120, 110, 100, 90, 80, 70, 60, 50, 40, 30, 20, 10},
			"gold:2@230 silver:4@190 bronze:6@130"},
		// A tie across the gold line: nobody in the tie gets gold.
		{[]float64{240, 230, 230, 210, 200, 190, 180, 170, 160, 150, 140, 130, 120, 110, 100, 90, 80, 70, 60, 50, 40, 30, 20, 10},
			"gold:1@240 silver:5@190 bronze:6@130"},
		// Zero scores win nothing.
		{[]float64{50, 40, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, "gold:1@50 silver:1@40"},
		{nil, ""},
	}
	for _, c := range cases {
		r := rowsOf(c.totals...)
		r.places()
		r.medals()
		got := ""
		for _, k := range r.Cutoffs {
			if got != "" {
				got += " "
			}
			got += fmt.Sprintf("%s:%d@%g", k.Medal, k.Count, k.Score)
		}
		if got != c.want {
			t.Errorf("%v: cutoffs %q, want %q", c.totals, got, c.want)
		}
		n := map[string]int{}
		for _, row := range r.Rows {
			n[row.Medal]++
		}
		for _, k := range r.Cutoffs {
			if n[k.Medal] != k.Count {
				t.Errorf("%v: %d rows with %s, cutoff says %d", c.totals, n[k.Medal], k.Medal, k.Count)
			}
		}
	}
	// Unofficial participants neither win nor count.
	r := rowsOf(100, 90, 80, 70, 60, 50, 40, 30, 20, 10, 5, 1, 0.5)
	r.Rows[0].Unofficial = true
	r.places()
	r.medals()
	if r.Rows[0].Medal != "" || r.Rows[1].Medal != "gold" || len(r.Cutoffs) == 0 || r.Cutoffs[0].Count != 1 {
		t.Fatalf("unofficial: %+v %+v", r.Rows[:2], r.Cutoffs)
	}
}

// TestBoardPlacesAndMedals: the public board shows places only when some
// row is unofficial (plain boards keep sending ranks alone), medals only
// when the contest publishes them, and the cutoffs never force clients to
// reload (they are not part of the header).
func TestBoardPlacesAndMedals(t *testing.T) {
	build := func(unofficial bool, medals string) *Board {
		r := rowsOf(240, 230, 220, 210, 200, 190, 180, 170, 160, 150, 140, 130, 120, 110, 100, 90, 80, 70, 60, 50, 40, 30, 20, 10)
		for i := range r.Rows {
			r.Rows[i].ParticipationID = int64(i + 1)
		}
		r.Rows[1].Unofficial = unofficial
		r.places()
		r.medals()
		return BuildBoard(r, sqlc.Contest{Name: "c", Medals: medals}, time.Now())
	}
	plain := build(false, "admins")
	if plain.Unofficial || plain.Cutoffs != nil || plain.Rows[0].Place != 0 || plain.Rows[0].Medal != "" || plain.Rows[1].ShownPlace() != "2" {
		t.Fatalf("plain board: %+v", plain.Rows[:2])
	}
	b := build(true, "public")
	if !b.Unofficial || !b.Rows[1].Unofficial || b.Rows[1].ShownPlace() != "–" || b.Rows[2].ShownPlace() != "2" || b.Rows[2].Rank != 3 {
		t.Fatalf("unofficial board: %+v", b.Rows[:3])
	}
	if b.Rows[0].Medal != "gold" || len(b.Cutoffs) != 3 || b.Header().Cutoffs != nil {
		t.Fatalf("medals: %+v %+v", b.Rows[0], b.Cutoffs)
	}
}
