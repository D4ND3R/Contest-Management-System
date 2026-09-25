// Package ranking computes contest rankings from the per-task aggregates
// maintained by the dispatcher (participation_task_scores). It is used by
// the admin exports and by the ranking pushers that feed the public
// scoreboards.
package ranking

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/scoring"
)

// Task is a ranked task with its maximum score (from the live dataset).
type Task struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Title       string    `json:"title"`
	MaxScore    float64   `json:"max_score"`
	SubtaskMax  []float64 `json:"subtask_max,omitempty"`
	Precision   int       `json:"precision"`
	DatasetID   int64     `json:"dataset_id,omitempty"`
	ScoreType   string    `json:"-"`
	NumTestcase int       `json:"-"`
	scoreMode   string
}

// Cell is a participation's result on a task.
type Cell struct {
	Score     float64    `json:"score"`
	Subtasks  []float64  `json:"subtasks,omitempty"`
	Submitted bool       `json:"submitted"`
	Pending   int        `json:"pending,omitempty"`
	Solved    bool       `json:"solved,omitempty"`
	Attempts  int        `json:"attempts,omitempty"`
	SolvedAt  *time.Time `json:"solved_at,omitempty"`
	// SolvedMinute is the contest minute of the accepted submission (ICPC).
	SolvedMinute int `json:"solved_minute,omitempty"`
	// Adjustment is the manual adjustment included in Score.
	Adjustment float64 `json:"adjustment,omitempty"`
}

// Row is one participation.
type Row struct {
	Rank            int     `json:"rank"`
	ParticipationID int64   `json:"participation_id"`
	UserID          int64   `json:"user_id"`
	Username        string  `json:"username"`
	FirstName       string  `json:"first_name"`
	LastName        string  `json:"last_name"`
	TeamCode        string  `json:"team,omitempty"`
	TeamName        string  `json:"team_name,omitempty"`
	TeamFlag        string  `json:"team_flag,omitempty"` // blob digest
	TeamInstitution string  `json:"team_institution,omitempty"`
	Institution     string  `json:"institution,omitempty"`
	Country         string  `json:"country,omitempty"`
	Site            string  `json:"site,omitempty"`
	Hidden          bool    `json:"hidden,omitempty"`
	Unrestricted    bool    `json:"unrestricted,omitempty"`
	Cells           []Cell  `json:"tasks"`
	Total           float64 `json:"total"`
	Solved          int     `json:"solved,omitempty"`
	Penalty         int     `json:"penalty,omitempty"`
}

// Ranking of a contest.
type Ranking struct {
	ContestID int64     `json:"contest_id"`
	Contest   string    `json:"contest"`
	ICPC      bool      `json:"icpc"`
	Precision int       `json:"precision"`
	Tasks     []Task    `json:"tasks"`
	Rows      []Row     `json:"rows"`
	Generated time.Time `json:"generated"`
}

// Options select what Compute includes.
type Options struct {
	IncludeHidden bool
	// SiteID restricts the ranking to one site (0 = every participant).
	SiteID int64
}

// LoadTasks returns the tasks of a contest in order with their maximum
// scores computed from the live datasets.
func LoadTasks(ctx context.Context, q *sqlc.Queries, contestID int64) ([]Task, error) {
	tasks, err := q.ListTasksByContest(ctx, &contestID)
	if err != nil {
		return nil, err
	}
	datasets, err := q.ListLiveDatasetsByContest(ctx, &contestID)
	if err != nil {
		return nil, err
	}
	dsByTask := map[int64]sqlc.Dataset{}
	ids := make([]int64, 0, len(datasets))
	for _, d := range datasets {
		dsByTask[d.TaskID] = d
		ids = append(ids, d.ID)
	}
	tcs, err := q.ListTestcasesByDatasets(ctx, ids)
	if err != nil {
		return nil, err
	}
	type tcset struct {
		codes []string
		pub   []bool
	}
	byDS := map[int64]*tcset{}
	for _, tc := range tcs {
		s := byDS[tc.DatasetID]
		if s == nil {
			s = &tcset{}
			byDS[tc.DatasetID] = s
		}
		s.codes = append(s.codes, tc.Codename)
		s.pub = append(s.pub, tc.Public)
	}
	out := make([]Task, 0, len(tasks))
	for _, t := range tasks {
		rt := Task{ID: t.ID, Name: t.Name, Title: t.Title, Precision: int(t.ScorePrecision), scoreMode: t.ScoreMode}
		if d, ok := dsByTask[t.ID]; ok {
			rt.DatasetID, rt.ScoreType = d.ID, d.ScoreType
			s := byDS[d.ID]
			if s == nil {
				s = &tcset{}
			}
			rt.NumTestcase = len(s.codes)
			if st, err := scoring.New(d.ScoreType, d.ScoreTypeParams, s.codes, s.pub, rt.Precision); err == nil {
				rt.MaxScore, rt.SubtaskMax = st.MaxScore(), st.SubtaskMaxScores()
			}
		}
		out = append(out, rt)
	}
	return out, nil
}

