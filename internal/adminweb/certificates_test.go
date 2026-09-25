package adminweb

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/pdf"
	"github.com/D4ND3R/Contest-Management-System/internal/webtest"
)

// TestCertificatesFromAdmin (SPEC_CLOSE E2): the template is edited with
// awards, signatures and a logo; the certificates of the contest and of
// one participation are PDFs following the ranking; bad input is refused.
func TestCertificatesFromAdmin(t *testing.T) {
	f := newFixture(t)
	path := fmt.Sprintf("/contests/%d/certificates", f.contest.ID)
	a := f.login("all")
	code, body := a.Get(path)
	if code != 200 || !strings.Contains(body, "{name}") || !strings.Contains(body, `name="awards"`) {
		t.Fatalf("form = %d\n%s", code, body)
	}
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	img.Set(1, 1, color.NRGBA{255, 0, 0, 255})
	var logo bytes.Buffer
	png.Encode(&logo, img)
	fields := map[string]string{"title": "Constancia", "body": "Se otorga a\n\n# {name}\n\n## {award}", "date_text": "Lima, 2026",
		"footer": "{date}", "signatures": "Dra. Ruiz | Presidenta", "awards": "Oro: 1", "contestants_can_download": "on"}
	code, body = a.PostMultipart(path, fields, webtest.File{Field: "logo", Name: "logo.png", Data: logo.Bytes()})
	if code != 200 || !strings.Contains(body, "Certificate template saved") {
		t.Fatalf("save = %d\n%s", code, body)
	}
	tpl, err := f.q.GetCertificateTemplate(bg, f.contest.ID)
	if err != nil || tpl.Title != "Constancia" || tpl.LogoDigest == nil || !tpl.ContestantsCanDownload || string(tpl.Awards) != `[{"name": "Oro", "up_to_rank": 1}]` {
		t.Fatalf("template %+v %v (%s)", tpl, err, tpl.Awards)
	}
	code, body = a.Get(path + ".pdf")
	if code != 200 || !strings.HasPrefix(body, "%PDF") {
		t.Fatalf("pdf = %d", code)
	}
	if n, err := pdf.CountPages([]byte(body)); err != nil || n != 1 {
		t.Fatalf("pages = %d %v", n, err)
	}
	for _, want := range []string{"(Constancia)", "(Ana)", "(Oro)", "(Lima, 2026)", "(Dra. Ruiz)", "/Im1 Do"} {
		if !strings.Contains(body, want) {
			t.Errorf("certificate misses %q", want)
		}
	}
	code, body = a.Get(fmt.Sprintf("/participations/%d/certificate.pdf", f.part.ID))
	if code != 200 || !strings.Contains(body, "(Ana)") {
		t.Fatalf("participation certificate = %d", code)
	}
	// Invalid awards and a non-image logo: nothing is saved.
	fields["awards"] = "Oro: 3\nPlata: 2"
	code, body = a.PostMultipart(path, fields)
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "invalid award line") {
		t.Fatalf("bad awards = %d\n%s", code, body)
	}
	fields["awards"] = ""
	code, body = a.PostMultipart(path, fields, webtest.File{Field: "logo", Name: "x.png", Data: []byte("nope")})
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "PNG or JPEG") {
		t.Fatalf("bad logo = %d\n%s", code, body)
	}
	// Only awarded contestants, and nobody qualifies: a clear page.
	fields["awards"], fields["min_score"] = "", "1000"
	if code, _ = a.PostMultipart(path, fields); code != 200 {
		t.Fatalf("save = %d", code)
	}
	if code, body = a.Get(path + ".pdf"); code != 404 || !strings.Contains(body, "No contestant receives") {
		t.Fatalf("nobody = %d", code)
	}
	if code, _ := f.login("read_only").PostMultipart(path, fields); code != http.StatusForbidden {
		t.Fatalf("read-only save = %d", code)
	}
	var n int
	f.pool.QueryRow(bg, "SELECT count(*) FROM audit_log WHERE action IN ('certificates.update', 'certificates.download')").Scan(&n)
	if n < 4 {
		t.Fatalf("%d audit entries", n)
	}
}
