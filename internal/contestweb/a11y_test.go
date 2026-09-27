package contestweb

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/webtest"
)

// TestAccessibility renders the contestant pages and checks them with the
// shared accessibility rules (SPEC_IOI §8), in both writing directions.
func TestAccessibility(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.setContest(t, "allow_printing = true")
	c := f.client()
	var sub string
	pages := func(label string) {
		for _, p := range []string{"/ioi/", "/ioi/tasks/sum", "/ioi/communication", "/ioi/documentation", "/ioi/testing", "/ioi/printing",
			"/ioi/ranking", "/ioi/submissions/" + sub} {
			code, body := f.get(c, p)
			if code != http.StatusOK {
				t.Errorf("%s %s: %d", label, p, code)
				continue
			}
			webtest.A11y(t, label+" "+p, body, true)
		}
	}
	_, login := f.get(c, "/ioi/login")
	webtest.A11y(t, "login", login, true)
	_, list := f.get(c, "/")
	webtest.A11y(t, "contest list", list, true)
	_, page := f.login(c, "ana", "secret")
	if code, body := f.submit(c, csrfOf(t, page), "c11", "int main(){}", true); code != http.StatusOK {
		t.Fatalf("submit: %d %s", code, body)
	}
	var id int64
	f.pool.QueryRow(bg, "SELECT max(id) FROM submissions").Scan(&id)
	sub = strconv.FormatInt(id, 10)
	for _, frag := range []string{"/ioi/submissions/" + sub + "/card", "/ioi/tasks/sum/submissions"} {
		_, body := f.get(c, frag, "HX-Request", "true")
		webtest.A11y(t, frag, body, false)
	}
	pages("en")
	_, page = f.get(c, "/ioi/")
	csrf := csrfOf(t, page)
	f.post(c, "/ioi/lang", url.Values{"csrf": {csrf}, "lang": {"ar"}})
	_, page = f.get(c, "/ioi/")
	if !strings.Contains(page, `<html lang="ar" dir="rtl"`) {
		t.Fatalf("Arabic page: %.300s", page)
	}
	pages("ar")
}

// TestDisplayPreferences covers the theme, text size and language choice:
// one form, cookies that apply before logging in too.
func TestDisplayPreferences(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	c := f.client()
	_, page := f.get(c, "/ioi/login")
	if !strings.Contains(page, `<html lang="en" dir="ltr">`) || !strings.Contains(page, `name="theme"`) {
		t.Fatalf("login page: %.400s", page)
	}
	f.post(c, "/ioi/lang", url.Values{"csrf": {csrfOf(t, page)}, "lang": {"es"}, "theme": {"contrast"}, "size": {"xl"}})
	_, page = f.get(c, "/ioi/login")
	if !strings.Contains(page, `<html lang="es" dir="ltr" data-theme="contrast" data-size="xl">`) {
		t.Fatalf("preferences not applied: %.300s", page)
	}
	f.login(c, "ana", "secret")
	_, page = f.get(c, "/ioi/")
	if !strings.Contains(page, `data-theme="contrast" data-size="xl"`) || !strings.Contains(page, `value="contrast" selected`) {
		t.Fatalf("after login: %.300s", page)
	}
	// Unknown values fall back to the defaults.
	f.post(c, "/ioi/lang", url.Values{"csrf": {csrfOf(t, page)}, "theme": {"neon"}, "size": {"huge"}})
	_, page = f.get(c, "/ioi/")
	if strings.Contains(page, "data-theme") || strings.Contains(page, "data-size") {
		t.Fatalf("bad values kept: %.300s", page)
	}
}

// TestEditorSubmission sends the source typed in the page's editor.
func TestEditorSubmission(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	c := f.client()
	f.login(c, "ana", "secret")
	_, page := f.get(c, "/ioi/tasks/sum")
	if !strings.Contains(page, `name="source"`) || !strings.Contains(page, `data-exts=".c"`) {
		t.Fatalf("no editor or extensions on the task page")
	}
	send := func(fields map[string]string) (int, string) {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		for k, v := range fields {
			mw.WriteField(k, v)
		}
		mw.Close()
		req, _ := http.NewRequest("POST", f.url+"/ioi/tasks/sum/submit", &body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("HX-Request", "true")
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	csrf := csrfOf(t, page)
	if code, body := send(map[string]string{"csrf": csrf, "language": "c11", "source": "   \r\n"}); code == http.StatusOK || !strings.Contains(body, "Every file") {
		t.Fatalf("blank editor accepted: %d %s", code, body)
	}
	if code, body := send(map[string]string{"csrf": csrf, "language": "c11", "source": "int main() {\r\n  return 0;\r\n}\r\n"}); code != http.StatusOK {
		t.Fatalf("editor submission: %d %s", code, body)
	}
	var digest string
	if err := f.pool.QueryRow(bg, `SELECT f.digest FROM submission_files f JOIN submissions s ON s.id = f.submission_id
		WHERE s.participation_id = $1 AND f.filename = 'sum.%l'`, f.part.ID).Scan(&digest); err != nil {
		t.Fatal(err)
	}
	rd, err := f.store.Open(bg, digest)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rd)
	rd.Close()
	if string(got) != "int main() {\n  return 0;\n}\n" {
		t.Fatalf("stored source %q", got)
	}
}
