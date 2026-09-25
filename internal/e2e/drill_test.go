package e2e

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/pdf"
	"github.com/D4ND3R/Contest-Management-System/internal/webtest"
)

// submitFiles sends a submission with the given files (field = submission
// format entry) from the contestant's browser.
func (b *browser) submitFiles(task, lang string, files map[string][2]string) int {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("csrf", b.csrf)
	if lang != "" {
		mw.WriteField("language", lang)
	}
	for field, f := range files {
		fw, _ := mw.CreateFormFile(field, f[0])
		fw.Write([]byte(f[1]))
	}
	mw.Close()
	req, _ := http.NewRequest("POST", b.s.cwsURL+"/e2e/tasks/"+task+"/submit", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("HX-Request", "true")
	resp, err := b.c.Do(req)
	if err != nil {
		b.s.t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

// TestDrill plays the rehearsal of docs/en/drill.md (SPEC_CLOSE F5) with
// real judging: three problems (normal I/O, interactive, output only)
// imported from the admin panel, two contestants, a question and its
// public answer, an announcement, an invalidated submission, the frozen
// ranking and its unfreezing, and the exported results.
func TestDrill(t *testing.T) {
	s := newStack(t, stackOpts{workers: true, admin: true})
	a := s.adminBrowser(t)
	cid := itoa(s.contest.ID)

	// 1. Two more problems from the documented packages.
	examples := filepath.Join("..", "..", "docs", "examples", "packages")
	for _, name := range []string{"interactive-adivina", "output-only-cuadrados"} {
		code, body := a.PostMultipart("/tasks/import", map[string]string{"step": "preview", "mode": "task", "contest_id": cid},
			webtest.File{Field: "package", Name: name + ".zip", Data: zipFolder(t, filepath.Join(examples, name), nil)})
		m := digestRe.FindStringSubmatch(body)
		if code != 200 || m == nil {
			t.Fatalf("%s preview = %d\n%s", name, code, body)
		}
		code, body = a.Post("/tasks/import", url.Values{"step": {"confirm"}, "digest": {m[1]}, "mode": {"task"}, "contest_id": {cid}})
		webtest.MustOK(t, name+" import", code, body)
	}
	tasks, _ := s.q.ListTasksByContest(bg, &s.contest.ID)
	if len(tasks) != 3 {
		t.Fatalf("%d tasks in the contest", len(tasks))
	}
	byName := map[string]sqlc.Task{}
	for _, tk := range tasks {
		byName[tk.Name] = tk
	}
	// The public ranking freezes for the last 200 minutes (from 20 minutes
	// ago): what follows is hidden from contestants until unfrozen.
	s.pool.Exec(bg, "UPDATE contests SET ranking_freeze_minutes = 200 WHERE id = $1", s.contest.ID)
	a.Post("/contests/"+cid+"/ranking/freeze", url.Values{"unfrozen": {"0"}})

	ana, beto := s.addContestant("ana"), s.addContestant("beto")
	ca, cb := s.login("ana"), s.login("beto")

	// 2. Submissions: ana solves everything, beto fails or half-solves.
	read := func(p string) string { return mustRead(t, filepath.Join(examples, p)) }
	aplusb := "#include <stdio.h>\nint main(void){long a,b;scanf(\"%ld %ld\",&a,&b);printf(\"%ld\\n\",a+b);return 0;}\n"
	format := func(task string) string { return byName[task].SubmissionFormat[0] }
	for _, sub := range []struct {
		c     *browser
		task  string
		lang  string
		files map[string][2]string
	}{
		{ca, "sum", "c11", map[string][2]string{"sum.%l": {"sum.c", aplusb}}},
		{cb, "sum", "python3", map[string][2]string{"sum.%l": {"sum.py", "a, b = map(int, input().split())\nprint(a - b)\n"}}},
		{ca, "adivina", "c11", map[string][2]string{format("adivina"): {"adivina.c", read("interactive-adivina/solutions/ac_binaria.c")}}},
		{cb, "adivina", "c11", map[string][2]string{format("adivina"): {"adivina.c", read("interactive-adivina/solutions/wa_uno.c")}}},
		{ca, "cuadrados", "", map[string][2]string{
			"output_01.txt": {"output_01.txt", read("output-only-cuadrados/tests/01.out")},
			"output_02.txt": {"output_02.txt", read("output-only-cuadrados/tests/02.out")},
			"output_03.txt": {"output_03.txt", read("output-only-cuadrados/tests/03.out")},
			"output_04.txt": {"output_04.txt", read("output-only-cuadrados/tests/04.out")}}},
		{cb, "cuadrados", "", map[string][2]string{"output_01.txt": {"output_01.txt", read("output-only-cuadrados/tests/01.out")}}},
	} {
		if code := sub.c.submitFiles(sub.task, sub.lang, sub.files); code != 200 {
			t.Fatalf("submit %s = %d", sub.task, code)
		}
	}
	scores := func(p sqlc.Participation, want map[string]float64, what string) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Minute)
		for {
			ok := true
			var got []string
			for name, w := range want {
				ts, err := s.q.GetParticipationTaskScore(bg, sqlc.GetParticipationTaskScoreParams{ParticipationID: p.ID, TaskID: byName[name].ID})
				got = append(got, fmt.Sprintf("%s=%v/%d", name, ts.Score, ts.Pending))
				if err != nil || ts.Score != w || ts.Pending != 0 {
					ok = false
				}
			}
			if ok {
				return
			}
			if time.Now().After(deadline) {
				logEvaluations(t, s)
				t.Fatalf("%s: %v, want %v", what, got, want)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	scores(ana, map[string]float64{"sum": 100, "adivina": 100, "cuadrados": 100}, "ana judged")
	// wa_uno always answers 1: right on one testcase of four.
	scores(beto, map[string]float64{"sum": 0, "adivina": 25, "cuadrados": 25}, "beto judged")

	// 3. A question, answered publicly, and an announcement.
	if code, body := cb.post("/e2e/questions", url.Values{"task": {"cuadrados"}, "text": {"¿Cuenta el salto de línea final?"}}); code != 200 || !strings.Contains(body, "Your question was sent.") {
		t.Fatalf("ask = %d\n%s", code, body)
	}
	qs, _ := s.q.ListQuestionsByParticipation(bg, beto.ID)
	if len(qs) != 1 {
		t.Fatalf("questions %+v", qs)
	}
	if code, _ := a.Post(fmt.Sprintf("/questions/%d/reply", qs[0].ID), url.Values{"text": {"No, se ignora."}, "public": {"on"}}); code != 200 {
		t.Fatalf("reply = %d", code)
	}
	if code, _ := a.Post("/contests/"+cid+"/announcements", url.Values{"subject": {"Quedan 30 minutos"}, "text": {"Revisen sus envíos."}}); code != 200 {
		t.Fatalf("announce = %d", code)
	}
	for _, c := range []*browser{ca, cb} {
		_, body := c.get("/e2e/communication")
		if !strings.Contains(body, "No, se ignora.") || !strings.Contains(body, "Quedan 30 minutos") {
			t.Fatalf("communication page:\n%s", body)
		}
	}

	// 4. Invalidate ana's normal I/O submission.
	subs, _ := s.q.ListSubmissionsByParticipationTask(bg, sqlc.ListSubmissionsByParticipationTaskParams{ParticipationID: ana.ID, TaskID: byName["sum"].ID})
	if code, _ := a.Post(fmt.Sprintf("/submissions/%d/invalidate", subs[0].ID), url.Values{"reason": {"Ensayo: envío anulado"}}); code != 200 {
		t.Fatalf("invalidate = %d", code)
	}
	scores(ana, map[string]float64{"sum": 0, "adivina": 100, "cuadrados": 100}, "after invalidating")
	if _, body := ca.get(fmt.Sprintf("/e2e/submissions/%d", subs[0].ID)); !strings.Contains(body, "Ensayo: envío anulado") {
		t.Fatal("the contestant does not see the reason")
	}

	// 5. Frozen: contestants see the ranking as it was at the freeze (no
	// points yet); the admin sees the real one; unfreezing shows it.
	_, frozen := ca.get("/e2e/ranking")
	if !strings.Contains(frozen, "frozen") || strings.Contains(frozen, ">200<") {
		t.Fatalf("frozen ranking:\n%s", frozen)
	}
	if _, body := a.Get("/contests/" + cid + "/ranking"); !strings.Contains(body, ">200<") || !strings.Contains(body, ">50<") {
		t.Fatalf("admin ranking:\n%s", body)
	}
	if code, _ := a.Post("/contests/"+cid+"/ranking/freeze", url.Values{"unfrozen": {"1"}}); code != 200 {
		t.Fatalf("unfreeze = %d", code)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		_, body := ca.get("/e2e/ranking")
		if strings.Contains(body, ">200<") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("unfrozen ranking:\n%s", body)
		}
		time.Sleep(500 * time.Millisecond)
	}

	// 6. The results: CSV and the printable PDF.
	code, csv := a.Get("/contests/" + cid + "/ranking.csv")
	lines := strings.Split(strings.TrimSpace(csv), "\n")
	if code != 200 || len(lines) != 3 || !strings.Contains(lines[1], "ana") || !strings.Contains(lines[1], "200") || !strings.Contains(lines[2], "beto") {
		t.Fatalf("results.csv = %d\n%s", code, csv)
	}
	code, doc := a.Get("/contests/" + cid + "/ranking.pdf")
	if n, err := pdf.CountPages([]byte(doc)); code != 200 || err != nil || n < 1 {
		t.Fatalf("results.pdf = %d, %d pages, %v", code, n, err)
	}
	if os.Getenv("CMS_DRILL_OUT") != "" { // keep the files for a look
		os.WriteFile(filepath.Join(os.Getenv("CMS_DRILL_OUT"), "results.csv"), []byte(csv), 0o644)
		os.WriteFile(filepath.Join(os.Getenv("CMS_DRILL_OUT"), "results.pdf"), []byte(doc), 0o644)
	}
}
