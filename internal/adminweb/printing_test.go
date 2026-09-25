package adminweb

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/pdf"
	"github.com/D4ND3R/Contest-Management-System/internal/printing"
)

// TestPrintQueue (SPEC_CLOSE B8): the staff see the jobs by state, hand
// printed ones over (and undo), reprint and cancel; the contest form
// saves the total pages per contestant.
func TestPrintQueue(t *testing.T) {
	f := newFixture(t)
	f.pool.Exec(bg, "UPDATE contests SET allow_printing = true WHERE id = $1", f.contest.ID)
	d := pdf.New()
	d.AddPage()
	doc := f.put(string(d.Bytes()))
	job := func(name, status string) sqlc.PrintJob {
		n := int32(1)
		j, err := f.q.CreatePrintJob(bg, sqlc.CreatePrintJobParams{ParticipationID: f.part.ID, CreatedAt: time.Now(), Filename: name, Digest: doc, Pages: &n})
		if err != nil {
			t.Fatal(err)
		}
		f.pool.Exec(bg, "UPDATE print_jobs SET status = $2, status_text = CASE WHEN $2 = 'failed' THEN 'lp: no such printer' ELSE '' END WHERE id = $1", j.ID, status)
		return j
	}
	printed, queued, failed := job("printed.txt", "done"), job("queued.txt", "queued"), job("failed.txt", "failed")
	a := f.login("messaging")
	path := fmt.Sprintf("/contests/%d/printing", f.contest.ID)
	code, body := a.Get(path)
	if code != 200 {
		t.Fatalf("queue = %d\n%s", code, body)
	}
	toDeliver := body[strings.Index(body, "Printed, to deliver"):strings.Index(body, "Waiting for the printer")]
	if !strings.Contains(toDeliver, "printed.txt") || strings.Contains(toDeliver, "queued.txt") || !strings.Contains(body, "lp: no such printer") {
		t.Fatalf("queue:\n%s", body)
	}
	if _, cp := a.Get(fmt.Sprintf("/contests/%d", f.contest.ID)); !strings.Contains(cp, path) {
		t.Error("no printing link on the contest page")
	}
	act := func(b interface {
		Post(string, url.Values) (int, string)
	}, id int64, action string) int {
		code, _ := b.Post(fmt.Sprintf("/print-jobs/%d/%s", id, action), url.Values{})
		return code
	}
	if code := act(f.login("read_only"), printed.ID, "deliver"); code != http.StatusForbidden {
		t.Fatalf("read-only deliver = %d", code)
	}
	code, body = a.PostHTMX(fmt.Sprintf("/print-jobs/%d/deliver", printed.ID), url.Values{})
	if code != 200 || !strings.Contains(body, `id="print-queue"`) || !strings.Contains(body, "admin_messaging") {
		t.Fatalf("deliver = %d\n%s", code, body)
	}
	state := func(id int64) (string, *time.Time, string) {
		var st, text string
		var at *time.Time
		f.pool.QueryRow(bg, "SELECT status, delivered_at, status_text FROM print_jobs WHERE id = $1", id).Scan(&st, &at, &text)
		return st, at, text
	}
	if _, at, _ := state(printed.ID); at == nil {
		t.Fatal("not delivered")
	}
	if code := act(a, printed.ID, "undeliver"); code != 200 {
		t.Fatalf("undeliver = %d", code)
	}
	if _, at, _ := state(printed.ID); at != nil {
		t.Fatal("still delivered")
	}
	if code := act(a, queued.ID, "deliver"); code != http.StatusConflict {
		t.Fatalf("deliver a queued job = %d", code)
	}
	if code := act(a, queued.ID, "cancel"); code != 200 {
		t.Fatalf("cancel = %d", code)
	}
	if st, _, text := state(queued.ID); st != "failed" || text != printing.MsgCancelled {
		t.Fatalf("cancelled job %s %q", st, text)
	}
	if code := act(a, printed.ID, "cancel"); code != http.StatusConflict {
		t.Fatalf("cancel a printed job = %d", code)
	}
	if code := act(a, failed.ID, "reprint"); code != 200 {
		t.Fatalf("reprint = %d", code)
	}
	if st, _, text := state(failed.ID); st != "queued" || text != "" {
		t.Fatalf("reprinted job %s %q", st, text)
	}
	if code := act(a, failed.ID, "explode"); code != http.StatusNotFound {
		t.Fatalf("unknown action = %d", code)
	}
	code, pdfBody := a.Get(fmt.Sprintf("/print-jobs/%d/pdf", printed.ID))
	if code != 200 || !strings.HasPrefix(pdfBody, "%PDF-") {
		t.Fatalf("pdf = %d", code)
	}
	// The contest form: total pages per contestant.
	now := time.Now().UTC()
	form := url.Values{"name": {"seeded"}, "timezone": {"UTC"},
		"start_time": {now.Add(-time.Hour).Format("2006-01-02T15:04:05")}, "stop_time": {now.Add(time.Hour).Format("2006-01-02T15:04:05")},
		"token_mode": {"disabled"}, "token_gen_interval_s": {"1800"}, "scoring_mode": {"ioi"}, "icpc_penalty_minutes": {"20"},
		"allow_printing": {"on"}, "max_print_jobs": {"5"}, "max_print_pages": {"4"}, "max_print_total_pages": {"12"}}
	all := f.login("all")
	if code, body := all.Post(fmt.Sprintf("/contests/%d", f.contest.ID), form); code != 200 {
		t.Fatalf("save = %d\n%s", code, body)
	}
	c, _ := f.q.GetContest(bg, f.contest.ID)
	if !c.AllowPrinting || c.MaxPrintJobs != 5 || c.MaxPrintPages != 4 || c.MaxPrintTotalPages == nil || *c.MaxPrintTotalPages != 12 {
		t.Fatalf("contest %v %d %d %v", c.AllowPrinting, c.MaxPrintJobs, c.MaxPrintPages, c.MaxPrintTotalPages)
	}
	form.Set("max_print_total_pages", "0")
	if code, _ := all.Post(fmt.Sprintf("/contests/%d", f.contest.ID), form); code != 422 {
		t.Fatalf("total pages 0 = %d", code)
	}
}
