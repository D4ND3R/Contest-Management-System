package adminweb

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"net/url"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
)

// TestSubmissionFiltersSourceAndZip (SPEC_CLOSE D1): verdict and date
// filters, highlighted sources, and the zip of the filtered submissions.
func TestSubmissionFiltersSourceAndZip(t *testing.T) {
	f := newFixture(t)
	f.pool.Exec(bg, "UPDATE contests SET timezone = 'America/Mexico_City' WHERE id = $1", f.contest.ID)
	f.pool.Exec(bg, "UPDATE submission_results SET verdict = 'AC' WHERE submission_id = $1", f.subs[0])
	f.pool.Exec(bg, "UPDATE submissions SET submitted_at = '2030-01-01 18:00:00+00' WHERE id = $1", f.subs[0])
	f.pool.Exec(bg, "UPDATE submissions SET submitted_at = '2030-01-01 20:00:00+00' WHERE id = $1", f.subs[1])
	b := f.login("read_only")
	list := func(q string) string {
		t.Helper()
		code, body := b.Get(fmt.Sprintf("/contests/%d/submissions?%s", f.contest.ID, q))
		if code != 200 {
			t.Fatalf("list %s = %d\n%s", q, code, body)
		}
		return body
	}
	has := func(body string, id int64) bool { return strings.Contains(body, fmt.Sprintf(`id="sub-%d"`, id)) }
	if body := list("verdict=AC"); !has(body, f.subs[0]) || has(body, f.subs[1]) {
		t.Errorf("verdict filter:\n%s", body)
	}
	if body := list("verdict=WA"); has(body, f.subs[0]) || has(body, f.subs[1]) {
		t.Error("verdict WA matched")
	}
	// 18:00 UTC is 12:00 in Mexico City: from 13:00 local keeps only the
	// 20:00 UTC (14:00 local) one.
	if body := list("from=2030-01-01T13:00"); has(body, f.subs[0]) || !has(body, f.subs[1]) {
		t.Errorf("from filter:\n%s", body)
	}
	if body := list("to=2030-01-01T13:00"); !has(body, f.subs[0]) || has(body, f.subs[1]) {
		t.Errorf("to filter:\n%s", body)
	}
	if body := list("from=garbage&verdict=nope"); !has(body, f.subs[0]) || !has(body, f.subs[1]) {
		t.Error("invalid filters must be ignored")
	}
	// Highlighted source with line numbers.
	_, body := b.Get(fmt.Sprintf("/submissions/%d", f.subs[1]))
	if !strings.Contains(body, `<pre class="code hl">`) || !strings.Contains(body, `<span class="k">int</span> main()`) ||
		strings.Count(body, `<span class="l">`) < 4 {
		t.Errorf("source view:\n%s", body)
	}
	// The zip: index.csv plus task/user/id/file.
	zipOf := func(q string) (*zip.Reader, map[string]string) {
		t.Helper()
		code, body := b.Get(fmt.Sprintf("/contests/%d/submissions.zip?%s", f.contest.ID, q))
		if code != 200 {
			t.Fatalf("zip %s = %d", q, code)
		}
		zr, err := zip.NewReader(bytes.NewReader([]byte(body)), int64(len(body)))
		if err != nil {
			t.Fatal(err)
		}
		files := map[string]string{}
		for _, zf := range zr.File {
			rd, _ := zf.Open()
			data, _ := io.ReadAll(rd)
			files[zf.Name] = string(data)
		}
		return zr, files
	}
	_, files := zipOf(url.Values{"task": {fmt.Sprint(f.task.ID)}}.Encode())
	index, err := csv.NewReader(strings.NewReader(files["index.csv"])).ReadAll()
	if err != nil || len(index) != 3 || index[0][0] != "id" {
		t.Fatalf("index %v %v", index, err)
	}
	for _, id := range f.subs {
		name := fmt.Sprintf("sum/ana/%d/sum.c", id)
		if !strings.Contains(files[name], "return 0;") {
			t.Errorf("zip lacks %s: %v", name, keys(files))
		}
	}
	if _, files := zipOf("verdict=AC"); len(files) != 2 || !strings.Contains(files["index.csv"], ",AC,") {
		t.Errorf("filtered zip %v\n%s", keys(files), files["index.csv"])
	}
	rows, _ := f.q.ListAuditLog(bg, sqlc.ListAuditLogParams{Limit: 50})
	found := false
	for _, r := range rows {
		found = found || r.Action == "submissions.download"
	}
	if !found {
		t.Error("zip download not audited")
	}
	if got := zipSafe("../x/y\\z"); got != ".._x_y_z" {
		t.Errorf("zipSafe %q", got)
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
