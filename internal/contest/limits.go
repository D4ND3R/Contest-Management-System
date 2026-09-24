package contest

import (
	"fmt"
	"time"
)

// Limit is a cap on how many and how often (seconds) something may be
// done. Nil fields mean "no limit".
type Limit struct {
	MaxNumber   *int32
	MinInterval *int64
}

// Usage is what a participation already did: counts contest-wide and on
// the task, and the last times (zero or Unix epoch when never).
type Usage struct {
	ContestCount, TaskCount int64
	ContestLast, TaskLast   time.Time
}

// LimitError explains why an action is refused.
type LimitError struct {
	// Key is a translatable message; Wait is set for interval limits.
	Key  string
	Max  int32
	Wait time.Duration
}

func (e *LimitError) Error() string {
	if e.Wait > 0 {
		return fmt.Sprintf("%s (wait %s)", e.Key, e.Wait.Round(time.Second))
	}
	return e.Key
}

// Limit messages.
const (
	MsgContestMax      = "You have reached the maximum number of submissions for this contest."
	MsgTaskMax         = "You have reached the maximum number of submissions for this task."
	MsgContestInterval = "Please wait before submitting again (contest-wide interval)."
	MsgTaskInterval    = "Please wait before submitting again on this task."
)

func unset(t time.Time) bool { return t.IsZero() || t.Unix() <= 0 }

// Check applies the contest-wide and per-task limits to an action at now.
// Messages are for submissions; user tests use the same rules.
func Check(contestLim, taskLim Limit, u Usage, now time.Time) error {
	if m := contestLim.MaxNumber; m != nil && u.ContestCount >= int64(*m) {
		return &LimitError{Key: MsgContestMax, Max: *m}
	}
	if m := taskLim.MaxNumber; m != nil && u.TaskCount >= int64(*m) {
		return &LimitError{Key: MsgTaskMax, Max: *m}
	}
	if iv := contestLim.MinInterval; iv != nil && *iv > 0 && !unset(u.ContestLast) {
		if next := u.ContestLast.Add(time.Duration(*iv) * time.Second); now.Before(next) {
			return &LimitError{Key: MsgContestInterval, Wait: next.Sub(now)}
		}
	}
	if iv := taskLim.MinInterval; iv != nil && *iv > 0 && !unset(u.TaskLast) {
		if next := u.TaskLast.Add(time.Duration(*iv) * time.Second); now.Before(next) {
			return &LimitError{Key: MsgTaskInterval, Wait: next.Sub(now)}
		}
	}
	return nil
}
