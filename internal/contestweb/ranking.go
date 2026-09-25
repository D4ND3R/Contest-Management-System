package contestweb

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/ranking"
)

// boardCache keeps each contest's board briefly, so contestants opening
// the ranking cost at most one computation per contest every few seconds.
type boardCache struct {
	mu      sync.Mutex
	entries map[int64]boardEntry
}

type boardEntry struct {
	b  *ranking.Board
	at time.Time
}

// rankingVisible reports whether contestants may see the ranking of c.
func rankingVisible(c sqlc.Contest, now time.Time) bool {
	switch c.RankingVisibility {
	case "public", "contestants":
	default:
		return false
	}
	return c.RankingContestantView != "none" && (c.RankingWhen != "after" || !now.Before(c.StopTime))
}

func (s *Server) contestBoard(ctx context.Context, cv *contestView, now time.Time) (*ranking.Board, error) {
	c := cv.Contest
	frozen := ranking.Frozen(c, now)
	ttl := 2 * time.Second
	if frozen {
		ttl = 5 * time.Second
	}
	s.boards.mu.Lock()
	defer s.boards.mu.Unlock()
	if e, ok := s.boards.entries[c.ID]; ok && now.Sub(e.at) < ttl && e.b.Frozen == frozen {
		return e.b, nil
	}
	opt := ranking.Options{IncludeHidden: c.RankingShowHidden}
	var r *ranking.Ranking
	var err error
	if frozen {
		r, _, err = ranking.Replay(ctx, s.q, c.ID, ranking.FreezeAt(c), opt)
	} else {
		r, err = ranking.Compute(ctx, s.q, c.ID, opt)
	}
	if err != nil {
		return nil, err
	}
	b := ranking.BuildBoard(r, c, now)
	if s.boards.entries == nil {
		s.boards.entries = map[int64]boardEntry{}
	}
	s.boards.entries[c.ID] = boardEntry{b: b, at: now}
	return b, nil
}

// rankingData is the contestant's ranking page.
type rankingData struct {
	Board *ranking.Board
	Rows  []ranking.BoardRow
	Own   bool
	Mine  string // the key of the contestant's row
	Rank  int
	Count int
}

func (s *Server) handleRanking(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	if !rankingVisible(rc.contest.Contest, rc.now) {
		http.NotFound(w, r)
		return
	}
	b, err := s.contestBoard(r.Context(), rc.contest, rc.now)
	if err != nil {
		s.fail(w, err)
		return
	}
	d := &rankingData{Board: b, Own: rc.contest.RankingContestantView == "own", Mine: ranking.ParticipationKey(rc.part.ID), Count: len(b.Rows)}
	for _, row := range b.Rows {
		mine := false
		for _, m := range row.Members {
			mine = mine || m == rc.part.ID
		}
		if mine {
			d.Mine, d.Rank = row.Key, row.Rank
		}
		if !d.Own || mine {
			d.Rows = append(d.Rows, row)
		}
	}
	p := s.newPage(rc, "", "ranking")
	p.Title, p.Data = p.T("Ranking"), d
	if r.URL.Query().Get("fragment") != "" {
		s.renderPartial(w, "ranking-table", p)
		return
	}
	s.render(w, "ranking", http.StatusOK, p)
}
