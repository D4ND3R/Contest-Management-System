package rankingpush

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/db"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/logging"
	"github.com/D4ND3R/Contest-Management-System/internal/ranking"
	"github.com/D4ND3R/Contest-Management-System/internal/rankingweb"
	"github.com/D4ND3R/Contest-Management-System/internal/testutil"
)

var bg = context.Background()

func ptr[T any](v T) *T { return &v }

func getJSON(t *testing.T, url string) (int, *ranking.Board) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, nil
	}
	var b ranking.Board
	if err := json.NewDecoder(resp.Body).Decode(&b); err != nil {
		t.Fatal(err)
	}
	return 200, &b
}

// TestPusherFeedsRankingWeb covers the pusher end to end: full boards,
// deltas with history, recovery when a server loses its state, the freeze,
// administrator-only boards and visibility changes.
func TestPusherFeedsRankingWeb(t *testing.T) {
	pool := testutil.DB(t)
	rdb, ns := testutil.Redis(t)
	q := sqlc.New(pool)
	rws, err := rankingweb.New(config.RankingWeb{DataDir: t.TempDir(), PushToken: "tok"}, logging.Discard())
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(rws.Handler())
	defer ts.Close()
	p := New(pool, rdb, blob.NewMem(), logging.Discard(), Options{URLs: []string{ts.URL}, Token: "tok", Secret: []byte("s3cret"), Namespace: ns})

	now := time.Now().UTC()
	c, _ := q.CreateContest(bg, db.NewContestParams("omi", now.Add(-time.Hour), now.Add(2*time.Hour)))
	tp := db.NewTaskParams("suma", "Suma")
	tp.ContestID, tp.Num = &c.ID, ptr(int32(0))
	task, _ := q.CreateTask(bg, tp)
	ds, _ := q.CreateDataset(bg, db.NewDatasetParams(task.ID, "d"))
	q.SetActiveDataset(bg, sqlc.SetActiveDatasetParams{ID: task.ID, ActiveDatasetID: &ds.ID})
	for _, code := range []string{"1", "2"} {
		q.UpsertTestcase(bg, sqlc.UpsertTestcaseParams{DatasetID: ds.ID, Codename: code, InputDigest: strings.Repeat("0", 64), OutputDigest: strings.Repeat("0", 64)})
	}
	parts := map[string]int64{}
	for _, name := range []string{"ana", "beto"} {
		u, _ := q.CreateUser(bg, sqlc.CreateUserParams{Username: name, FirstName: name, PasswordHash: "x", PreferredLanguages: []string{}})
		pt, _ := q.CreateParticipation(bg, sqlc.CreateParticipationParams{ContestID: c.ID, UserID: u.ID, Ip: []netip.Prefix{}})
		parts[name] = pt.ID
	}
	// score records a scored submission and the aggregate the dispatcher
	// would store, and tells the pusher (as the ranking stream does).
	score := func(who string, at time.Time, v float64) {
		s, err := q.CreateSubmission(bg, sqlc.CreateSubmissionParams{ParticipationID: ptr(parts[who]), TaskID: task.ID, SubmittedAt: at, Official: true})
		if err != nil {
			t.Fatal(err)
		}
		pool.Exec(bg, `INSERT INTO submission_results (submission_id, dataset_id, compilation_outcome, score, ranking_score_details, scored_at)
			VALUES ($1, $2, 'ok', $3, jsonb_build_array($3::float8), now())`, s.ID, ds.ID, v)
		det, _ := json.Marshal([]float64{v})
		q.UpsertParticipationTaskScore(bg, sqlc.UpsertParticipationTaskScoreParams{ParticipationID: parts[who], TaskID: task.ID, Score: v,
			SubtaskScores: det, LastSubmissionAt: &at})
		p.mu.Lock()
		st := p.state(c.ID)
		st.dirty = true
		if st.touched == nil {
			st.touched = map[int64]time.Time{}
		}
		st.touched[parts[who]] = at
		p.mu.Unlock()
	}
	score("ana", now.Add(-30*time.Minute), 1)
	if err := p.discover(bg, true); err != nil {
		t.Fatal(err)
	}
	p.Flush(bg)
	code, b := getJSON(t, ts.URL+"/omi/ranking.json")
	if code != 200 || len(b.Rows) != 2 || b.Rows[0].Name != "ana" || b.Rows[0].Total != 1 {
		t.Fatalf("first board %d %+v", code, b)
	}
	// A delta (live ranking from the aggregates) with a history point.
	time.Sleep(p.opts.LiveInterval)
	score("beto", now.Add(-10*time.Minute), 2)
	p.Flush(bg)
	if _, b = getJSON(t, ts.URL+"/omi/ranking.json"); b.Rows[0].Name != "beto" || b.Rows[1].Rank != 2 {
		t.Fatalf("after delta %+v", b.Rows)
	}
	resp, _ := http.Get(ts.URL + "/omi/u/" + ranking.ParticipationKey(parts["beto"]) + "?format=json")
	var pts []ranking.Point
	json.NewDecoder(resp.Body).Decode(&pts)
	resp.Body.Close()
	if len(pts) != 1 || pts[0].Total != 2 {
		t.Fatalf("history %+v", pts)
	}
	// The server loses the board: the next delta is refused and a full
	// board follows.
	req, _ := http.NewRequest("POST", ts.URL+"/push", strings.NewReader(`{"contest":"omi","kind":"delete"}`))
	req.Header.Set("Authorization", "Bearer tok")
	http.DefaultClient.Do(req)
	time.Sleep(p.opts.LiveInterval)
	score("ana", now.Add(-5*time.Minute), 2)
	p.Flush(bg) // conflict
	time.Sleep(p.opts.LiveInterval)
	p.Flush(bg) // full
	if code, b = getJSON(t, ts.URL+"/omi/ranking.json"); code != 200 || b.Rows[0].Rank != 1 || b.Rows[1].Rank != 1 {
		t.Fatalf("after recovery %d %+v", code, b)
	}
	// Freeze the last 3 hours (so it is frozen now): later results hide.
	pool.Exec(bg, "UPDATE contests SET ranking_freeze_minutes = 180 WHERE id = $1", c.ID)
	pool.Exec(bg, "UPDATE contests SET stop_time = $2 WHERE id = $1", c.ID, now.Add(2*time.Hour+40*time.Minute)) // frozen since 20 minutes ago
	p.mu.Lock()
	p.state(c.ID).full, p.state(c.ID).dirty = true, true
	p.mu.Unlock()
	p.Flush(bg)
	if _, b = getJSON(t, ts.URL+"/omi/ranking.json"); !b.Frozen || b.Rows[0].Name != "ana" || b.Rows[0].Total != 1 || b.Rows[0].Cells[0].Pending != 1 {
		t.Fatalf("frozen %+v", b)
	}
	// Administrators only: the board needs the key.
	pool.Exec(bg, "UPDATE contests SET ranking_visibility = 'admins', ranking_freeze_minutes = 0 WHERE id = $1", c.ID)
	p.mu.Lock()
	p.state(c.ID).full, p.state(c.ID).dirty = true, true
	p.mu.Unlock()
	p.Flush(bg)
	if code, _ := getJSON(t, ts.URL+"/omi/ranking.json"); code != 404 {
		t.Fatalf("private board without key = %d", code)
	}
	if code, _ := getJSON(t, ts.URL+"/omi/ranking.json?key="+BoardKey([]byte("s3cret"), "omi")); code != 200 {
		t.Fatalf("private board with key = %d", code)
	}
	// Contestants only: removed from the ranking web servers.
	pool.Exec(bg, "UPDATE contests SET ranking_visibility = 'contestants' WHERE id = $1", c.ID)
	p.mu.Lock()
	p.state(c.ID).full, p.state(c.ID).dirty = true, true
	p.mu.Unlock()
	p.Flush(bg)
	if code, _ := getJSON(t, ts.URL+"/omi/ranking.json?key="+BoardKey([]byte("s3cret"), "omi")); code != 404 {
		t.Fatalf("board still published = %d", code)
	}
}
