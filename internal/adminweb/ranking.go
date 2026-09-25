package adminweb

import (
	"net/http"
	"strconv"

	"github.com/D4ND3R/Contest-Management-System/internal/ranking"
)

func (s *Server) handleRanking(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	c, ok := s.loadContest(w, r, rc)
	if !ok {
		return
	}
	rk, err := ranking.Compute(r.Context(), s.q, c.ID, ranking.Options{IncludeHidden: r.URL.Query().Get("hidden") == "1"})
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	s.render(w, "ranking", http.StatusOK, s.newPage(w, r, rc, "Ranking", "contests", rk).
		crumb("Contests", "/contests").crumb(c.Name, "/contests/"+strconv.FormatInt(c.ID, 10)))
}

func (s *Server) exportRanking(w http.ResponseWriter, r *http.Request, rc *reqCtx, csv bool) {
	c, ok := s.loadContest(w, r, rc)
	if !ok {
		return
	}
	rk, err := ranking.Compute(r.Context(), s.q, c.ID, ranking.Options{IncludeHidden: r.URL.Query().Get("hidden") == "1"})
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	ext, ct := ".json", "application/json"
	if csv {
		ext, ct = ".csv", "text/csv; charset=utf-8"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", `attachment; filename="ranking-`+c.Name+ext+`"`)
	w.Header().Set("Cache-Control", "no-store")
	if csv {
		err = rk.WriteCSV(w)
	} else {
		err = rk.WriteJSON(w)
	}
	if err != nil {
		s.log.Warn("ranking export", "error", err)
	}
}

func (s *Server) handleRankingCSV(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.exportRanking(w, r, rc, true)
}

func (s *Server) handleRankingJSON(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.exportRanking(w, r, rc, false)
}

// taskStats summarises one task.
type taskStats struct {
	Task                                         ranking.Task
	Submissions, Participants, CompileFailed     int64
	Pending, Errors                              int64
	Attempted, Full, Partial, Zero, NotAttempted int
	Average                                      float64
	Histogram                                    []histBucket
	Verdicts                                     []verdictRow
	VerdictTotal                                 int64
}

type histBucket struct {
	Label string
	Count int
	Pct   int
}

type verdictRow struct {
	Verdict          string
	N                int64
	AvgTime, MaxTime float64
	MaxMemory        int64
}

type statsPage struct {
	Contest      string
	ContestID    int64
	Participants int
	Tasks        []*taskStats
}

// handleStats shows per-task statistics: submission counters, the score
// distribution of the (non-hidden) contestants and testcase verdicts.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	c, ok := s.loadContest(w, r, rc)
	if !ok {
		return
	}
	rk, err := ranking.Compute(r.Context(), s.q, c.ID, ranking.Options{})
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	counters, err := s.q.AdminTaskSubmissionStats(r.Context(), &c.ID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	verdicts, err := s.q.AdminTaskVerdictStats(r.Context(), &c.ID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	d := &statsPage{Contest: c.Name, ContestID: c.ID, Participants: len(rk.Rows)}
	byID := map[int64]*taskStats{}
	for i, t := range rk.Tasks {
		ts := &taskStats{Task: t, Histogram: make([]histBucket, 10)}
		for b := range ts.Histogram {
			ts.Histogram[b].Label = strconv.Itoa(b*10) + "–" + strconv.Itoa((b+1)*10) + "%"
		}
		var sum float64
		for _, row := range rk.Rows {
			cell := row.Cells[i]
			if !cell.Submitted {
				ts.NotAttempted++
				continue
			}
			ts.Attempted++
			sum += cell.Score
			switch {
			case t.MaxScore > 0 && cell.Score >= t.MaxScore-1e-9:
				ts.Full++
			case cell.Score > 0:
				ts.Partial++
			default:
				ts.Zero++
			}
			if t.MaxScore > 0 {
				b := int(10 * cell.Score / t.MaxScore)
				ts.Histogram[min(max(b, 0), 9)].Count++
			}
		}
		if ts.Attempted > 0 {
			ts.Average = sum / float64(ts.Attempted)
			for b := range ts.Histogram {
				ts.Histogram[b].Pct = 100 * ts.Histogram[b].Count / ts.Attempted
			}
		}
		byID[t.ID] = ts
		d.Tasks = append(d.Tasks, ts)
	}
	for _, cn := range counters {
		if ts := byID[cn.TaskID]; ts != nil {
			ts.Submissions, ts.Participants, ts.CompileFailed = cn.Submissions, cn.Participants, cn.CompileFailed
			ts.Pending, ts.Errors = cn.Pending, cn.Errors
		}
	}
	for _, v := range verdicts {
		if ts := byID[v.TaskID]; ts != nil {
			ts.Verdicts = append(ts.Verdicts, verdictRow{v.Verdict, v.N, v.AvgTime, v.MaxTime, v.MaxMemory})
			ts.VerdictTotal += v.N
		}
	}
	s.render(w, "stats", http.StatusOK, s.newPage(w, r, rc, "Statistics", "contests", d).
		crumb("Contests", "/contests").crumb(c.Name, "/contests/"+strconv.FormatInt(c.ID, 10)))
}
