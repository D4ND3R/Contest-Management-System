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
	if c.RankingVisibility != "public" || c.RankingContestantView != "full" || !c.RankingShowSubtasks || c.QuestionsPerMinute != 3 {
		t.Fatalf("defaults %+v", c)
	}
	code, body = b.Post(fmt.Sprintf("/contests/%d", c.ID), url.Values{"name": {"rk"}, "start_time": {"2030-05-01T09:00"},
		"stop_time": {"2030-05-01T14:00"}, "token_mode": {"disabled"}, "scoring_mode": {"ioi"},
		"ranking_visibility": {"admins"}, "ranking_contestant_view": {"own"}, "ranking_when": {"after"}, "ranking_freeze_minutes": {"60"},
		"ranking_show_flags": {"on"}, "ranking_anonymous": {"on"}})
	webtest.MustOK(t, "save ranking settings", code, body)
	c, _ = f.q.GetContestByName(bg, "rk")
	if c.RankingVisibility != "admins" || c.RankingContestantView != "own" || c.RankingWhen != "after" || c.RankingFreezeMinutes != 60 ||
		c.RankingShowSubtasks || !c.RankingShowFlags || !c.RankingAnonymous {
		t.Fatalf("settings %+v", c)
	}
	if code, _ = b.Post(fmt.Sprintf("/contests/%d", c.ID), url.Values{"name": {"rk"}, "start_time": {"2030-05-01T09:00"},
		"stop_time": {"2030-05-01T14:00"}, "token_mode": {"disabled"}, "scoring_mode": {"ioi"}, "ranking_visibility": {"everyone"}}); code != 422 {
		t.Fatalf("invalid visibility = %d", code)
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
