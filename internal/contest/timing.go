// Package contest holds the contest rules shared by the web services and
// the CLI: the time window of each participation and what a contestant may
// do at a given instant.
package contest

import "time"

// Phase of a participation.
type Phase int

const (
	// NotStarted: the contest window has not opened yet.
	NotStarted Phase = iota
	// WaitingStart: per-user-time contest open, the contestant has not
	// pressed "start" yet.
	WaitingStart
	// Running: the contestant's window is open.
	Running
	// Finished: the window closed (and no analysis mode is running).
	Finished
	// Analysis: the contest is over and analysis mode is open (submissions
	// are accepted but unofficial).
	Analysis
	// Practice: the contest (and any analysis window) is over and practice
	// (upsolving) is enabled: unofficial submissions at any time.
	Practice
)

func (p Phase) String() string {
	return [...]string{"not_started", "waiting_start", "running", "finished", "analysis", "practice"}[p]
}

// Rules are the contest-level timing settings.
type Rules struct {
	Start, Stop     time.Time
	PerUserTime     time.Duration // 0 = everyone shares the global window
	AnalysisEnabled bool
	AnalysisStart   *time.Time
	AnalysisStop    *time.Time
	// Practice keeps unofficial submissions open after the contest.
	Practice bool
}

// Participant are the per-participation timing settings.
type Participant struct {
	StartingTime *time.Time    // when a per-user-time contestant pressed start
	SiteStart    *time.Time    // the participant's site starts at its own time
	Delay        time.Duration // shifts the whole window
	Extra        time.Duration // extends the end of the window
	Unrestricted bool          // may act at any time
}

// Status describes a participation at an instant.
type Status struct {
	Phase Phase
	// Begin/End of the contestant's effective window (End is zero while
	// waiting to start in per-user-time mode).
	Begin, End time.Time
	// Remaining time in the window (Running only).
	Remaining time.Duration
	// CanStart: per-user-time contestant may press start now.
	CanStart bool
	// CanSubmit: submissions (and user tests, questions) are accepted.
	CanSubmit bool
	// Official: submissions made now count for the ranking.
	Official bool
}

// Compute evaluates the rules for a participant at now.
func Compute(r Rules, p Participant, now time.Time) Status {
	start, stopAt := r.Start, r.Stop
	if p.SiteStart != nil {
		// Same duration, shifted to the site's start.
		start, stopAt = *p.SiteStart, p.SiteStart.Add(r.Stop.Sub(r.Start))
	}
	begin := start.Add(p.Delay)
	stop := stopAt.Add(p.Delay) // last instant a per-user contestant may start
	end := stop.Add(p.Extra)
	s := Status{Begin: begin, End: end}
	if r.PerUserTime > 0 {
		if p.StartingTime == nil {
			s.End = time.Time{}
			switch {
			case now.Before(begin):
				s.Phase = NotStarted
			case now.Before(stop):
				s.Phase, s.CanStart = WaitingStart, true
			default:
				s.Phase = Finished
			}
		} else {
			s.Begin = *p.StartingTime
			userEnd := p.StartingTime.Add(r.PerUserTime + p.Extra)
			if userEnd.Before(end) {
				end = userEnd
			}
			s.End = end
		}
	}
	if s.Phase == 0 && !(r.PerUserTime > 0 && p.StartingTime == nil) {
		switch {
		case now.Before(s.Begin):
			s.Phase = NotStarted
		case now.Before(s.End):
			s.Phase = Running
			s.Remaining = s.End.Sub(now)
		default:
			s.Phase = Finished
		}
	}
	if s.Phase == Finished && r.AnalysisEnabled && r.AnalysisStart != nil && r.AnalysisStop != nil &&
		!now.Before(*r.AnalysisStart) && now.Before(*r.AnalysisStop) {
		s.Phase = Analysis
	}
	if s.Phase == Finished && r.Practice && (r.AnalysisStop == nil || !r.AnalysisEnabled || !now.Before(*r.AnalysisStop)) {
		s.Phase = Practice
	}
	switch s.Phase {
	case Running:
		s.CanSubmit, s.Official = true, true
	case Analysis, Practice:
		s.CanSubmit, s.Official = true, false
	}
	if p.Unrestricted {
		s.CanSubmit = true
		// Unrestricted submissions during analysis or practice stay unofficial.
		s.Official = s.Phase != Analysis && s.Phase != Practice
	}
	return s
}
