package adminweb

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/events"
	"github.com/D4ND3R/Contest-Management-System/internal/printing"
	"github.com/D4ND3R/Contest-Management-System/internal/webkit"
)

// The staff print queue (C9): jobs waiting or being printed, printed ones
// to deliver, and the delivered ones.

type printQueue struct {
	C                           sqlc.Contest
	Sites                       []sqlc.Site
	Site                        string
	Waiting, Printed, Delivered []sqlc.ListPrintJobsByContestRow
	Failed                      []sqlc.ListPrintJobsByContestRow
}

// Query is the list's query string (site filter).
func (d *printQueue) Query() string {
	if d.Site == "" {
		return ""
	}
	return "site=" + urlQueryEscape(d.Site)
}

func (s *Server) printQueue(r *http.Request, c sqlc.Contest, site string) (*printQueue, error) {
	d := &printQueue{C: c, Site: site}
	var err error
	if d.Sites, err = s.q.ListSites(r.Context(), c.ID); err != nil {
		return nil, err
	}
	jobs, err := s.q.ListPrintJobsByContest(r.Context(), c.ID)
	if err != nil {
		return nil, err
	}
	for _, j := range jobs {
		if site != "" && derefStr(j.SiteName) != site {
			continue
		}
		switch {
		case j.Status == "failed":
			d.Failed = append(d.Failed, j)
		case j.Status == "done" && j.DeliveredAt != nil:
			d.Delivered = append(d.Delivered, j)
		case j.Status == "done":
			d.Printed = append(d.Printed, j)
		default:
			d.Waiting = append(d.Waiting, j)
		}
	}
	// Latest deliveries first.
	for i, j := 0, len(d.Delivered)-1; i < j; i, j = i+1, j-1 {
		d.Delivered[i], d.Delivered[j] = d.Delivered[j], d.Delivered[i]
	}
	return d, nil
}

func (s *Server) handlePrintQueue(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	c, ok := s.loadContest(w, r, rc)
	if !ok {
		return
	}
	s.renderPrintQueue(w, r, rc, c, r.URL.Query().Get("site"), r.URL.Query().Get("fragment") != "")
}

func (s *Server) renderPrintQueue(w http.ResponseWriter, r *http.Request, rc *reqCtx, c sqlc.Contest, site string, fragment bool) {
	d, err := s.printQueue(r, c, site)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	p := s.newPage(w, r, rc, "Printing", "contests", d).
		crumb("Contests", "/contests").crumb(c.Name, "/contests/"+strconv.FormatInt(c.ID, 10))
	if fragment {
		w.Header().Set("Cache-Control", "no-store")
		s.renderPartial(w, "print-queue", wrap{P: p, V: d})
		return
	}
	s.render(w, "printing", http.StatusOK, p)
}

// loadPrintJob loads the job of the path and its contest.
func (s *Server) loadPrintJob(w http.ResponseWriter, r *http.Request, rc *reqCtx) (sqlc.GetPrintJobInfoRow, bool) {
	id, _ := pathID(r, "id")
	j, err := s.q.GetPrintJobInfo(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r, rc)
		} else {
			s.internalError(w, r, rc, err)
		}
		return j, false
	}
	return j, true
}

// handlePrintJobAction delivers (or undelivers), reprints or cancels a job.
func (s *Server) handlePrintJobAction(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	j, ok := s.loadPrintJob(w, r, rc)
	if !ok {
		return
	}
	ctx := r.Context()
	var err error
	switch action := r.PathValue("action"); action {
	case "deliver", "undeliver":
		if j.Status != "done" {
			s.errorPage(w, r, rc, http.StatusConflict, "Only printed jobs can be delivered.")
			return
		}
		err = s.q.SetPrintJobDelivered(ctx, sqlc.SetPrintJobDeliveredParams{ID: j.ID, Delivered: action == "deliver", AdminID: &rc.admin.ID})
	case "reprint":
		err = s.q.RequeuePrintJob(ctx, j.ID)
	case "cancel":
		var n int64
		if n, err = s.q.CancelPrintJob(ctx, sqlc.CancelPrintJobParams{ID: j.ID, StatusText: printing.MsgCancelled}); err == nil && n == 0 {
			s.errorPage(w, r, rc, http.StatusConflict, "Only jobs waiting for the printer can be cancelled.")
			return
		}
	default:
		s.notFound(w, r, rc)
		return
	}
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("print_job", j.ID)
	rc.note("action", r.PathValue("action"))
	rc.note("username", j.Username)
	// The contestant's page and the printing service follow.
	status := map[string]string{"reprint": "queued", "cancel": "failed"}[r.PathValue("action")]
	if status == "" {
		status = "done"
	}
	if err := events.Publish(ctx, s.rdb, s.ns, events.Event{Type: events.TypePrint, ContestID: j.ContestID,
		ParticipationID: j.ParticipationID, Status: status}); err != nil {
		s.log.Warn("publish print event", "error", err)
	}
	c, err := s.q.GetContest(ctx, j.ContestID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	site := r.FormValue("site")
	if webkit.IsHTMX(r) {
		s.renderPrintQueue(w, r, rc, c, site, true)
		return
	}
	to := "/contests/" + strconv.FormatInt(c.ID, 10) + "/printing"
	if site != "" {
		to += "?site=" + urlQueryEscape(site)
	}
	s.done(w, r, to, "")
}

// handlePrintJobPDF shows the document of a job.
func (s *Server) handlePrintJobPDF(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	j, ok := s.loadPrintJob(w, r, rc)
	if !ok {
		return
	}
	name := strings.TrimSuffix(j.Filename, ".pdf") + ".pdf"
	s.serveBlob(w, r, rc, j.Digest, "application/pdf", name, false)
}