// build is a ranking being assembled: rows without results yet.
type build struct {
	c       sqlc.Contest
	r       *Ranking
	rowIdx  map[int64]int
	taskIdx map[int64]int
	starts  map[int64]time.Time
}

// newBuild loads the contest, its tasks and the participations to rank.
func newBuild(ctx context.Context, q *sqlc.Queries, contestID int64, opt Options) (*build, error) {
	c, err := q.GetContest(ctx, contestID)
	if err != nil {
		return nil, err
	}
	tasks, err := LoadTasks(ctx, q, contestID)
	if err != nil {
		return nil, err
	}
	parts, err := q.ListParticipationsByContest(ctx, contestID)
	if err != nil {
		return nil, err
	}
	b := &build{c: c, rowIdx: map[int64]int{}, taskIdx: map[int64]int{}, starts: map[int64]time.Time{},
		r: &Ranking{ContestID: c.ID, Contest: c.Name, ICPC: c.ScoringMode == "icpc", Precision: int(c.ScorePrecision),
			Tasks: tasks, Generated: time.Now().UTC()}}
	for i, t := range tasks {
		b.taskIdx[t.ID] = i
	}
	for _, p := range parts {
		if p.Participation.Hidden && !opt.IncludeHidden || !p.Participation.Approved {
			// Registrations waiting for approval are not contestants yet.
			continue
		}
		if opt.SiteID != 0 && (p.Participation.SiteID == nil || *p.Participation.SiteID != opt.SiteID) {
			continue
		}
		row := Row{ParticipationID: p.Participation.ID, UserID: p.Participation.UserID, Username: p.Username,
			FirstName: p.FirstName, LastName: p.LastName, Hidden: p.Participation.Hidden,
			Institution: p.Institution, Country: p.Country, Site: derefStr(p.SiteName),
			Unrestricted: p.Participation.Unrestricted, Cells: make([]Cell, len(tasks)),
			TeamCode: derefStr(p.TeamCode), TeamName: derefStr(p.TeamName), TeamFlag: derefStr(p.TeamFlag),
			TeamInstitution: derefStr(p.TeamInstitution)}
		b.rowIdx[row.ParticipationID] = len(b.r.Rows)
		b.r.Rows = append(b.r.Rows, row)
	}
	for _, p := range parts {
		start := c.StartTime
		if p.SiteStartTime != nil {
			start = *p.SiteStartTime
		}
		start = start.Add(time.Duration(p.Participation.DelayTimeS) * time.Second)
		if c.PerUserTimeS != nil && p.Participation.StartingTime != nil {
			start = *p.Participation.StartingTime
		}
		b.starts[p.Participation.ID] = start
	}
	return b, nil
}

// finish computes totals and ranks.
func (b *build) finish() *Ranking {
	r := b.r
	for i := range r.Rows {
		row := &r.Rows[i]
		row.Total, row.Solved, row.Penalty = 0, 0, 0
		for _, cell := range row.Cells {
			row.Total += cell.Score
			if r.ICPC && cell.Solved {
				row.Solved++
				row.Penalty += cell.SolvedMinute + int(b.c.IcpcPenaltyMinutes)*cell.Attempts
			}
		}
		row.Total = round(row.Total, r.Precision)
	}
	r.sort()
	return r
}

