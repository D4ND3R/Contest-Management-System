package adminweb

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/ranking"
	"github.com/D4ND3R/Contest-Management-System/internal/webkit"
)

// Balloons (X11): every task solved by a team (or contestant in individual
// contests) is a balloon for the staff to deliver. The list comes from the
// ICPC solves of participation_task_scores; only deliveries are stored.

// balloon is one solved task of one recipient.
type balloon struct {
	TaskID    int64
	Task      string
	Recipient string // ranking key of the team or participation
	Who       string
	Solver    string // the member who solved it (team contests)
	Site      string
	SolvedAt  time.Time
	// First solve of the task in the contest.
	First       bool
	DeliveredAt *time.Time
	DeliveredBy string
}

type balloonsData struct {
	C                  sqlc.Contest
	Sites              []sqlc.Site
	Site               string
	Pending, Delivered []balloon
}

// Query is the list's query string (site filter).
func (d *balloonsData) Query() string {
	if d.Site == "" {
		return ""
	}
	return "site=" + urlQueryEscape(d.Site)
}

func (s *Server) balloons(r *http.Request, c sqlc.Contest, site string) (*balloonsData, error) {
	ctx := r.Context()
	d := &balloonsData{C: c, Site: site}
	var err error
	if d.Sites, err = s.q.ListSites(ctx, c.ID); err != nil {
		return nil, err
	}
	tasks, err := s.q.ListTasksByContest(ctx, &c.ID)
	if err != nil {
		return nil, err
	}
	names := make(map[int64]string, len(tasks))
	for _, t := range tasks {
		names[t.ID] = t.Name
	}
	solves, err := s.q.ListContestSolves(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	deliveries, err := s.q.ListBalloonDeliveries(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	type key struct {
		task      int64
		recipient string
	}
	delivered := make(map[key]sqlc.ListBalloonDeliveriesRow, len(deliveries))
	for _, b := range deliveries {
		delivered[key{b.TaskID, b.Recipient}] = b
	}
	seen := map[key]bool{}
	firstOfTask := map[int64]bool{}
	// Solves come first-solved first: the first of a recipient is its
	// balloon, the first of a task is the first solve.
	for _, sv := range solves {
		name, ok := names[sv.TaskID]
		if !ok || sv.IcpcSolvedAt == nil {
			continue
		}
		b := balloon{TaskID: sv.TaskID, Task: name, SolvedAt: *sv.IcpcSolvedAt, Site: derefStr(sv.SiteName),
			Recipient: ranking.ParticipationKey(sv.ParticipationID), Who: strings.TrimSpace(sv.FirstName + " " + sv.LastName)}
		if b.Who == "" {
			b.Who = sv.Username
		}
		if c.TeamMode && sv.TeamCode != nil {
			b.Recipient, b.Who, b.Solver = "t"+*sv.TeamCode, *sv.TeamCode, sv.Username
			if sv.TeamName != nil && *sv.TeamName != "" {
				b.Who += " — " + *sv.TeamName
			}
		}
		k := key{b.TaskID, b.Recipient}
		if seen[k] {
			continue
		}
		seen[k] = true
		if !firstOfTask[b.TaskID] {
			firstOfTask[b.TaskID], b.First = true, true
		}
		if site != "" && b.Site != site {
			continue
		}
		if dv, ok := delivered[k]; ok {
			b.DeliveredAt, b.DeliveredBy = &dv.DeliveredAt, dv.DeliveredBy
			d.Delivered = append(d.Delivered, b)
		} else {
			d.Pending = append(d.Pending, b)
		}
	}
	// Most recent deliveries first.
	sort.SliceStable(d.Delivered, func(i, j int) bool { return d.Delivered[i].DeliveredAt.After(*d.Delivered[j].DeliveredAt) })
	return d, nil
}

func (s *Server) handleBalloons(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	c, ok := s.loadContest(w, r, rc)
	if !ok {
		return
	}
	d, err := s.balloons(r, c, r.URL.Query().Get("site"))
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	p := s.newPage(w, r, rc, "Balloons", "contests", d).
		crumb("Contests", "/contests").crumb(c.Name, "/contests/"+strconv.FormatInt(c.ID, 10))
	if r.URL.Query().Get("fragment") != "" {
		w.Header().Set("Cache-Control", "no-store")
		s.renderPartial(w, "balloon-list", wrap{P: p, V: d})
		return
	}
	s.render(w, "balloons", http.StatusOK, p)
}

// handleBalloonDeliver marks a balloon delivered (or, with undo, not yet).
func (s *Server) handleBalloonDeliver(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	c, ok := s.loadContest(w, r, rc)
	if !ok {
		return
	}
	taskID, err := strconv.ParseInt(r.FormValue("task_id"), 10, 64)
	recipient := r.FormValue("recipient")
	if err != nil || recipient == "" || len(recipient) > 200 {
		s.errorPage(w, r, rc, http.StatusBadRequest, "Invalid request.")
		return
	}
	if t, err := s.q.GetTask(r.Context(), taskID); err != nil || t.ContestID == nil || *t.ContestID != c.ID {
		s.notFound(w, r, rc)
		return
	}
	if r.FormValue("undo") != "" {
		err = s.q.UndeliverBalloon(r.Context(), sqlc.UndeliverBalloonParams{TaskID: taskID, Recipient: recipient})
	} else {
		err = s.q.DeliverBalloon(r.Context(), sqlc.DeliverBalloonParams{TaskID: taskID, Recipient: recipient, DeliveredBy: &rc.admin.ID})
	}
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("task", taskID)
	rc.note("recipient", recipient)
	site := r.FormValue("site")
	if webkit.IsHTMX(r) {
		d, err := s.balloons(r, c, site)
		if err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		s.renderPartial(w, "balloon-list", wrap{P: s.newPage(w, r, rc, "Balloons", "contests", d), V: d})
		return
	}
	to := "/contests/" + strconv.FormatInt(c.ID, 10) + "/balloons"
	if site != "" {
		to += "?site=" + urlQueryEscape(site)
	}
	s.done(w, r, to, "")
}
