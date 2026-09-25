package contestweb

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/webtest"
)

func zipBytes(files map[string]string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		w, _ := zw.Create(name)
		w.Write([]byte(data))
	}
	zw.Close()
	return buf.Bytes()
}

// TestOutputOnlySubmissions covers the output-only rules of SPEC_AUDIT §2.5:
// one file per testcase or a zip, file names validated, partial submissions
// and, when enabled, missing outputs taken from the best previous result.
func TestOutputOnlySubmissions(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	tp := db.NewTaskParams("outs", "Outputs")
	num := int32(1)
	tp.ContestID, tp.Num, tp.SubmissionFormat = &f.contest.ID, &num, []string{}
	task, err := f.q.CreateTask(bg, tp)
	if err != nil {
		t.Fatal(err)
	}
	dp := db.NewDatasetParams(task.ID, "v1")
	dp.TaskType, dp.TaskTypeParams = "OutputOnly", json.RawMessage(`{"merge_previous": true}`)
	dp.ScoreType, dp.ScoreTypeParams = "Sum", json.RawMessage(`10`)
	ds, _ := f.q.CreateDataset(bg, dp)
	f.q.SetActiveDataset(bg, sqlc.SetActiveDatasetParams{ID: task.ID, ActiveDatasetID: &ds.ID})
	var tcs []sqlc.Testcase
	for i := 0; i < 3; i++ {
		in, _ := f.store.PutBytes(bg, []byte(fmt.Sprint(i)))
		tc, _ := f.q.UpsertTestcase(bg, sqlc.UpsertTestcaseParams{DatasetID: ds.ID, Codename: fmt.Sprint(i), InputDigest: in.Digest, OutputDigest: in.Digest})
		tcs = append(tcs, tc)
	}

	b := webtest.New(t, f.url)
	b.Get("/ioi/login")
	code, body := b.Post("/ioi/login", url.Values{"username": {"ana"}, "password": {"secret"}})
	webtest.MustOK(t, "login", code, body)
	code, body = b.Get("/ioi/tasks/outs")
	webtest.MustOK(t, "task page", code, body)
	for _, name := range []string{"output_0.txt", "output_1.txt", "output_2.txt", `name="zip"`} {
		if !strings.Contains(body, name) {
			t.Fatalf("task page lacks %s:\n%s", name, body)
		}
	}
	submit := func(fields map[string]string, files ...webtest.File) (int, string) {
		return b.PostMultipart("/ioi/tasks/outs/submit", fields, files...)
	}
	filesOf := func(id int64) map[string]string {
		fs, _ := f.q.ListSubmissionFiles(bg, id)
		out := map[string]string{}
		for _, x := range fs {
			out[x.Filename] = x.Digest
		}
		return out
	}
	subs := func() []sqlc.Submission {
		s, _ := f.q.ListSubmissionsByParticipationTask(bg, sqlc.ListSubmissionsByParticipationTaskParams{ParticipationID: f.part.ID, TaskID: task.ID})
		sort.Slice(s, func(i, j int) bool { return s[i].ID < s[j].ID })
		return s
	}

	// Unknown names inside the archive are rejected.
	code, body = submit(nil, webtest.File{Field: "zip", Name: "out.zip", Data: zipBytes(map[string]string{"output_0.txt": "0", "notes.txt": "x"})})
	if code != 400 || !strings.Contains(body, "notes.txt") {
		t.Fatalf("unexpected file accepted: %d %s", code, body)
	}
	// A partial zip (two of three outputs, in a folder) is accepted.
	code, body = submit(nil, webtest.File{Field: "zip", Name: "out.zip", Data: zipBytes(map[string]string{"res/output_0.txt": "0", "res/output_1.txt": "wrong"})})
	webtest.MustOK(t, "zip submission", code, body)
	s1 := subs()[0]
	if got := filesOf(s1.ID); len(got) != 2 || got["output_0.txt"] == "" || got["output_1.txt"] == "" {
		t.Fatalf("files of the zip submission: %v", got)
	}
	// Judge it: output 0 correct, output 1 wrong.
	eval := func(sub int64, tc sqlc.Testcase, outcome float64) {
		f.q.EnsureSubmissionResult(bg, sqlc.EnsureSubmissionResultParams{SubmissionID: sub, DatasetID: ds.ID})
		if err := f.q.UpsertEvaluation(bg, sqlc.UpsertEvaluationParams{SubmissionID: sub, DatasetID: ds.ID, TestcaseID: tc.ID,
			Outcome: outcome, ExitStatus: "ok"}); err != nil {
			t.Fatal(err)
		}
	}
	eval(s1.ID, tcs[0], 1)
	eval(s1.ID, tcs[1], 0)

	// Individual files: only output 1 (now correct). Output 0 is merged
	// from submission 1; output 2 was never submitted.
	right1, _ := f.store.PutBytes(bg, []byte("1"))
	code, body = submit(nil, webtest.File{Field: "output_1.txt", Name: "output_1.txt", Data: []byte("1")})
	webtest.MustOK(t, "single file", code, body)
	s2 := subs()[1]
	got := filesOf(s2.ID)
	if len(got) != 2 || got["output_0.txt"] != filesOf(s1.ID)["output_0.txt"] || got["output_1.txt"] != right1.Digest {
		t.Fatalf("merged submission files: %v", got)
	}
	eval(s2.ID, tcs[0], 1)
	eval(s2.ID, tcs[1], 1)

	// Output 2 only: 0 and 1 come from the best previous results
	// (output 1 from submission 2, not the wrong one of submission 1).
	code, body = submit(nil, webtest.File{Field: "zip", Name: "o.zip", Data: zipBytes(map[string]string{"output_2.txt": "2"})})
	webtest.MustOK(t, "third submission", code, body)
	s3 := subs()[2]
	got = filesOf(s3.ID)
	if len(got) != 3 || got["output_1.txt"] != right1.Digest {
		t.Fatalf("best-previous merge: %v", got)
	}

	// Without merge_previous, partial submissions stay partial.
	up := db.DatasetToUpdate(ds)
	up.TaskTypeParams = json.RawMessage(`{}`)
	f.q.UpdateDataset(bg, up)
	f.srv.cache.invalidateContest(0)
	code, body = submit(nil, webtest.File{Field: "output_2.txt", Name: "o2.txt", Data: []byte("2")})
	webtest.MustOK(t, "no merge", code, body)
	if got := filesOf(subs()[3].ID); len(got) != 1 {
		t.Fatalf("submission without merge has %d files", len(got))
	}
}
