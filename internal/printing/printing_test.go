package printing

import (
	"bytes"
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/events"
	"github.com/D4ND3R/Contest-Management-System/internal/logging"
	"github.com/D4ND3R/Contest-Management-System/internal/pdf"
	"github.com/D4ND3R/Contest-Management-System/internal/testutil"
)

func TestPrepareText(t *testing.T) {
	src := "#include <stdio.h>\r\n\tint main() {\n" + strings.Repeat("x", 200) + "\n}\n"
	for i := 0; i < 150; i++ {
		src += "// línea con acentos: ñandú\n"
	}
	doc, pages, err := Prepare("ana — sol.cpp", []byte("\xef\xbb\xbf"+src), 0)
	if err != nil {
		t.Fatal(err)
	}
	// 155 source lines, one wrapped twice: 157 printed lines.
	if want := (157 + linesPerPage - 1) / linesPerPage; pages != want {
		t.Fatalf("pages %d, want %d", pages, want)
	}
	if n, err := pdf.CountPages(doc); n != pages || err != nil {
		t.Fatalf("document has %d pages (%v)", n, err)
	}
	for _, want := range []string{"/Courier", "    1 #include <stdio.h>", `    2     int main\(\) {`, "1 / 3", "l\xednea con acentos: \xf1and\xfa"} {
		if !bytes.Contains(doc, []byte(want)) {
			t.Errorf("document lacks %q", want)
		}
	}
	if _, _, err := Prepare("x", []byte(src), 2); !errors.Is(err, ErrTooLarge) {
		t.Errorf("page limit: %v", err)
	}
	for in, want := range map[string]error{"": ErrEmpty, "\n \n": ErrEmpty, "a\x00b": ErrNotText, "\xff\xfe": ErrNotText, "%PDF-1.4 junk": ErrBadPDF} {
		if _, _, err := Prepare("x", []byte(in), 0); !errors.Is(err, want) {
			t.Errorf("%q: %v, want %v", in, err, want)
		}
	}
	// PDFs are printed as they are.
	d := pdf.New()
	d.AddPage()
	d.AddPage()
	in := d.Bytes()
	out, n, err := Prepare("x", in, 5)
	if err != nil || n != 2 || !bytes.Equal(out, in) {
		t.Fatalf("pdf: %d %v", n, err)
	}
	if _, _, err := Prepare("x", in, 1); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("pdf limit: %v", err)
	}
	if b := Banner(BannerInfo{JobID: 7, Username: "ana", Team: "MEX", Pages: 2, Submitted: time.Now()}); !bytes.Contains(b, []byte("(ana)")) {
		t.Fatal("banner")
	}
}

// TestService prints through a fake lp, records failures, and takes back
// the jobs a crash interrupted.
func TestService(t *testing.T) {
	pool := testutil.DB(t)
	rdb, ns := testutil.Redis(t)
	q := sqlc.New(pool)
	store := blob.NewMem()
	ctx := context.Background()
	c, _ := q.CreateContest(ctx, db.NewContestParams("final", time.Now(), time.Now().Add(time.Hour)))
	u, _ := q.CreateUser(ctx, sqlc.CreateUserParams{Username: "ana", FirstName: "Ana", PreferredLanguages: []string{}})
	p, _ := q.CreateParticipation(ctx, sqlc.CreateParticipationParams{ContestID: c.ID, UserID: u.ID, Ip: []netip.Prefix{}})
	doc, pages, _ := Prepare("ana — a.txt", []byte("hola\n"), 0)
	info, _ := store.PutBytes(ctx, doc)
	newJob := func() sqlc.PrintJob {
		n := int32(pages)
		j, err := q.CreatePrintJob(ctx, sqlc.CreatePrintJobParams{ParticipationID: p.ID, CreatedAt: time.Now(), Filename: "a.txt",
			Digest: info.Digest, Pages: &n})
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	// A job left "printing" by a crash.
	stuck := newJob()
	pool.Exec(ctx, "UPDATE print_jobs SET status = 'printing' WHERE id = $1", stuck.ID)

	dir := t.TempDir()
	lp := filepath.Join(dir, "lp")
	// The fake lp records its arguments and the files it got; "fail" in
	// the printer name makes it fail like CUPS does.
	os.WriteFile(lp, []byte(`#!/bin/sh
echo "$@" >> "`+dir+`/calls"
case "$2" in *fail*) echo "lp: The printer or class does not exist." >&2; exit 1;; esac
for f; do :; done
cat "$f" > "`+dir+`/last.pdf"
echo "request id is $2-1 (2 file(s))"
`), 0o755)
	svc := New(q, rdb, ns, store, config.Printing{Printer: "hall", LPPath: lp, PaperSize: "A4"}, logging.Discard())
	svc.Idle, svc.backoff = 100*time.Millisecond, time.Millisecond
	var got []events.Event
	evCtx, stopEv := context.WithCancel(ctx)
	defer stopEv()
	evs := make(chan events.Event, 16)
	go events.Subscribe(evCtx, rdb, ns, func(e events.Event) { evs <- e })
	time.Sleep(50 * time.Millisecond)
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { svc.Run(runCtx); close(done) }()
	wait := func(id int64, status string) sqlc.PrintJob {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			jobs, _ := q.ListPrintJobsByParticipation(ctx, p.ID)
			for _, j := range jobs {
				if j.ID == id && j.Status == status {
					return j
				}
			}
		}
		t.Fatalf("job %d never became %s", id, status)
		return sqlc.PrintJob{}
	}
	if j := wait(stuck.ID, "done"); j.PrintedAt == nil || !strings.Contains(j.StatusText, "request id is hall-1") {
		t.Fatalf("interrupted job %+v", j)
	}
	// A new job is printed at once when its event arrives.
	j := newJob()
	events.Publish(ctx, rdb, ns, events.Event{Type: events.TypePrint, ContestID: c.ID, ParticipationID: p.ID, Status: "queued"})
	wait(j.ID, "done")
	calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
	if !strings.Contains(string(calls), "-d hall -t cms-") || !strings.Contains(string(calls), "media=A4") || !strings.Contains(string(calls), "banner.pdf") {
		t.Fatalf("lp calls:\n%s", calls)
	}
	if last, _ := os.ReadFile(filepath.Join(dir, "last.pdf")); !bytes.Equal(last, doc) {
		t.Fatal("the document sent differs")
	}
	cancel()
	<-done

	// A printer that fails: retried, then the job fails with lp's message.
	svc = New(q, rdb, ns, store, config.Printing{Printer: "fail", LPPath: lp}, logging.Discard())
	svc.Idle, svc.backoff = 100*time.Millisecond, time.Millisecond
	runCtx, cancel = context.WithCancel(ctx)
	done = make(chan struct{})
	go func() { svc.Run(runCtx); close(done) }()
	j = newJob()
	if f := wait(j.ID, "failed"); !strings.Contains(f.StatusText, "does not exist") {
		t.Fatalf("failed job %+v", f)
	}
	calls, _ = os.ReadFile(filepath.Join(dir, "calls"))
	if n := strings.Count(string(calls), "-d fail"); n != 3 {
		t.Errorf("%d attempts, want 3", n)
	}
	cancel()
	<-done
	stopEv()
	for len(evs) > 0 {
		got = append(got, <-evs)
	}
	var statuses []string
	for _, e := range got {
		if e.Type == events.TypePrint && e.ParticipationID == p.ID {
			statuses = append(statuses, e.Status)
		}
	}
	if s := strings.Join(statuses, ","); s != "done,queued,done,failed" {
		t.Errorf("events %s", s)
	}
}
