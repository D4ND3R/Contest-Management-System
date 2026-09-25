package contestweb

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/certificate"
	"github.com/D4ND3R/Contest-Management-System/internal/contest"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/ranking"
	"github.com/jackc/pgx/v5"
)

// certRankingTTL is how long the ranking behind contestants' certificates
// is reused: after the ceremony everybody downloads at once.
const certRankingTTL = 30 * time.Second

type certRanking struct {
	mu sync.Mutex
	m  map[int64]certRankingEntry
}

type certRankingEntry struct {
	rk *ranking.Ranking
	at time.Time
}

func (c *certRanking) get(ctx context.Context, q *sqlc.Queries, contestID int64, now time.Time) (*ranking.Ranking, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.m[contestID]; ok && now.Sub(e.at) < certRankingTTL {
		return e.rk, nil
	}
	rk, err := ranking.Compute(ctx, q, contestID, ranking.Options{})
	if err != nil {
		return nil, err
	}
	if c.m == nil {
		c.m = map[int64]certRankingEntry{}
	}
	c.m[contestID] = certRankingEntry{rk: rk, at: now}
	return rk, nil
}

// certificateTemplate returns the contest's template when contestants may
// download their certificate now.
func (s *Server) certificateTemplate(ctx context.Context, rc *reqCtx) (*sqlc.CertificateTemplate, error) {
	switch rc.status.Phase {
	case contest.Finished, contest.Analysis, contest.Practice:
	default:
		return nil, nil
	}
	if rc.part.Hidden {
		return nil, nil
	}
	t, err := s.q.GetCertificateTemplate(ctx, rc.contest.ID)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !t.ContestantsCanDownload {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// handleCertificate is the contestant's own certificate.
func (s *Server) handleCertificate(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	t, err := s.certificateTemplate(r.Context(), rc)
	if err != nil {
		s.fail(w, err)
		return
	}
	if t == nil {
		s.errorPage(w, r, rc.contest, http.StatusNotFound, "Not found", "There is no certificate for you in this contest.")
		return
	}
	rk, err := s.certRanks.get(r.Context(), s.q, rc.contest.ID, rc.now)
	if err != nil {
		s.fail(w, err)
		return
	}
	doc, n := certificate.Build(r.Context(), s.blobs, rc.contest.Contest, *t, rk, certificate.Options{ParticipationID: rc.part.ID})
	if n == 0 {
		s.errorPage(w, r, rc.contest, http.StatusNotFound, "Not found", "There is no certificate for you in this contest.")
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="`+rc.contest.Name+`-certificate.pdf"`)
	w.Header().Set("Cache-Control", "private, no-store")
	doc.Write(w)
}
