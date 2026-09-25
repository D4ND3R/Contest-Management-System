package ranking

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/scoring"
)

// Point is a moment of a participant's score history.
type Point struct {
	Time  time.Time
	Total float64 // score, or problems solved in ICPC mode
}

// MarshalJSON writes [unix milliseconds, total].
func (p Point) MarshalJSON() ([]byte, error) {
	return json.Marshal([2]float64{float64(p.Time.UnixMilli()), p.Total})
}

// UnmarshalJSON reads [unix milliseconds, total].
func (p *Point) UnmarshalJSON(b []byte) error {
	var v [2]float64
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	p.Time, p.Total = time.UnixMilli(int64(v[0])).UTC(), v[1]
	return nil
}

// FreezeAt is when the public ranking freezes: the last
// ranking_freeze_minutes of the contest, or the explicit freeze time.
func FreezeAt(c sqlc.Contest) *time.Time {
	if c.RankingFreezeMinutes > 0 {
		t := c.StopTime.Add(-time.Duration(c.RankingFreezeMinutes) * time.Minute)
		return &t
	}
	return c.RankingFreezeTime
}

// Frozen reports whether the public ranking of c is frozen at now.
func Frozen(c sqlc.Contest, now time.Time) bool {
	f := FreezeAt(c)
	return f != nil && !now.Before(*f) && !c.RankingUnfrozen
}

// Replay rebuilds the ranking from the submissions, counting only those
// made before cutoff (nil: all). Later submissions show as pending, as a
// frozen scoreboard does. It also returns every participant's score
// history up to cutoff.
func Replay(ctx context.Context, q *sqlc.Queries, contestID int64, cutoff *time.Time, opt Options) (*Ranking, map[int64][]Point, error) {
	b, err := newBuild(ctx, q, contestID, opt)
	if err != nil {
		return nil, nil, err
	}
	subs, err := q.ListContestSubmissionsForRanking(ctx, contestID)
	if err != nil {
		return nil, nil, err
	}
	type key struct{ pid, tid int64 }
	groups := map[key][]sqlc.ListContestSubmissionsForRankingRow{}
	var order []key
	for _, s := range subs {
		k := key{s.ParticipationID, s.TaskID}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], s)
	}
	// Manual adjustments (scores only; ICPC counts solved tasks) made
	// before the cutoff, in time order.
	adjusts := map[key][]sqlc.ListContestScoreAdjustmentsRow{}
	if !b.r.ICPC {
		adjs, err := q.ListContestScoreAdjustments(ctx, contestID)
		if err != nil {
			return nil, nil, err
		}
		for _, a := range adjs {
			if cutoff != nil && !a.CreatedAt.Before(*cutoff) {
				continue
			}
			k := key{a.ParticipationID, a.TaskID}
			if _, ok := groups[k]; !ok {
				if _, seen := adjusts[k]; !seen {
					order = append(order, k)
				}
			}
			adjusts[k] = append(adjusts[k], a)
		}
	}
	type change struct {
		t     time.Time
		tid   int64
		value float64
	}
	changes := map[int64][]change{}
	for _, k := range order {
		ri, ok := b.rowIdx[k.pid]
		ti, ok2 := b.taskIdx[k.tid]
		if !ok || !ok2 {
			continue
		}
		task := b.r.Tasks[ti]
		var prefix []scoring.Submission
		after := 0
		adjs, ai := adjusts[k], 0
		adjusted, lastScore := 0.0, 0.0
		// adjustUntil applies the adjustments made before t, each a point
		// of the history.
		adjustUntil := func(t *time.Time) {
			for ai < len(adjs) && (t == nil || adjs[ai].CreatedAt.Before(*t)) {
				adjusted += adjs[ai].Points
				changes[k.pid] = append(changes[k.pid], change{adjs[ai].CreatedAt, k.tid, lastScore + adjusted})
				ai++
			}
		}
		for _, s := range groups[k] {
			if cutoff != nil && !s.SubmittedAt.Before(*cutoff) {
				after++
				continue
			}
			adjustUntil(&s.SubmittedAt)
			ss := scoring.Submission{ID: s.ID, Time: s.SubmittedAt, Official: true, Tokened: s.Tokened, Scored: s.ScoredAt != nil,
				CompileError: s.CompilationOutcome != nil && *s.CompilationOutcome == "fail"}
			if s.Score != nil {
				ss.Score = *s.Score
			}
			if len(s.RankingScoreDetails) > 0 {
				_ = json.Unmarshal(s.RankingScoreDetails, &ss.Subtasks)
			}
			prefix = append(prefix, ss)
			if ss.Scored {
				lastScore = scoring.Aggregate(task.scoreMode, prefix, task.Precision).Score
				v := lastScore + adjusted
				if b.r.ICPC {
					v = 0
					if scoring.ICPC(prefix, task.MaxScore).Solved {
						v = 1
					}
				}
				changes[k.pid] = append(changes[k.pid], change{s.SubmittedAt, k.tid, v})
			}
		}
		adjustUntil(nil)
		ts := scoring.Aggregate(task.scoreMode, prefix, task.Precision)
		icpc := scoring.ICPC(prefix, task.MaxScore)
		cell := Cell{Score: ts.Score + adjusted, Subtasks: ts.Subtasks, Submitted: len(prefix)+after > 0 || adjusted != 0,
			Pending: ts.Pending + after, Solved: icpc.Solved, Attempts: icpc.Attempts, SolvedAt: icpc.SolvedAt, Adjustment: adjusted}
		if cell.Solved {
			cell.SolvedMinute = b.solvedMinute(k.pid, cell.SolvedAt)
		}
		b.r.Rows[ri].Cells[ti] = cell
	}
	history := map[int64][]Point{}
	for pid, cs := range changes {
		sort.SliceStable(cs, func(i, j int) bool { return cs[i].t.Before(cs[j].t) })
		cur := map[int64]float64{}
		var pts []Point
		for _, c := range cs {
			cur[c.tid] = c.value
			total := 0.0
			for _, v := range cur {
				total += v
			}
			total = round(total, b.r.Precision)
			if n := len(pts); n > 0 && pts[n-1].Total == total {
				continue
			}
			pts = append(pts, Point{c.t.UTC(), total})
		}
		history[pid] = pts
	}
	return b.finish(), history, nil
}
