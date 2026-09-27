package rankingweb

import (
	"net/http"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/ranking"
	"github.com/D4ND3R/Contest-Management-System/internal/webtest"
)

// TestAccessibility checks the public scoreboard pages, and that the
// preference form works without cookies from the other sites.
func TestAccessibility(t *testing.T) {
	_, ts := newTest(t, t.TempDir(), 10)
	b := sampleBoard()
	b.Rows[0].Medal, b.Rows[1].Unofficial = "gold", true
	push(t, ts, ranking.Push{Contest: "omi", Kind: "full", Seq: 1, Board: b})
	for _, p := range []string{"/", "/omi/", "/omi/u/" + b.Rows[0].Key} {
		code, _, body := get(t, ts.URL+p)
		if code != http.StatusOK {
			t.Fatalf("%s: %d", p, code)
		}
		webtest.A11y(t, p, body, true)
	}
	code, hdr, body := get(t, ts.URL+"/omi/?lang=ar&theme=light&size=xxl")
	if code != http.StatusOK || !strings.Contains(body, `<html lang="ar" dir="rtl" data-theme="light" data-size="xxl">`) {
		t.Fatalf("preferences: %d %.200s", code, body)
	}
	if !strings.Contains(strings.Join(hdr.Values("Set-Cookie"), ";"), "cms_display=light.xxl") {
		t.Fatalf("cookies %v", hdr.Values("Set-Cookie"))
	}
	webtest.A11y(t, "arabic board", body, true)
}
