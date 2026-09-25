package contestweb

import (
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/events"
	"github.com/D4ND3R/Contest-Management-System/internal/printing"
	"github.com/jackc/pgx/v5"
)

// Printing (C9): contestants send PDFs or plain text; text is typeset here
// so that its pages are known before the job is queued, and the printing
// service only hands documents to CUPS.

type printJobView struct {
	ID       int64
	Filename string
	Created  time.Time
	Pages    int32
	Status   string
	Class    string
	Detail   string
}

type printData struct {
	Jobs []printJobView
	// Limits (0: none) and what the contestant used.
	MaxJobs, MaxPages, MaxTotal int32
	UsedJobs, UsedPages         int64
	// Open: printing is possible now (else Closed says why).
	Open   bool
	Closed string
}

// printingOpen says whether the contestant may print now.
func printingOpen(rc *reqCtx) (bool, string) {
	switch {
	case !rc.contest.AllowPrinting:
		return false, "Printing is not available in this contest."
	case !rc.status.CanSubmit || !rc.status.Official:
		return false, "Printing is only available during the contest."
	}
	return true, ""
}

func (s *Server) printData(r *http.Request, rc *reqCtx) (*printData, error) {
	d := &printData{MaxJobs: rc.contest.MaxPrintJobs, MaxPages: rc.contest.MaxPrintPages}
	if rc.contest.MaxPrintTotalPages != nil {
		d.MaxTotal = *rc.contest.MaxPrintTotalPages
	}
	d.Open, d.Closed = printingOpen(rc)
	jobs, err := s.q.ListPrintJobsByParticipation(r.Context(), rc.part.ID)
	if err != nil {
		return nil, err
	}
	p := &page{Lang: rc.lang}
	for _, j := range jobs {
		v := printJobView{ID: j.ID, Filename: j.Filename, Created: j.CreatedAt}
		if j.Pages != nil {
			v.Pages = *j.Pages
		}
		switch {
		case j.Status == "queued":
			v.Status = p.T("Waiting for the printer")
		case j.Status == "printing":
			v.Status = p.T("Printing")
		case j.Status == "done" && j.DeliveredAt != nil:
			v.Status, v.Class = p.T("Delivered to you"), "ok"
		case j.Status == "done":
			v.Status, v.Class = p.T("Printed: the staff will bring it to you"), "ok"
		default:
			v.Status, v.Class = p.T("Not printed"), "bad"
			if j.StatusText == printing.MsgCancelled {
				v.Detail = p.T(printing.MsgCancelled)
			} else {
				v.Detail = p.T("Ask the organizers.")
			}
		}
		if j.Status != "failed" {
			d.UsedJobs++
			d.UsedPages += int64(v.Pages)
		}
		d.Jobs = append(d.Jobs, v)
	}
	return d, nil
}

func (s *Server) handlePrinting(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	if !rc.contest.AllowPrinting {
		http.NotFound(w, r)
		return
	}
	d, err := s.printData(r, rc)
	if err != nil {
		s.fail(w, err)
		return
	}
	p := s.newPage(rc, "", "printing")
	p.Title, p.Data = p.T("Printing"), d
	if r.URL.Query().Get("fragment") != "" {
		s.renderPartial(w, "print-jobs", p)
		return
	}
	s.render(w, "printing", http.StatusOK, p)
}

func (s *Server) handlePrint(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	if !rc.contest.AllowPrinting {
		http.NotFound(w, r)
		return
	}
	fail := func(status int, msg string, args ...any) {
		p := s.newPage(rc, "", "printing")
		s.errorPage(w, r, rc.contest, status, "Not printed", p.T(msg, args...))
	}
	if ok, why := printingOpen(rc); !ok {
		fail(http.StatusForbidden, why)
		return
	}
	if !s.limiter.Allow(r.Context(), "print:"+itoa(rc.part.ID), 10, time.Minute) {
		fail(http.StatusTooManyRequests, "Too many requests, please slow down.")
		return
	}
	max := int64(s.cfg.MaxPrintBytes)
	if max <= 0 {
		max = 2 << 20
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		fail(http.StatusBadRequest, "Choose a file to print.")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		s.fail(w, err)
		return
	}
	if int64(len(data)) > max {
		fail(http.StatusRequestEntityTooLarge, "The file is too large (at most %d KiB).", max>>10)
		return
	}
	name := filepath.Base(strings.ReplaceAll(hdr.Filename, "\\", "/"))
	if name == "." || name == "/" || !utf8.ValidString(name) || len(name) > 200 {
		name = "document"
	}
	perJob := int(rc.contest.MaxPrintPages)
	if rc.part.Unrestricted {
		perJob = 0
	}
	doc, pages, err := printing.Prepare(rc.part.Username+" — "+name, data, perJob)
	switch {
	case errors.Is(err, printing.ErrTooLarge):
		fail(http.StatusUnprocessableEntity, "The document has %d pages; at most %d per job.", pages, perJob)
		return
	case err != nil:
		fail(http.StatusUnprocessableEntity, err.Error())
		return
	}
	info, err := s.blobs.PutBytes(r.Context(), doc)
	if err != nil {
		s.fail(w, err)
		return
	}
	n := int32(pages)
	var limitMsg string
	var limitArg int32
	err = db.InTx(r.Context(), s.pool, func(tx pgx.Tx, q *sqlc.Queries) error {
		// The participation lock serialises a contestant's requests, so two
		// at once cannot both pass the limits.
		if err := q.LockParticipation(r.Context(), rc.part.ID); err != nil {
			return err
		}
		if !rc.part.Unrestricted {
			used, err := q.PrintUsage(r.Context(), rc.part.ID)
			if err != nil {
				return err
			}
			if mj := rc.contest.MaxPrintJobs; mj > 0 && used.Jobs >= int64(mj) {
				limitMsg, limitArg = "You have used your %d print jobs.", mj
				return nil
			}
			if mt := rc.contest.MaxPrintTotalPages; mt != nil && used.Pages+int64(n) > int64(*mt) {
				limitMsg, limitArg = "This would exceed the %d pages you may print.", *mt
				return nil
			}
		}
		_, err := q.CreatePrintJob(r.Context(), sqlc.CreatePrintJobParams{ParticipationID: rc.part.ID, CreatedAt: rc.now,
			Filename: name, Digest: info.Digest, Pages: &n})
		return err
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	if limitMsg != "" {
		fail(http.StatusTooManyRequests, limitMsg, limitArg)
		return
	}
	if err := events.Publish(r.Context(), s.rdb, s.ns, events.Event{Type: events.TypePrint, ContestID: rc.contest.ID,
		ParticipationID: rc.part.ID, Status: "queued"}); err != nil {
		s.log.Warn("publish print event", "error", err)
	}
	s.log.Info("print job queued", "participation", rc.part.ID, "pages", pages, "file", name)
	http.Redirect(w, r, "/"+rc.contest.Name+"/printing", http.StatusSeeOther)
}
