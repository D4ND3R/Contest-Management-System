package scoring

import (
	"math"
	"sort"
	"time"
)

// Submission is one submission's contribution to a task score.
type Submission struct {
	ID       int64
	Time     time.Time
	Official bool
	Tokened  bool
	// Scored is false while the submission is still being judged.
	Scored bool
	// CompileError marks submissions that did not compile (never ICPC
	// penalties).
	CompileError bool
	Score        float64
	// Subtasks are the ranking details (per-subtask scores).
	Subtasks []float64
}

// TaskScore is the aggregate of a participation on a task.
type TaskScore struct {
	Score    float64
	Subtasks []float64
	// Pending counts official submissions not yet scored.
	Pending int
	// LastSubmission is the time of the latest official submission.
	LastSubmission *time.Time
}

// Score modes.
const (
	ModeMax            = "max"
	ModeMaxSubtask     = "max_subtask"
	ModeMaxTokenedLast = "max_tokened_last"
)

// Aggregate computes a task score with the given mode. Only official
// submissions count; unscored ones are counted as pending.
func Aggregate(mode string, subs []Submission, precision int) TaskScore {
	var ts TaskScore
	var official []Submission
	for _, s := range subs {
		if !s.Official {
			continue
		}
		t := s.Time
		if ts.LastSubmission == nil || t.After(*ts.LastSubmission) {
			ts.LastSubmission = &t
		}
		if !s.Scored {
			ts.Pending++
		}
		official = append(official, s)
	}
	sort.SliceStable(official, func(i, j int) bool { return official[i].Time.Before(official[j].Time) })
	switch mode {
	case ModeMaxSubtask:
		for _, s := range official {
			if !s.Scored {
				continue
			}
			for i, v := range s.Subtasks {
				if i >= len(ts.Subtasks) {
					ts.Subtasks = append(ts.Subtasks, v)
				} else {
					ts.Subtasks[i] = math.Max(ts.Subtasks[i], v)
				}
			}
		}
		for _, v := range ts.Subtasks {
			ts.Score += v
		}
	case ModeMaxTokenedLast:
		var best *Submission
		consider := func(s *Submission) {
			if s.Scored && (best == nil || s.Score > best.Score) {
				best = s
			}
		}
		if n := len(official); n > 0 {
			consider(&official[n-1])
		}
		for i := range official {
			if official[i].Tokened {
				consider(&official[i])
			}
		}
		if best != nil {
			ts.Score, ts.Subtasks = best.Score, append([]float64(nil), best.Subtasks...)
		}
	default: // ModeMax
		var best *Submission
		for i := range official {
			s := &official[i]
			if s.Scored && (best == nil || s.Score > best.Score) {
				best = s
			}
		}
		if best != nil {
			ts.Score, ts.Subtasks = best.Score, append([]float64(nil), best.Subtasks...)
		}
	}
	ts.Score = round(ts.Score, precision)
	return ts
}

// ICPCTask is the ICPC view of a participation on a task.
type ICPCTask struct {
	Solved bool
	// Attempts are the rejected submissions before the first accepted one
	// (compilation errors do not count).
	Attempts int
	SolvedAt *time.Time
	Pending  int
}

// ICPC aggregates submissions with a binary verdict: a submission is
// accepted when it reaches maxScore.
func ICPC(subs []Submission, maxScore float64) ICPCTask {
	var r ICPCTask
	official := make([]Submission, 0, len(subs))
	for _, s := range subs {
		if s.Official {
			official = append(official, s)
		}
	}
	sort.SliceStable(official, func(i, j int) bool { return official[i].Time.Before(official[j].Time) })
	for _, s := range official {
		if r.Solved {
			break
		}
		switch {
		case !s.Scored:
			r.Pending++
		case s.CompileError:
		case s.Score >= maxScore-1e-9 && maxScore > 0:
			r.Solved = true
			t := s.Time
			r.SolvedAt = &t
		default:
			r.Attempts++
		}
	}
	return r
}

// ICPCPenalty returns the penalty minutes of a solved task: minutes from
// the participation start to the accepted submission plus penaltyMinutes
// for each rejected attempt.
func ICPCPenalty(t ICPCTask, start time.Time, penaltyMinutes int) int {
	if !t.Solved || t.SolvedAt == nil {
		return 0
	}
	mins := int(t.SolvedAt.Sub(start) / time.Minute)
	if mins < 0 {
		mins = 0
	}
	return mins + penaltyMinutes*t.Attempts
}
