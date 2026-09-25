package contestweb

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/i18n"
	"github.com/D4ND3R/Contest-Management-System/internal/pdf"
	"github.com/D4ND3R/Contest-Management-System/internal/printing"
)

func (f *fixture) postPrint(c *http.Client, csrf, name string, data []byte) (int, string) {
	f.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("csrf", csrf)
	fw, _ := mw.CreateFormFile("file", name)
	fw.Write(data)
	mw.Close()
	req, _ := http.NewRequest("POST", f.url+"/ioi/printing", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// TestPrinting (SPEC_CLOSE B8): contestants print text (typeset with the
// page count known at once) and PDFs, within the jobs, pages per job and
// total pages of the contest, only during the contest.
func TestPrinting(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	c := f.client()
	_, page := f.login(c, "ana", "secret")
	csrf := csrfOf(t, page)
	if code, _ := f.get(c, "/ioi/printing"); code != 404 {
		t.Fatalf("printing disabled = %d", code)
	}
	f.setContest(t, "allow_printing = true, max_print_jobs = 3, max_print_pages = 2, max_print_total_pages = 3")
	_, body := f.get(c, "/ioi/")
	if !strings.Contains(body, `href="/ioi/printing"`) {
		t.Fatal("no printing link")
	}
	code, body := f.postPrint(c, csrf, "sol.cpp", []byte("int main() {\n\treturn 0;\n}\n"))
	if code != 200 || !strings.Contains(body, "sol.cpp") || !strings.Contains(body, "Waiting for the printer") {
		t.Fatalf("print = %d\n%s", code, body)
	}
	jobs, _ := f.q.ListPrintJobsByParticipation(bg, f.part.ID)
	if len(jobs) != 1 || jobs[0].Pages == nil || *jobs[0].Pages != 1 {
		t.Fatalf("jobs %+v", jobs)
	}
	stored, _ := f.store.Stat(bg, jobs[0].Digest)
	if stored == 0 {
		t.Fatal("document not stored")
	}
	long := strings.Repeat("x\n", 200) // 3 pages
	for _, tc := range []struct {
		name string
		data []byte
		code int
		want string
	}{
		{"long.txt", []byte(long), 422, "at most 2 per job"},
		{"a.out", []byte("\x7fELF\x00\x01"), 422, "Only PDF files and plain text"},
		{"empty.txt", nil, 422, "empty"},
		{"broken.pdf", []byte("%PDF-1.4 nothing"), 422, "could not be read"},
	} {
		if code, body := f.postPrint(c, csrf, tc.name, tc.data); code != tc.code || !strings.Contains(body, tc.want) {
			t.Errorf("%s = %d, want %d %q\n%s", tc.name, code, tc.code, tc.want, body)
		}
	}
	// A two-page PDF: 3 pages used in all, the limit.
	d := pdf.New()
	d.AddPage()
	d.AddPage()
	if code, body := f.postPrint(c, csrf, "notes.pdf", d.Bytes()); code != 200 || !strings.Contains(body, "notes.pdf") {
		t.Fatalf("pdf = %d\n%s", code, body)
	}
	if code, body := f.postPrint(c, csrf, "more.txt", []byte("x\n")); code != 429 || !strings.Contains(body, "3 pages") {
		t.Fatalf("total pages = %d\n%s", code, body)
	}
	// Failed jobs do not count; the number of jobs is limited too.
	f.pool.Exec(bg, "UPDATE print_jobs SET status = 'failed', status_text = 'Cancelled by the organizers.' WHERE filename = 'notes.pdf'")
	if code, _ := f.postPrint(c, csrf, "b.txt", []byte("b\n")); code != 200 {
		t.Fatalf("after a failed job = %d", code)
	}
	f.setContest(t, "max_print_jobs = 2")
	if code, body := f.postPrint(c, csrf, "c.txt", []byte("c\n")); code != 429 || !strings.Contains(body, "2 print jobs") {
		t.Fatalf("jobs limit = %d\n%s", code, body)
	}
	if _, body := f.get(c, "/ioi/printing?fragment=1"); !strings.Contains(body, "Cancelled by the organizers.") || !strings.Contains(body, "Not printed") {
		t.Errorf("list:\n%s", body)
	}
	// Only during the contest.
	f.setContest(t, "max_print_jobs = 10, start_time = now() - interval '3 hours', stop_time = now() - interval '1 hour'")
	if code, body := f.postPrint(c, csrf, "late.txt", []byte("x\n")); code != 403 || !strings.Contains(body, "only available during the contest") {
		t.Fatalf("after the contest = %d\n%s", code, body)
	}
}

func TestPrintingMessagesTranslated(t *testing.T) {
	for _, k := range []string{printing.ErrEmpty.Error(), printing.ErrNotText.Error(), printing.ErrBadPDF.Error(), printing.ErrTooLarge.Error(),
		printing.MsgCancelled, "Waiting for the printer", "Printing", "Delivered to you", "Printed: the staff will bring it to you",
		"Not printed", "Ask the organizers.", "queued", "printing"} {
		if !i18n.Has("es", k) {
			t.Errorf("%q has no Spanish translation", k)
		}
	}
}
