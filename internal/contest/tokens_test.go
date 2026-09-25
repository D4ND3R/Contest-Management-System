package contest

import (
	"testing"
	"time"
)

func i32(v int32) *int32 { return &v }

func TestTokens(t *testing.T) {
	start := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	at := func(min int) time.Time { return start.Add(time.Duration(min) * time.Minute) }
	finite := TokenRules{Mode: "finite", GenInitial: 2, GenNumber: 1, GenInterval: 30 * time.Minute, GenMax: i32(3)}

	if s := Tokens(TokenRules{Mode: "disabled"}, start, nil, at(10)); s.CanPlay() {
		t.Fatal("disabled pool playable")
	}
	s := Tokens(finite, start, nil, at(10))
	if s.Available != 2 || !s.CanPlay() || !s.Next.Equal(at(30)) {
		t.Fatalf("start: %+v", s)
	}
	// Two played at once: none left until the next generation.
	s = Tokens(finite, start, []time.Time{at(5), at(6)}, at(10))
	if s.Available != 0 || s.CanPlay() || !s.Next.Equal(at(30)) {
		t.Fatalf("spent: %+v", s)
	}
	s = Tokens(finite, start, []time.Time{at(5), at(6)}, at(61))
	if s.Available != 2 {
		t.Fatalf("after two generations: %+v", s)
	}
	// Generation stops at the cap (3) and restarts after a play.
	s = Tokens(finite, start, nil, at(200))
	if s.Available != 3 || !s.Next.IsZero() {
		t.Fatalf("capped: %+v", s)
	}
	s = Tokens(finite, start, []time.Time{at(200)}, at(201))
	if s.Available != 2 || !s.Next.Equal(at(210)) {
		t.Fatalf("after a play at the cap: %+v", s)
	}
	// Total cap and minimum interval.
	capped := finite
	capped.MaxNumber, capped.MinInterval = i32(2), 10*time.Minute
	if s := Tokens(capped, start, []time.Time{at(1)}, at(5)); s.CanPlay() || s.Wait != 6*time.Minute {
		t.Fatalf("interval: %+v", s)
	}
	if s := Tokens(capped, start, []time.Time{at(1), at(20)}, at(100)); s.CanPlay() || !s.Exhausted {
		t.Fatalf("max: %+v", s)
	}
	// Infinite: always, except for the interval.
	inf := TokenRules{Mode: "infinite", MinInterval: time.Minute}
	if s := Tokens(inf, start, []time.Time{at(1), at(2), at(3)}, at(10)); !s.CanPlay() || !s.Unlimited {
		t.Fatalf("infinite: %+v", s)
	}
	if s := Tokens(inf, start, []time.Time{at(10)}, at(10).Add(30*time.Second)); s.CanPlay() {
		t.Fatalf("infinite within the interval: %+v", s)
	}
}
