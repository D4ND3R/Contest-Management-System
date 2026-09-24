package contest

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)

func at(h float64) time.Time { return t0.Add(time.Duration(h * float64(time.Hour))) }

func TestGlobalWindow(t *testing.T) {
	r := Rules{Start: at(0), Stop: at(5)}
	cases := []struct {
		now   float64
		phase Phase
		sub   bool
	}{
		{-0.1, NotStarted, false}, {0, Running, true}, {4.99, Running, true}, {5, Finished, false},
	}
	for _, c := range cases {
		s := Compute(r, Participant{}, at(c.now))
		if s.Phase != c.phase || s.CanSubmit != c.sub || (s.CanSubmit && !s.Official) {
			t.Errorf("t=%v: %+v", c.now, s)
		}
	}
	if s := Compute(r, Participant{}, at(4)); s.Remaining != time.Hour {
		t.Errorf("remaining %v", s.Remaining)
	}
}

func TestDelayAndExtra(t *testing.T) {
	r := Rules{Start: at(0), Stop: at(5)}
	p := Participant{Delay: 30 * time.Minute, Extra: 15 * time.Minute}
	if s := Compute(r, p, at(0.25)); s.Phase != NotStarted {
		t.Errorf("delayed contestant started early: %+v", s)
	}
	s := Compute(r, p, at(5.6))
	if s.Phase != Running || !s.End.Equal(at(5.75)) {
		t.Errorf("window not shifted/extended: %+v", s)
	}
	if s := Compute(r, p, at(5.75)); s.Phase != Finished {
		t.Errorf("end: %+v", s)
	}
}

func TestPerUserTime(t *testing.T) {
	r := Rules{Start: at(0), Stop: at(24), PerUserTime: 3 * time.Hour}
	if s := Compute(r, Participant{}, at(-1)); s.Phase != NotStarted || s.CanStart {
		t.Errorf("before: %+v", s)
	}
	s := Compute(r, Participant{}, at(2))
	if s.Phase != WaitingStart || !s.CanStart || s.CanSubmit {
		t.Errorf("waiting: %+v", s)
	}
	started := at(2)
	s = Compute(r, Participant{StartingTime: &started}, at(4))
	if s.Phase != Running || !s.End.Equal(at(5)) || s.Remaining != time.Hour {
		t.Errorf("running: %+v", s)
	}
	if s := Compute(r, Participant{StartingTime: &started}, at(5)); s.Phase != Finished {
		t.Errorf("after own window: %+v", s)
	}
	// Starting late is capped by the contest end.
	late := at(23)
	s = Compute(r, Participant{StartingTime: &late}, at(23.5))
	if !s.End.Equal(at(24)) {
		t.Errorf("late start end = %v", s.End)
	}
	// Extra time extends both the personal window and the cap.
	s = Compute(r, Participant{StartingTime: &late, Extra: time.Hour}, at(24.5))
	if s.Phase != Running || !s.End.Equal(at(25)) {
		t.Errorf("late start with extra: %+v", s)
	}
	// Never started and the contest is over.
	if s := Compute(r, Participant{}, at(24)); s.Phase != Finished || s.CanStart {
		t.Errorf("never started: %+v", s)
	}
}

func TestAnalysisAndUnrestricted(t *testing.T) {
	as, ae := at(6), at(8)
	r := Rules{Start: at(0), Stop: at(5), AnalysisEnabled: true, AnalysisStart: &as, AnalysisStop: &ae}
	if s := Compute(r, Participant{}, at(5.5)); s.Phase != Finished || s.CanSubmit {
		t.Errorf("gap before analysis: %+v", s)
	}
	s := Compute(r, Participant{}, at(7))
	if s.Phase != Analysis || !s.CanSubmit || s.Official {
		t.Errorf("analysis: %+v", s)
	}
	if s := Compute(r, Participant{}, at(8)); s.Phase != Finished {
		t.Errorf("after analysis: %+v", s)
	}
	u := Participant{Unrestricted: true}
	if s := Compute(r, u, at(-2)); !s.CanSubmit || !s.Official {
		t.Errorf("unrestricted before start: %+v", s)
	}
	if s := Compute(r, u, at(7)); !s.CanSubmit || s.Official {
		t.Errorf("unrestricted during analysis must be unofficial: %+v", s)
	}
}
