package adminweb

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/D4ND3R/Contest-Management-System/internal/certificate"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/pdf"
	"github.com/D4ND3R/Contest-Management-System/internal/ranking"
	"github.com/jackc/pgx/v5"
)

// maxLogoBytes bounds the certificate logo.
const maxLogoBytes = 2 << 20

type certificatesPage struct {
	Contest      sqlc.Contest
	T            sqlc.CertificateTemplate
	Signatures   string
	Awards       string
	MinScore     string
	HasLogo      bool
	Recipients   int
	Placeholders []string
}

// certificateTemplate loads the saved template of a contest, or the
// default one in the admin's language.
func (s *Server) certificateTemplate(ctx context.Context, r *http.Request, c sqlc.Contest) (sqlc.CertificateTemplate, error) {
	t, err := s.q.GetCertificateTemplate(ctx, c.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return certificate.Default(c, adminTr(r)), nil
	}
	return t, err
}

func (s *Server) certificatesView(w http.ResponseWriter, r *http.Request, rc *reqCtx, c sqlc.Contest, d *certificatesPage) *page {
	return s.newPage(w, r, rc, "Certificates", "contests", d).
		crumb("Contests", "/contests").crumb(c.Name, "/contests/"+strconv.FormatInt(c.ID, 10))
}

func (s *Server) certificatesPage(ctx context.Context, c sqlc.Contest, t sqlc.CertificateTemplate) (*certificatesPage, error) {
	ct := certificate.FromRow(t)
	d := &certificatesPage{Contest: c, T: t, Signatures: certificate.FormatSignatures(ct.Signatures),
		Awards: certificate.FormatAwards(ct.Awards), HasLogo: t.LogoDigest != nil, Placeholders: certificate.Placeholders}
	if t.MinScore != nil {
		d.MinScore = strconv.FormatFloat(*t.MinScore, 'f', -1, 64)
	}
	rk, err := ranking.Compute(ctx, s.q, c.ID, ranking.Options{})
	if err != nil {
		return nil, err
	}
	d.Recipients = len(ct.Recipients(rk, "", ""))
	return d, nil
}

func (s *Server) handleCertificates(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	c, ok := s.loadContest(w, r, rc)
	if !ok {
		return
	}
	t, err := s.certificateTemplate(r.Context(), r, c)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	d, err := s.certificatesPage(r.Context(), c, t)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	s.render(w, "certificates", http.StatusOK, s.certificatesView(w, r, rc, c, d))
}

func (s *Server) handleCertificatesSave(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	c, ok := s.loadContest(w, r, rc)
	if !ok {
		return
	}
	if err := s.parseAnyForm(w, r); err != nil {
		s.errorPage(w, r, rc, http.StatusBadRequest, "Upload failed: "+err.Error())
		return
	}
	ctx := r.Context()
	old, err := s.certificateTemplate(ctx, r, c)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	f := newForm(r)
	t := sqlc.UpsertCertificateTemplateParams{ContestID: c.ID, Title: f.required("title", "Title"), Body: f.str("body"),
		Footer: f.str("footer"), DateText: f.str("date_text"), OnlyAwarded: f.check("only_awarded"),
		ContestantsCanDownload: f.check("contestants_can_download"), LogoDigest: old.LogoDigest}
	if v, ok := f.float("min_score", "Minimum score"); ok {
		t.MinScore = &v
	}
	awards, err := certificate.ParseAwards(f.str("awards"))
	var ae *certificate.AwardLineError
	if errors.As(err, &ae) {
		f.fail("invalid award line %s: write “Name: last rank”, with growing ranks", strconv.Quote(ae.Line))
	}
	if awards == nil {
		awards = []certificate.Award{}
	}
	sigs := certificate.ParseSignatures(f.str("signatures"))
	if sigs == nil {
		sigs = []certificate.Signature{}
	}
	t.Awards, _ = json.Marshal(awards)
	t.Signatures, _ = json.Marshal(sigs)
	if f.check("remove_logo") {
		t.LogoDigest = nil
	}
	if file, fh, err := r.FormFile("logo"); err == nil {
		defer file.Close()
		data, _ := io.ReadAll(io.LimitReader(file, maxLogoBytes+1))
		if fh.Size > maxLogoBytes || len(data) > maxLogoBytes {
			f.fail("the logo may have at most 2 MiB")
		} else if _, err := pdf.New().AddImage(data); err != nil {
			f.fail("the logo must be a PNG or JPEG image: %v", err)
		} else if info, err := s.blobs.PutBytes(ctx, data); err != nil {
			s.internalError(w, r, rc, err)
			return
		} else {
			t.LogoDigest = &info.Digest
		}
	}
	if f.err != nil {
		row := sqlc.CertificateTemplate{ContestID: c.ID, Title: t.Title, Body: t.Body, Footer: t.Footer, DateText: t.DateText,
			Signatures: t.Signatures, Awards: t.Awards, MinScore: t.MinScore, OnlyAwarded: t.OnlyAwarded, LogoDigest: t.LogoDigest,
			ContestantsCanDownload: t.ContestantsCanDownload}
		d, err := s.certificatesPage(ctx, c, row)
		if err != nil {
			s.internalError(w, r, rc, err)
			return
		}
		d.Signatures, d.Awards, d.MinScore = f.str("signatures"), f.str("awards"), f.str("min_score")
		s.formError(w, r, rc, "certificates", s.certificatesView(w, r, rc, c, d), f.err.Error())
		return
	}
	if err := s.q.UpsertCertificateTemplate(ctx, t); err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("contest", c.ID)
	s.done(w, r, "/contests/"+strconv.FormatInt(c.ID, 10)+"/certificates", "Certificate template saved.")
}

