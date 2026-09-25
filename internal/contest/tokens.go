package contest

import (
	"sort"
	"time"
)

// TokenRules are the token settings of a contest or of a task.
type TokenRules struct {
	// Mode is "disabled", "finite" or "infinite".
	Mode string
	// MaxNumber caps the tokens played in total (nil: no cap).
	MaxNumber *int32
	// MinInterval is the least time between two tokens.
	MinInterval time.Duration
	// Finite mode: GenInitial tokens at the start, GenNumber more every
	// GenInterval, never more than GenMax available (nil: no limit).
	GenInitial, GenNumber int32
	GenInterval           time.Duration
	GenMax                *int32
}

// TokenState is what a contestant can do with the tokens of one pool.
type TokenState struct {
	// Available tokens now; Unlimited in infinite mode.
	Available int
	Unlimited bool
	// Next is when the next token is generated (zero when none will be).
	Next time.Time
	// Wait is set while the minimum interval since the last token runs.
	Wait time.Duration
	// Exhausted: the total cap is reached.
	Exhausted bool
}

// CanPlay reports whether a token of this pool can be played now.
func (s TokenState) CanPlay() bool {
	return (s.Unlimited || s.Available > 0) && s.Wait == 0 && !s.Exhausted
}

// Tokens computes a pool's state at now for a contestant whose window began
// at start and who played the tokens at the given times (of this pool: all
// the contest's for the contest pool, the task's for a task pool).
func Tokens(r TokenRules, start time.Time, played []time.Time, now time.Time) TokenState {
	var s TokenState
	if r.Mode == "disabled" || r.Mode == "" {
		s.Exhausted = true
		return s
	}
	ps := append([]time.Time(nil), played...)
	sort.Slice(ps, func(i, j int) bool { return ps[i].Before(ps[j]) })
	if r.MaxNumber != nil && len(ps) >= int(*r.MaxNumber) {
		s.Exhausted = true
	}
	if n := len(ps); n > 0 && r.MinInterval > 0 {
		if next := ps[n-1].Add(r.MinInterval); now.Before(next) {
			s.Wait = next.Sub(now)
		}
	}
	if r.Mode == "infinite" {
		s.Unlimited = true
		return s
	}
	capped := func(v int) int {
		if r.GenMax != nil && v > int(*r.GenMax) {
			return int(*r.GenMax)
		}
		return v
	}
	// Replay generations and plays in order (a play at the same instant
	// as a generation comes after it).
	avail := capped(int(r.GenInitial))
	gen := 1
	genAt := func(k int) time.Time { return start.Add(time.Duration(k) * r.GenInterval) }
	for _, p := range ps {
		for r.GenInterval > 0 && r.GenNumber > 0 && !genAt(gen).After(p) {
			avail = capped(avail + int(r.GenNumber))
			gen++
		}
		avail--
	}
	for r.GenInterval > 0 && r.GenNumber > 0 && !genAt(gen).After(now) {
		avail = capped(avail + int(r.GenNumber))
		gen++
	}
	s.Available = max(avail, 0)
	if r.GenInterval > 0 && r.GenNumber > 0 && (r.GenMax == nil || s.Available < int(*r.GenMax)) {
		s.Next = genAt(gen)
	}
	return s
}
