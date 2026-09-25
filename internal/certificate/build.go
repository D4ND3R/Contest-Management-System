package certificate

import (
	"context"
	"encoding/json"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/pdf"
	"github.com/D4ND3R/Contest-Management-System/internal/ranking"
)

// Default is the template of a contest that has not saved one, with the
// texts in the language of tr.
func Default(c sqlc.Contest, tr func(string, ...any) string) sqlc.CertificateTemplate {
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		loc = time.UTC
	}
	return sqlc.CertificateTemplate{
		ContestID: c.ID,
		Title:     tr("Certificate"),
		Body: tr("This certificate is awarded to") + "\n\n# {name}\n\n" +
			tr("for taking part in {contest}, ranked {rank} of {participants} with {score} points.") + "\n\n## {award}",
		DateText:   c.StopTime.In(loc).Format("02/01/2006"),
		Footer:     "{date}",
		Signatures: json.RawMessage(`[]`),
		Awards:     json.RawMessage(`[]`),
	}
}

// FromRow converts a stored template.
func FromRow(t sqlc.CertificateTemplate) *Template {
	ct := &Template{Title: t.Title, Body: t.Body, Footer: t.Footer, MinScore: t.MinScore, OnlyAwarded: t.OnlyAwarded}
	_ = json.Unmarshal(t.Signatures, &ct.Signatures)
	_ = json.Unmarshal(t.Awards, &ct.Awards)
	return ct
}

// Options select the certificates of a Build.
type Options struct {
	// ParticipationID keeps one contestant (0: everybody).
	ParticipationID int64
	// First stops after the first certificate (preview).
	First bool
}

// Build renders the certificates of a contest from its final ranking rk;
// it returns the document and the number of pages (0: nobody qualifies).
func Build(ctx context.Context, store blob.Store, c sqlc.Contest, t sqlc.CertificateTemplate, rk *ranking.Ranking, o Options) (*pdf.Doc, int) {
	name := c.Description
	if name == "" {
		name = c.Name
	}
	ct := FromRow(t)
	doc := pdf.New()
	doc.Title = t.Title
	var logo *pdf.Image
	if t.LogoDigest != nil {
		if data, err := blob.ReadAll(ctx, store, *t.LogoDigest); err == nil {
			logo, _ = doc.AddImage(data)
		}
	}
	n := 0
	for _, r := range ct.Recipients(rk, name, t.DateText) {
		if o.ParticipationID != 0 && r.ParticipationID != o.ParticipationID {
			continue
		}
		ct.Render(doc, r, logo)
		n++
		if o.First {
			break
		}
	}
	return doc, n
}
