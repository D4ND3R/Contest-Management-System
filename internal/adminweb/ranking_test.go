package adminweb

import (
	"bytes"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/rankingpush"
	"github.com/D4ND3R/Contest-Management-System/internal/webtest"
)

// TestRankingSettings covers the per-contest ranking configuration, the
// manual unfreeze and the scoreboard links (A2).
func TestRankingSettings(t *testing.T) {
	f := newFixture(t)
	b := f.login("all")
	code, body := b.Post("/contests", url.Values{"name": {"rk"}, "start_time": {"2030-05-01T09:00"}, "stop_time": {"2030-05-01T14:00"},
		"token_mode": {"disabled"}, "scoring_mode": {"ioi"}})
	webtest.MustOK(t, "create contest", code, body)
	c, _ := f.q.GetContestByName(bg, "rk")
	if c.RankingVisibility != "public" || c.RankingContestantView != "full" || !c.RankingShowSubtasks || c.QuestionsPerMinute != 3 ||
		c.RankingTieBreak != "shared" {
		t.Fatalf("defaults %+v", c)
	}
	settings := url.Values{"name": {"rk"}, "start_time": {"2030-05-01T09:00"},
		"stop_time": {"2030-05-01T14:00"}, "token_mode": {"disabled"}, "scoring_mode": {"ioi"},
		"ranking_visibility": {"admins"}, "ranking_contestant_view": {"own"}, "ranking_when": {"after"}, "ranking_freeze_minutes": {"60"},
		"ranking_show_flags": {"on"}, "ranking_anonymous": {"on"}, "ranking_tie_break": {"time"}, "ranking_show_photos": {"on"}}
	code, body = b.Post(fmt.Sprintf("/contests/%d", c.ID), settings)
	webtest.MustOK(t, "save ranking settings", code, body)
	c, _ = f.q.GetContestByName(bg, "rk")
	if c.RankingVisibility != "admins" || c.RankingContestantView != "own" || c.RankingWhen != "after" || c.RankingFreezeMinutes != 60 ||
		c.RankingShowSubtasks || !c.RankingShowFlags || !c.RankingAnonymous || c.RankingTieBreak != "time" || !c.RankingShowPhotos {
		t.Fatalf("settings %+v", c)
	}
	// The form's single question (SPEC_MIN §11): a stored combination that
	// is none of its answers is offered as "keep", and kept.
	_, body = b.Get(fmt.Sprintf("/contests/%d/settings", c.ID))
	if !strings.Contains(body, `<option value="keep" selected>keep the current setting (admins, own)</option>`) {
		t.Fatalf("settings form:\n%s", body)
	}
	form := url.Values{"name": {"rk"}, "start_time": {"2030-05-01T09:00"}, "stop_time": {"2030-05-01T14:00"}, "token_mode": {"disabled"},
		"scoring_mode": {"ioi"}, "ranking_preset": {"keep"}, "ranking_when": {"after"}}
	code, body = b.Post(fmt.Sprintf("/contests/%d", c.ID), form)
	webtest.MustOK(t, "keep", code, body)
	if c, _ = f.q.GetContestByName(bg, "rk"); c.RankingVisibility != "admins" || c.RankingContestantView != "own" {
		t.Fatalf("kept %s/%s", c.RankingVisibility, c.RankingContestantView)
	}
	for preset, want := range map[string][2]string{"public": {"public", "full"}, "own": {"contestants", "own"}, "staff": {"admins", "none"}, "hidden": {"hidden", "none"}} {
		form.Set("ranking_preset", preset)
		code, body := b.Post(fmt.Sprintf("/contests/%d", c.ID), form)
		webtest.MustOK(t, preset, code, body)
		if c, _ = f.q.GetContestByName(bg, "rk"); c.RankingVisibility != want[0] || c.RankingContestantView != want[1] {
			t.Errorf("%s: %s/%s", preset, c.RankingVisibility, c.RankingContestantView)
		}
	}
	form.Set("ranking_preset", "everyone")
	if code, _ := b.Post(fmt.Sprintf("/contests/%d", c.ID), form); code != 422 {
		t.Fatalf("unknown preset = %d", code)
	}
	code, body = b.Post(fmt.Sprintf("/contests/%d", c.ID), settings)
	webtest.MustOK(t, "back to the settings", code, body)
	if code, _ = b.Post(fmt.Sprintf("/contests/%d", c.ID), url.Values{"name": {"rk"}, "start_time": {"2030-05-01T09:00"},
		"stop_time": {"2030-05-01T14:00"}, "token_mode": {"disabled"}, "scoring_mode": {"ioi"}, "ranking_visibility": {"everyone"}}); code != 422 {
		t.Fatalf("invalid visibility = %d", code)
	}
	if code, _ = b.Post(fmt.Sprintf("/contests/%d", c.ID), url.Values{"name": {"rk"}, "start_time": {"2030-05-01T09:00"},
		"stop_time": {"2030-05-01T14:00"}, "token_mode": {"disabled"}, "scoring_mode": {"ioi"}, "ranking_tie_break": {"coin"}}); code != 422 {
		t.Fatalf("invalid tie-break = %d", code)
	}
	// The ranking page: the secret scoreboard link and the unfreeze button.
	_, body = b.Get(fmt.Sprintf("/contests/%d/ranking", c.ID))
	key := rankingpush.BoardKey(bytes.Repeat([]byte("a"), 32), "rk")
	if !strings.Contains(body, "https://ranking.example.org/rk/?key="+key) || !strings.Contains(body, "Unfreeze now") {
		t.Fatalf("ranking page:\n%s", body)
	}
	code, body = b.Post(fmt.Sprintf("/contests/%d/ranking/freeze", c.ID), url.Values{"unfrozen": {"1"}})
	if c, _ = f.q.GetContestByName(bg, "rk"); code != 200 || !c.RankingUnfrozen || !strings.Contains(body, "Freeze again") {
		t.Fatalf("unfreeze = %d", code)
	}
	b.Post(fmt.Sprintf("/contests/%d/ranking/freeze", c.ID), url.Values{"unfrozen": {"0"}})
	if c, _ = f.q.GetContestByName(bg, "rk"); c.RankingUnfrozen {
		t.Fatal("freeze again")
	}
	if code, _ := f.login("messaging").Post(fmt.Sprintf("/contests/%d/ranking/freeze", c.ID), url.Values{"unfrozen": {"1"}}); code != 403 {
		t.Fatalf("messaging unfreeze = %d", code)
	}
}