// writeCertificates sends the certificates of a contest as a PDF; it
// returns how many there are (0: nothing was sent).
func (s *Server) writeCertificates(ctx context.Context, w http.ResponseWriter, c sqlc.Contest, t sqlc.CertificateTemplate, o certificate.Options) (int, error) {
	rk, err := ranking.Compute(ctx, s.q, c.ID, ranking.Options{})
	if err != nil {
		return 0, err
	}
	doc, n := certificate.Build(ctx, s.blobs, c, t, rk, o)
	if n == 0 {
		return 0, nil
	}
	name := c.Name + "-certificates.pdf"
	if o.ParticipationID != 0 {
		name = c.Name + "-certificate-" + strconv.FormatInt(o.ParticipationID, 10) + ".pdf"
	}
	disposition := "attachment"
	if o.First {
		disposition = "inline"
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", disposition+`; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	return n, doc.Write(w)
}

// handleCertificatesPDF is every certificate of the contest in one PDF
// (?preview=1: the first one, shown inline). Audited: it carries names
// and ranks.
func (s *Server) handleCertificatesPDF(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	c, ok := s.loadContest(w, r, rc)
	if !ok {
		return
	}
	t, err := s.certificateTemplate(r.Context(), r, c)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("contest", c.ID)
	n, err := s.writeCertificates(r.Context(), w, c, t, certificate.Options{First: r.URL.Query().Get("preview") == "1"})
	switch {
	case err != nil:
		s.internalError(w, r, rc, err)
	case n == 0:
		s.errorPage(w, r, rc, http.StatusNotFound, "No contestant receives a certificate with these settings.")
	default:
		rc.note("certificates", n)
	}
}

// handleParticipationCertificate is one contestant's certificate.
func (s *Server) handleParticipationCertificate(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	id, _ := pathID(r, "id")
	p, err := s.q.GetParticipation(r.Context(), id)
	if err != nil {
		s.notFound(w, r, rc)
		return
	}
	c, err := s.q.GetContest(r.Context(), p.ContestID)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	t, err := s.certificateTemplate(r.Context(), r, c)
	if err != nil {
		s.internalError(w, r, rc, err)
		return
	}
	rc.target("participation", p.ID)
	n, err := s.writeCertificates(r.Context(), w, c, t, certificate.Options{ParticipationID: p.ID})
	switch {
	case err != nil:
		s.internalError(w, r, rc, err)
	case n == 0:
		s.errorPage(w, r, rc, http.StatusNotFound, "This contestant receives no certificate with the current settings (hidden, below the minimum score or without an award).")
	}
}
