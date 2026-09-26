package adminweb

import (
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/scoring"
)

// Dataset comparison (SPEC_IOI §6): a candidate dataset judged in the
// background (autojudge) is compared with the live one before switching:
// which task scores, and which submissions, would change.

type datasetComparePage struct {
	Task     sqlc.Task
	Datasets []sqlc.Dataset
	A, B     sqlc.Dataset
	// People whose task score differs, largest change first.
	People []datasetCompareRow
	// Subs are the submissions whose score differs.
	Subs []datasetCompareSub
	// Contestants and Submissions count everything compared; PendingB
	// the submissions not judged yet on B (the comparison is partial).
	Contestants, Submissions, PendingB int
}

type datasetCompareRow struct {
	Username      string
	Participation int64
	A, B, Delta   float64
}

type datasetCompareSub struct {
	ID       int64
	Username string
	At       time.Time
	A, B     *float64
}

func sameScore(a, b *float64) bool {
	switch {
	case a == nil || b == nil:
		return a == nil && b == nil
	}
	return math.Abs(*a-*b) < 1e-9
}

func compareSubmission(id int64, at time.Time, official, tokened bool, compilation *string, score *float64,
	details json.RawMessage, scoredAt *time.Time) scoring.Submission {
	s := scoring.Submission{ID: id, Time: at, Official: official, Tokened: tokened, Scored: scoredAt != nil,
		CompileError: compilation != nil && *compilation == "fail"}
	if score != nil {
		s.Score = *score
	}
	if len(details) > 0 {
		_ = json.Unmarshal(details, &s.Subtasks)
	}
	return s
}

func (s *Server) handleDatasetCompare(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	t, ok := s.loadTask(w, r, rc)
	if !ok {
		return
	}
	ctx := r.Context()
	d := &datasetComparePage{Task: t}
	var err error
	if d.Datasets, err = s.q.ListDatasetsByTask(ctx, t.ID); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	pick := func(key string, fallback func(sqlc.Dataset) bool) (sqlc.Dataset, bool) {
		id, _ := strconv.ParseInt(r.URL.Query().Get(key), 10, 64)
		for _, ds := range d.Datasets {
			if ds.ID == id {
				return ds, true
			}
		}
		for _, ds := range d.Datasets {
			if fallback(ds) {
				return ds, true
			}
		}
		return sqlc.Dataset{}, false
	}
	live := func(ds sqlc.Dataset) bool { return t.ActiveDatasetID != nil && ds.ID == *t.ActiveDatasetID }
	var okA, okB bool
	d.A, okA = pick("a", live)
	d.B, okB = pick("b", func(ds sqlc.Dataset) bool { return ds.ID != d.A.ID })
	if !okA || !okB || d.A.ID == d.B.ID {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Choose two different datasets of the task.")
		return
	}
	rows, err := s.q.CompareDatasetSubmissions(ctx, sqlc.CompareDatasetSubmissionsParams{TaskID: t.ID, DatasetA: d.A.ID, DatasetB: d.B.ID})
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	type person struct {
		name string
		a, b []scoring.Submission
	}
	people := map[int64]*person{}
	var order []int64
	for _, x := range rows {
		if x.ParticipationID == nil {
			continue
		}
		p := people[*x.ParticipationID]
		if p == nil {
			p = &person{name: x.Username}
			people[*x.ParticipationID] = p
			order = append(order, *x.ParticipationID)
		}
		p.a = append(p.a, compareSubmission(x.ID, x.SubmittedAt, x.Official, x.Tokened, x.CompilationA, x.ScoreA, x.DetailsA, x.ScoredAtA))
		p.b = append(p.b, compareSubmission(x.ID, x.SubmittedAt, x.Official, x.Tokened, x.CompilationB, x.ScoreB, x.DetailsB, x.ScoredAtB))
		d.Submissions++
		if x.ScoredAtB == nil {
			d.PendingB++
		}
		sa, sb := x.ScoreA, x.ScoreB
		if x.ScoredAtA == nil {
			sa = nil
		}
		if x.ScoredAtB == nil {
			sb = nil
		}
		if !sameScore(sa, sb) {
			d.Subs = append(d.Subs, datasetCompareSub{ID: x.ID, Username: x.Username, At: x.SubmittedAt, A: sa, B: sb})
		}
	}
	prec := int(t.ScorePrecision)
	for _, id := range order {
		p := people[id]
		a := scoring.Aggregate(t.ScoreMode, p.a, prec).Score
		b := scoring.Aggregate(t.ScoreMode, p.b, prec).Score
		d.Contestants++
		if math.Abs(a-b) >= 1e-9 {
			d.People = append(d.People, datasetCompareRow{Username: p.name, Participation: id, A: a, B: b, Delta: b - a})
		}
	}
	sort.SliceStable(d.People, func(i, j int) bool { return math.Abs(d.People[i].Delta) > math.Abs(d.People[j].Delta) })
	s.render(w, "dataset_compare", http.StatusOK, s.newPage(w, r, rc, "Compare datasets", "tasks", d).crumb("Tasks", "/tasks").
		crumb(t.Name, "/tasks/"+strconv.FormatInt(t.ID, 10)))
}
