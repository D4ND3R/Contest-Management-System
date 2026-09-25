package adminweb

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"github.com/D4ND3R/Contest-Management-System/internal/backup"
)

// backupsView is the backups page: settings, the running backup and the
// list (A4). Restoring is a command-line operation (services stopped).
type backupsView struct {
	Enabled         bool
	Dir             string
	Interval        string
	ContestInterval string
	Keep            int
	S3              bool
	Running         *backup.Status
	Entries         []backup.Entry
	Error           string
}

func (s *Server) backupsData() *backupsView {
	v := &backupsView{Enabled: s.backups != nil}
	if s.backups == nil {
		return v
	}
	cfg := s.backups.Config()
	v.Dir, v.Keep, v.S3 = cfg.Dir, cfg.Keep, s.backups.HasRemote()
	if d := cfg.Interval.D(); d > 0 {
		v.Interval = d.String()
	}
	if d := cfg.ContestInterval.D(); d > 0 {
		v.ContestInterval = d.String()
	}
	v.Running = s.backups.Running()
	list, err := s.backups.List()
	if err != nil {
		v.Error = err.Error()
	}
	v.Entries = list
	return v
}

func (s *Server) handleBackups(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	v := s.backupsData()
	p := s.newPage(w, r, rc, "Backups", "backups", v)
	if r.URL.Query().Get("fragment") != "" {
		w.Header().Set("Cache-Control", "no-store")
		s.renderPartial(w, "backup-list", wrap{P: p, V: v})
		return
	}
	s.render(w, "backups", http.StatusOK, p)
}

// handleBackupCreate starts a backup in the background; the page follows
// its progress.
func (s *Server) handleBackupCreate(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	if s.backups == nil {
		s.errorPage(w, r, rc, http.StatusServiceUnavailable, "Backups are not configured.")
		return
	}
	if err := s.backups.Start(r.Context(), backup.KindManual, rc.admin.Username); err != nil {
		if errors.Is(err, backup.ErrBusy) {
			s.errorPage(w, r, rc, http.StatusConflict, "Another backup is running.")
			return
		}
		s.internalError(w, r, rc, err)
		return
	}
	s.done(w, r, "/backups", "Backup started.")
}

func (s *Server) handleBackupDownload(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	if s.backups == nil {
		s.notFound(w, r, rc)
		return
	}
	name := r.PathValue("name")
	path, err := s.backups.Path(name)
	if err != nil {
		s.notFound(w, r, rc)
		return
	}
	rc.note("name", name)
	f, err := os.Open(path)
	if err != nil {
		s.notFound(w, r, rc)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	// The file holds every password hash: downloads are audited.
	s.audit(r, &rc.admin.ID, "backup.download", map[string]any{"name": name})
	w.Header().Set("Content-Type", "application/zstd")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(path)+`"`)
	http.ServeContent(w, r, "", info.ModTime(), f)
}

func (s *Server) handleBackupDelete(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	if s.backups == nil {
		s.notFound(w, r, rc)
		return
	}
	name := r.PathValue("name")
	if _, err := s.backups.Path(name); err != nil {
		s.notFound(w, r, rc)
		return
	}
	rc.note("name", name)
	if err := s.backups.Delete(r.Context(), name); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	s.done(w, r, "/backups", "Backup deleted.")
}
