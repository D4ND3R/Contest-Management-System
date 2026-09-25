package ranking

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
)

// Board is a scoreboard as the public (or the contestants) see it: the
// ranking after the contest's presentation settings — hidden users,
// anonymity, teams, subtasks, flags, institutions and the freeze.
type Board struct {
	Contest      string      `json:"contest"`
	Title        string      `json:"title"`
	ICPC         bool        `json:"icpc"`
	Precision    int         `json:"precision"`
	Start        time.Time   `json:"start"`
	Stop         time.Time   `json:"stop"`
	Timezone     string      `json:"timezone"`
	Frozen       bool        `json:"frozen"`
	FreezeAt     *time.Time  `json:"freeze_at,omitempty"`
	Teams        bool        `json:"teams"`
	Subtasks     bool        `json:"subtasks"`
	Flags        bool        `json:"flags"`
	Institutions bool        `json:"institutions"`
	Tasks        []BoardTask `json:"tasks"`
	Rows         []BoardRow  `json:"rows"`
	Generated    time.Time   `json:"generated"`
}

// BoardTask is a column of the scoreboard.
type BoardTask struct {
	Name       string    `json:"name"`
	Title      string    `json:"title"`
	MaxScore   float64   `json:"max_score"`
	SubtaskMax []float64 `json:"subtask_max,omitempty"`
	Precision  int       `json:"precision"`
}

// BoardRow is a participant (or a team) on the scoreboard. Key is stable
// across updates ("p<participation id>" or "t<team code>").
type BoardRow struct {
	Key         string      `json:"key"`
	Rank        int         `json:"rank"`
	Name        string      `json:"name"`
	Team        string      `json:"team,omitempty"`
	Flag        string      `json:"flag,omitempty"` // asset digest
	Institution string      `json:"institution,omitempty"`
	Total       float64     `json:"total"`
	Solved      int         `json:"solved,omitempty"`
	Penalty     int         `json:"penalty,omitempty"`
	Cells       []BoardCell `json:"cells"`
	// Members are the participations of a team row (for its history).
	Members []int64 `json:"-"`
}

// BoardCell is a row's result on a task.
type BoardCell struct {
	Score     float64   `json:"score"`
	Subtasks  []float64 `json:"subtasks,omitempty"`
	Submitted bool      `json:"submitted,omitempty"`
	Pending   int       `json:"pending,omitempty"`
	Solved    bool      `json:"solved,omitempty"`
	Attempts  int       `json:"attempts,omitempty"`
	Minute    int       `json:"minute,omitempty"`
}

// ParticipationKey is the board key of a participation.
func ParticipationKey(id int64) string { return "p" + strconv.FormatInt(id, 10) }

