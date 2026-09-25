package adminweb

import (
	"archive/zip"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/D4ND3R/Contest-Management-System/internal/contestarchive"
)

// handleContestArchive streams the archive of a contest (audited: it holds
// every password hash and, optionally, every submission).
func (s *Server) handleContestArchive(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	c, ok := s.loadContest(w, r, rc)
	if !ok {
		return
	}
	subs := r.URL.Query().Get("submissions") == "1"
	rc.target("contest", c.ID)
	rc.note("submissions", subs)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+c.Name+`-archive.zip"`)
	w.Header().Set("Cache-Control", "no-store")
	if _, err := contestarchive.Export(r.Context(), s.pool, s.blobs, c.ID, w, contestarchive.Options{Submissions: subs}); err != nil {
		// The response has started: the truncated zip does not open.
		s.log.Error("contest archive", "contest", c.ID, "error", err)
	}
}

// handleContestImport creates a new contest from an uploaded archive.
func (s *Server) handleContestImport(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	if err := s.parseUpload(w, r); err != nil {
		s.errorPage(w, r, rc, http.StatusBadRequest, "Upload failed: "+err.Error())
		return
	}
	file, fh, err := r.FormFile("archive")
	if err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, "Choose a file: "+err.Error())
		return
	}
	defer file.Close()
	tr := adminTr(r)
	zr, err := zip.NewReader(file, fh.Size)
	if err != nil {
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, tr("Not a contest archive: %v", err))
		return
	}
	f := newForm(r)
	o := contestarchive.ImportOptions{Name: f.str("name"), TaskSuffix: f.str("task_suffix"), Status: f.str("status")}
	res, err := contestarchive.Import(r.Context(), s.pool, s.blobs, zr, o)
	var ce *contestarchive.ConflictError
	switch {
	case errors.As(err, &ce):
		var parts []string
		if ce.Contest != "" {
			parts = append(parts, tr("A contest named %s already exists.", ce.Contest))
		}
		if len(ce.Tasks) > 0 {
			parts = append(parts, tr("These task names are taken: %s.", strings.Join(ce.Tasks, ", ")))
		}
		parts = append(parts, tr("Choose another contest name or a task name suffix; nothing was imported."))
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, strings.Join(parts, " "))
		return
	case errors.Is(err, contestarchive.ErrNewer):
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, tr("The archive was written by a newer version of CMS: upgrade this installation first."))
		return
	case err != nil:
		s.log.Warn("contest import", "file", fh.Filename, "error", err)
		s.errorPage(w, r, rc, http.StatusUnprocessableEntity, tr("The archive could not be imported (nothing was written): %v", err))
		return
	}
	rc.target("contest", res.ContestID)
	rc.note("file", fh.Filename)
	rc.note("rows", res.Rows)
	s.contestChanged(r.Context(), res.ContestID, 0)
	s.done(w, r, "/contests/"+strconv.FormatInt(res.ContestID, 10),
		"Contest imported: %d rows, %d files; %d existing users and %d teams reused.", res.Rows, res.Blobs, res.ReusedUsers, res.ReusedTeams)
}