func (b *build) solvedMinute(pid int64, at *time.Time) int {
	if at == nil {
		return 0
	}
	return max(0, int(at.Sub(b.starts[pid])/time.Minute))
}

// Compute builds the (unfrozen) ranking of a contest from the per-task
// aggregates the dispatcher maintains.
func Compute(ctx context.Context, q *sqlc.Queries, contestID int64, opt Options) (*Ranking, error) {
	b, err := newBuild(ctx, q, contestID, opt)
	if err != nil {
		return nil, err
	}
	scores, err := q.ListParticipationTaskScoresByContest(ctx, contestID)
	if err != nil {
		return nil, err
	}
	for _, s := range scores {
		ri, ok := b.rowIdx[s.ParticipationID]
		ti, ok2 := b.taskIdx[s.TaskID]
		if !ok || !ok2 {
			continue
		}
		cell := Cell{Score: s.Score, Submitted: s.LastSubmissionAt != nil || s.Pending > 0, Pending: int(s.Pending),
			Solved: s.IcpcSolved, Attempts: int(s.IcpcAttempts), SolvedAt: s.IcpcSolvedAt, Adjustment: s.Adjustment}
		_ = json.Unmarshal(s.SubtaskScores, &cell.Subtasks)
		if cell.Solved {
			cell.SolvedMinute = b.solvedMinute(s.ParticipationID, cell.SolvedAt)
		}
		b.r.Rows[ri].Cells[ti] = cell
	}
	return b.finish(), nil
}

func round(v float64, precision int) float64 {
	p := math.Pow(10, float64(precision))
	return math.Round(v*p) / p
}

// less orders rows: IOI by total score; ICPC by problems solved, then
// penalty. Equal rows share a rank and are listed by username.
func (r *Ranking) cmp(a, b *Row) int {
	if r.ICPC {
		if a.Solved != b.Solved {
			return b.Solved - a.Solved
		}
		return a.Penalty - b.Penalty
	}
	switch {
	case a.Total > b.Total+1e-9:
		return -1
	case b.Total > a.Total+1e-9:
		return 1
	}
	return 0
}

func (r *Ranking) sort() {
	sort.SliceStable(r.Rows, func(i, j int) bool {
		if c := r.cmp(&r.Rows[i], &r.Rows[j]); c != 0 {
			return c < 0
		}
		return r.Rows[i].Username < r.Rows[j].Username
	})
	for i := range r.Rows {
		if i > 0 && r.cmp(&r.Rows[i-1], &r.Rows[i]) == 0 {
			r.Rows[i].Rank = r.Rows[i-1].Rank
		} else {
			r.Rows[i].Rank = i + 1
		}
	}
}

// WriteJSON writes the ranking as JSON.
func (r *Ranking) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", " ")
	return enc.Encode(r)
}

// WriteCSV writes one line per participation: rank, user, names, team,
// one column per task and the total (plus solved/penalty in ICPC mode).
func (r *Ranking) WriteCSV(w io.Writer) error {
	cw := csv.NewWriter(w)
	head := []string{"rank", "username", "first_name", "last_name", "team"}
	for _, t := range r.Tasks {
		head = append(head, t.Name)
	}
	head = append(head, "total")
	if r.ICPC {
		head = append(head, "solved", "penalty")
	}
	if err := cw.Write(head); err != nil {
		return err
	}
	for _, row := range r.Rows {
		rec := []string{strconv.Itoa(row.Rank), row.Username, row.FirstName, row.LastName, row.TeamCode}
		for i, cell := range row.Cells {
			rec = append(rec, formatScore(cell.Score, r.Tasks[i].Precision))
		}
		rec = append(rec, formatScore(row.Total, r.Precision))
		if r.ICPC {
			rec = append(rec, strconv.Itoa(row.Solved), strconv.Itoa(row.Penalty))
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func formatScore(v float64, precision int) string {
	return strconv.FormatFloat(round(v, precision), 'f', -1, 64)
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