// BuildBoard applies c's presentation settings to r (computed with
// IncludeHidden = c.RankingShowHidden).
func BuildBoard(r *Ranking, c sqlc.Contest, now time.Time) *Board {
	b := &Board{Contest: c.Name, Title: c.Description, ICPC: r.ICPC, Precision: r.Precision, Start: c.StartTime, Stop: c.StopTime,
		Timezone: c.Timezone,
		FreezeAt: FreezeAt(c), Frozen: Frozen(c, now), Teams: c.TeamMode, Subtasks: c.RankingShowSubtasks,
		Flags: c.RankingShowFlags, Institutions: c.RankingShowInstitutions, Generated: r.Generated}
	if b.Title == "" {
		b.Title = c.Name
	}
	for _, t := range r.Tasks {
		bt := BoardTask{Name: t.Name, Title: t.Title, MaxScore: t.MaxScore, Precision: t.Precision}
		if b.Subtasks && len(t.SubtaskMax) > 1 {
			bt.SubtaskMax = t.SubtaskMax
		}
		b.Tasks = append(b.Tasks, bt)
	}
	// Anonymous labels follow the participation ids, not the ranking, so
	// they stay put as scores change.
	anon := map[int64]int{}
	if c.RankingAnonymous {
		ids := make([]int64, 0, len(r.Rows))
		for _, row := range r.Rows {
			ids = append(ids, row.ParticipationID)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for i, id := range ids {
			anon[id] = i + 1
		}
	}
	teams := map[string]int{}
	for _, row := range r.Rows {
		br := BoardRow{Key: ParticipationKey(row.ParticipationID), Name: displayName(row), Team: row.TeamCode,
			Total: row.Total, Solved: row.Solved, Penalty: row.Penalty, Members: []int64{row.ParticipationID}}
		if b.Flags {
			br.Flag = row.TeamFlag
		}
		if b.Institutions {
			br.Institution = row.Institution
			if br.Institution == "" {
				br.Institution = row.TeamInstitution
			}
		}
		if c.RankingAnonymous {
			br.Name, br.Team, br.Flag, br.Institution = fmt.Sprintf("#%d", anon[row.ParticipationID]), "", "", ""
		}
		for _, cell := range row.Cells {
			bc := BoardCell{Score: cell.Score, Submitted: cell.Submitted, Pending: cell.Pending,
				Subtasks: append([]float64(nil), cell.Subtasks...)}
			if r.ICPC {
				bc.Solved, bc.Attempts, bc.Minute = cell.Solved, cell.Attempts, cell.SolvedMinute
			}
			br.Cells = append(br.Cells, bc)
		}
		if b.Teams && row.TeamCode != "" && !c.RankingAnonymous {
			if i, ok := teams[row.TeamCode]; ok {
				mergeTeam(&b.Rows[i], br, r.ICPC, r.Tasks)
				continue
			}
			br.Key, br.Name = "t"+row.TeamCode, row.TeamName
			if br.Name == "" {
				br.Name = row.TeamCode
			}
			if b.Institutions {
				br.Institution = row.TeamInstitution
			}
			teams[row.TeamCode] = len(b.Rows)
		}
		b.Rows = append(b.Rows, br)
	}
	// Subtask scores are shown only when enabled and meaningful.
	for i := range b.Rows {
		for k := range b.Rows[i].Cells {
			if !b.Subtasks || len(r.Tasks[k].SubtaskMax) <= 1 {
				b.Rows[i].Cells[k].Subtasks = nil
			}
		}
	}
	if b.Teams {
		b.rerank(c)
	} else {
		ranks := map[string]int{}
		for _, row := range r.Rows {
			ranks[ParticipationKey(row.ParticipationID)] = row.Rank
		}
		for i := range b.Rows {
			b.Rows[i].Rank = ranks[b.Rows[i].Key]
		}
		b.SortRows()
	}
	return b
}

// SortRows orders rows by rank, then by name (the order clients keep when
// they apply deltas).
func (b *Board) SortRows() {
	sort.SliceStable(b.Rows, func(i, j int) bool {
		if b.Rows[i].Rank != b.Rows[j].Rank {
			return b.Rows[i].Rank < b.Rows[j].Rank
		}
		return b.Rows[i].Name < b.Rows[j].Name
	})
}

func displayName(row Row) string {
	name := row.FirstName
	if row.LastName != "" {
		if name != "" {
			name += " "
		}
		name += row.LastName
	}
	if name == "" {
		name = row.Username
	}
	return name
}

// mergeTeam adds a member's results to its team row: per task the best
// member score, or with "best per subtask" scoring the sum of the best
// member score of every subtask; in ICPC, the member who solved first.
func mergeTeam(t *BoardRow, m BoardRow, icpc bool, tasks []Task) {
	t.Members = append(t.Members, m.Members...)
	for i, mc := range m.Cells {
		tc := &t.Cells[i]
		tc.Submitted = tc.Submitted || mc.Submitted
		tc.Pending += mc.Pending
		if icpc {
			if mc.Solved && (!tc.Solved || mc.Minute < tc.Minute) {
				tc.Solved, tc.Minute, tc.Attempts = true, mc.Minute, mc.Attempts
			} else if !tc.Solved {
				tc.Attempts += mc.Attempts
			}
			continue
		}
		for k, v := range mc.Subtasks {
			if k < len(tc.Subtasks) {
				tc.Subtasks[k] = max(tc.Subtasks[k], v)
			} else {
				tc.Subtasks = append(tc.Subtasks, v)
			}
		}
		tc.Score = max(tc.Score, mc.Score)
		if tasks[i].scoreMode == "max_subtask" && len(tc.Subtasks) > 0 {
			sum := 0.0
			for _, v := range tc.Subtasks {
				sum += v
			}
			tc.Score = round(sum, tasks[i].Precision)
		}
	}
}

// rerank computes team totals and ranks.
func (b *Board) rerank(c sqlc.Contest) {
	for i := range b.Rows {
		row := &b.Rows[i]
		row.Total, row.Solved, row.Penalty = 0, 0, 0
		for _, cell := range row.Cells {
			row.Total += cell.Score
			if b.ICPC && cell.Solved {
				row.Solved++
				row.Penalty += cell.Minute + int(c.IcpcPenaltyMinutes)*cell.Attempts
			}
		}
		row.Total = round(row.Total, b.Precision)
	}
	cmp := func(x, y *BoardRow) int {
		if b.ICPC {
			if x.Solved != y.Solved {
				return y.Solved - x.Solved
			}
			return x.Penalty - y.Penalty
		}
		switch {
		case x.Total > y.Total+1e-9:
			return -1
		case y.Total > x.Total+1e-9:
			return 1
		}
		return 0
	}
	sort.SliceStable(b.Rows, func(i, j int) bool {
		if c := cmp(&b.Rows[i], &b.Rows[j]); c != 0 {
			return c < 0
		}
		return b.Rows[i].Name < b.Rows[j].Name
	})
	for i := range b.Rows {
		if i > 0 && cmp(&b.Rows[i-1], &b.Rows[i]) == 0 {
			b.Rows[i].Rank = b.Rows[i-1].Rank
		} else {
			b.Rows[i].Rank = i + 1
		}
	}
}

// Header is the board without its rows (to detect changes of settings,
// tasks or the freeze, which need a full refresh).
func (b *Board) Header() Board {
	h := *b
	h.Rows, h.Generated = nil, time.Time{}
	return h
}

// Diff returns the rows of next that differ from prev (new or changed) and
// the keys of prev's rows that are gone.
func Diff(prev, next *Board) (changed []BoardRow, removed []string) {
	old := map[string][]byte{}
	if prev != nil {
		for _, r := range prev.Rows {
			old[r.Key], _ = json.Marshal(r)
		}
	}
	seen := map[string]bool{}
	for _, r := range next.Rows {
		seen[r.Key] = true
		b, _ := json.Marshal(r)
		if string(old[r.Key]) != string(b) {
			changed = append(changed, r)
		}
	}
	for k := range old {
		if !seen[k] {
			removed = append(removed, k)
		}
	}
	sort.Strings(removed)
	return changed, removed
}

// Push is a message from the ranking pusher to a ranking web server.
type Push struct {
	Contest string `json:"contest"`
	// Kind: "full" (Board and History replace everything), "delta" (Rows
	// and Removed apply to sequence Base) or "delete".
	Kind    string             `json:"kind"`
	Seq     int64              `json:"seq"`
	Base    int64              `json:"base,omitempty"`
	Board   *Board             `json:"board,omitempty"`
	Rows    []BoardRow         `json:"rows,omitempty"`
	Removed []string           `json:"removed,omitempty"`
	History map[string][]Point `json:"history,omitempty"`
	// KeyHash (hex SHA-256) restricts the board to viewers with the key
	// (visibility "admins").
	KeyHash string `json:"key_hash,omitempty"`
}
