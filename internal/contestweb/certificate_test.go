package contestweb

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/events"
)

// TestContestantCertificate (SPEC_CLOSE E2): contestants download their
// own certificate only when the admin allows it and their contest time is
// over; hidden contestants never.
func TestContestantCertificate(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	c := f.client()
	f.login(c, "ana", "secret")
	save := func(allow bool) {
		t.Helper()
		err := f.q.UpsertCertificateTemplate(bg, sqlc.UpsertCertificateTemplateParams{ContestID: f.contest.ID, Title: "Certificado",
			Body: "# {name}\n\n{rank}/{participants}", Signatures: json.RawMessage(`[]`), Awards: json.RawMessage(`[]`), ContestantsCanDownload: allow})
		if err != nil {
			t.Fatal(err)
		}
	}
	if code, _ := f.get(c, "/ioi/certificate.pdf"); code != 404 {
		t.Fatalf("no template = %d", code)
	}
	save(true)
	// The contest is running: not yet.
	if code, _ := f.get(c, "/ioi/certificate.pdf"); code != 404 {
		t.Fatalf("during the contest = %d", code)
	}
	f.setContest(t, "start_time = now() - interval '3 hours', stop_time = now() - interval '1 hour'")
	_, body := f.get(c, "/ioi/")
	if !strings.Contains(body, `href="/ioi/certificate.pdf"`) {
		t.Fatalf("no certificate link:\n%s", body)
	}
	code, body := f.get(c, "/ioi/certificate.pdf")
	if code != 200 || !strings.HasPrefix(body, "%PDF") || !strings.Contains(body, "(Certificado)") {
		t.Fatalf("certificate = %d", code)
	}
	save(false)
	if code, _ := f.get(c, "/ioi/certificate.pdf"); code != 404 {
		t.Fatalf("not allowed = %d", code)
	}
	save(true)
	f.pool.Exec(bg, "UPDATE participations SET hidden = true WHERE id = $1", f.part.ID)
	events.Publish(bg, f.rdb, f.ns, events.Event{Type: events.TypeContest, ParticipationID: f.part.ID})
	time.Sleep(100 * time.Millisecond)
	if code, _ := f.get(c, "/ioi/certificate.pdf"); code != 404 {
		t.Fatalf("hidden = %d", code)
	}
}
