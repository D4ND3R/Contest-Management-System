package rankingweb

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/config"
	"github.com/D4ND3R/Contest-Management-System/internal/logging"
	"github.com/D4ND3R/Contest-Management-System/internal/ranking"
)

const token = "secret-token"

func newTest(t *testing.T, dir string, maxClients int) (*Server, *httptest.Server) {
	t.Helper()
	s, err := New(config.RankingWeb{DataDir: dir, PushToken: token, Title: "Rankings", MaxClients: maxClients}, logging.Discard())
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts
}

func push(t *testing.T, ts *httptest.Server, p ranking.Push) (int, int64) {
	t.Helper()
	body, _ := json.Marshal(p)
	req, _ := http.NewRequest("POST", ts.URL+"/push", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var ack struct{ Seq int64 }
	json.NewDecoder(resp.Body).Decode(&ack)
	return resp.StatusCode, ack.Seq
}

func get(t *testing.T, url string, hdr ...string) (int, http.Header, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(b)
}

func sampleBoard() *ranking.Board {
	start := time.Date(2030, 1, 1, 10, 0, 0, 0, time.UTC)
	return &ranking.Board{Contest: "omi", Title: "OMI 2030", Precision: 0, Start: start, Stop: start.Add(5 * time.Hour), Timezone: "UTC",
		Subtasks: true, Flags: true, Institutions: true,
		Tasks: []ranking.BoardTask{{Name: "suma", Title: "Suma", MaxScore: 100, SubtaskMax: []float64{40, 60}}},
		Rows: []ranking.BoardRow{
			{Key: "p1", Rank: 1, Name: "Ana", Institution: "UNAM", Total: 100, Cells: []ranking.BoardCell{{Score: 100, Subtasks: []float64{40, 60}, Submitted: true}}},
			{Key: "p2", Rank: 2, Name: "Beto", Total: 40, Cells: []ranking.BoardCell{{Score: 40, Subtasks: []float64{40, 0}, Submitted: true}}},
		}}
}

// sse opens the stream and returns its "event: data" lines.
func sse(t *testing.T, url string) (<-chan string, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("events: %v %v", err, resp)
	}
	ch := make(chan string, 16)
	go func() {
		br := bufio.NewReader(resp.Body)
		ev := ""
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				close(ch)
				return
			}
			switch {
			case strings.HasPrefix(line, "event: "):
				ev = strings.TrimSpace(line[7:])
			case strings.HasPrefix(line, "data: "):
				ch <- ev + " " + strings.TrimSpace(line[6:])
			}
		}
	}()
	time.Sleep(50 * time.Millisecond)
	return ch, cancel
}

func next(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(3 * time.Second):
		t.Fatal("no event")
		return ""
	}
}

func TestPushProtocolAndLiveRows(t *testing.T) {
	dir := t.TempDir()
	s, ts := newTest(t, dir, 100)
	// Pushes need the token; a delta needs a board.
	body, _ := json.Marshal(ranking.Push{Contest: "omi", Kind: "full", Board: sampleBoard()})
	resp, _ := http.Post(ts.URL+"/push", "application/json", bytes.NewReader(body))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("push without token = %d", resp.StatusCode)
	}
	if code, _ := push(t, ts, ranking.Push{Contest: "omi", Kind: "delta", Seq: 2, Base: 1}); code != http.StatusConflict {
		t.Fatalf("delta without board = %d", code)
	}
	hist := map[string][]ranking.Point{"p1": {{Time: time.Date(2030, 1, 1, 11, 0, 0, 0, time.UTC), Total: 40}, {Time: time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC), Total: 100}}}
	if code, seq := push(t, ts, ranking.Push{Contest: "omi", Kind: "full", Seq: 1, Board: sampleBoard(), History: hist}); code != 200 || seq != 1 {
		t.Fatalf("full = %d %d", code, seq)
	}
	code, _, body2 := get(t, ts.URL+"/omi/")
	if code != 200 || !strings.Contains(body2, `id="r-p1"`) || !strings.Contains(body2, "UNAM") || !strings.Contains(body2, "40 · 60") {
		t.Fatalf("board page = %d\n%s", code, body2)
	}
	if _, _, idx := get(t, ts.URL+"/"); !strings.Contains(idx, `href="/omi/"`) {
		t.Fatalf("index:\n%s", idx)
	}
	// JSON snapshot: gzip and ETag.
	code, h, gz := get(t, ts.URL+"/omi/ranking.json", "Accept-Encoding", "gzip")
	zr, err := gzip.NewReader(strings.NewReader(gz))
	if code != 200 || h.Get("Content-Encoding") != "gzip" || err != nil {
		t.Fatalf("json = %d %v", code, err)
	}
	var b ranking.Board
	if json.NewDecoder(zr).Decode(&b); len(b.Rows) != 2 {
		t.Fatalf("snapshot %+v", b)
	}
	if code, _, _ := get(t, ts.URL+"/omi/ranking.json", "If-None-Match", h.Get("ETag")); code != http.StatusNotModified {
		t.Fatalf("etag = %d", code)
	}
	// Live: a delta sends the changed row as HTML; Beto overtakes Ana.
	ch, stop := sse(t, ts.URL+"/omi/events")
	defer stop()
	beto := ranking.BoardRow{Key: "p2", Rank: 1, Name: "Beto", Total: 100, Cells: []ranking.BoardCell{{Score: 100, Subtasks: []float64{40, 60}, Submitted: true}}}
	ana := sampleBoard().Rows[0]
	ana.Rank = 1
	code, seq := push(t, ts, ranking.Push{Contest: "omi", Kind: "delta", Seq: 2, Base: 1, Rows: []ranking.BoardRow{ana, beto},
		History: map[string][]ranking.Point{"p2": {{Time: time.Date(2030, 1, 1, 13, 0, 0, 0, time.UTC), Total: 100}}}})
	if code != 200 || seq != 2 {
		t.Fatalf("delta = %d %d", code, seq)
	}
	m := next(t, ch)
	if !strings.HasPrefix(m, "rows ") || !strings.Contains(m, `"key":"p2"`) || !strings.Contains(m, `data-rank=\"1\"`) || strings.Contains(m, `"key":"p1"`) {
		t.Fatalf("rows event %s", m)
	}
	// A stale delta is refused with the current sequence.
	if code, seq := push(t, ts, ranking.Push{Contest: "omi", Kind: "delta", Seq: 9, Base: 7}); code != http.StatusConflict || seq != 2 {
		t.Fatalf("stale delta = %d %d", code, seq)
	}
	// A header change (the freeze) makes spectators reload.
	fb := sampleBoard()
	fb.Frozen = true
	push(t, ts, ranking.Push{Contest: "omi", Kind: "full", Seq: 3, Board: fb})
	if m := next(t, ch); !strings.HasPrefix(m, "reload") {
		t.Fatalf("header change event %s", m)
	}
	// Unfreezing reveals the rows instead: bottom-up, marked unfrozen.
	ub := sampleBoard()
	ub.Rows[0], ub.Rows[1] = ranking.BoardRow{Key: "p2", Rank: 1, Name: "Beto", Total: 100, Cells: beto.Cells}, ranking.BoardRow{Key: "p1", Rank: 2, Name: "Ana", Total: 90,
		Institution: "UNAM", Cells: []ranking.BoardCell{{Score: 90, Subtasks: []float64{40, 50}, Submitted: true}}}
	push(t, ts, ranking.Push{Contest: "omi", Kind: "full", Seq: 4, Board: ub})
	m = next(t, ch)
	if !strings.HasPrefix(m, "rows ") || !strings.Contains(m, `"unfrozen":true`) || strings.Index(m, `"key":"p2"`) > strings.Index(m, `"key":"p1"`) {
		t.Fatalf("unfreeze event %s", m)
	}
	// History page with a chart.
	code, _, page := get(t, ts.URL+"/omi/u/p1")
	if code != 200 || !strings.Contains(page, "<svg") || !strings.Contains(page, "2030-01-01 12:00") {
		t.Fatalf("history = %d\n%s", code, page)
	}
	// Persistence: a new server loads the saved boards.
	s.saveAll()
	s2, err := New(config.RankingWeb{DataDir: dir, PushToken: token}, logging.Discard())
	if err != nil {
		t.Fatal(err)
	}
	if bd := s2.board("omi", false); bd == nil || bd.seq != 4 || bd.b.Frozen || len(bd.history["p2"]) != 1 {
		t.Fatalf("reloaded board %+v", bd)
	}
}

func TestPrivateBoardsAssetsAndLimits(t *testing.T) {
	dir := t.TempDir()
	s, ts := newTest(t, dir, 1)
	key := "k3y"
	sum := sha256.Sum256([]byte(key))
	push(t, ts, ranking.Push{Contest: "staff", Kind: "full", Seq: 1, Board: sampleBoard(), KeyHash: hex.EncodeToString(sum[:])})
	if code, _, _ := get(t, ts.URL+"/staff/"); code != 404 {
		t.Fatalf("private board without key = %d", code)
	}
	code, h, _ := get(t, ts.URL+"/staff/?key="+key)
	if code != 200 || !strings.Contains(h.Get("Set-Cookie"), "rws_key_staff=") {
		t.Fatalf("private board with key = %d", code)
	}
	if _, _, idx := get(t, ts.URL+"/"); strings.Contains(idx, "/staff/") {
		t.Fatal("private board listed")
	}
	// Assets: only images whose digest matches.
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89")
	digest := sha256hex(png)
	put := func(d string, data []byte) int {
		req, _ := http.NewRequest("PUT", ts.URL+"/assets/"+d, bytes.NewReader(data))
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if put(digest, png) != http.StatusNoContent || put(digest, []byte("<script>")) != http.StatusBadRequest || put(sha256hex([]byte("x")), png) != http.StatusBadRequest {
		t.Fatal("asset upload checks")
	}
	if code, h, body := get(t, ts.URL+"/assets/"+digest); code != 200 || h.Get("Content-Type") != "image/png" || body != string(png) {
		t.Fatalf("asset = %d %s", code, h.Get("Content-Type"))
	}
	// At most one spectator.
	push(t, ts, ranking.Push{Contest: "omi", Kind: "full", Seq: 1, Board: sampleBoard()})
	_, stop := sse(t, ts.URL+"/omi/events")
	defer stop()
	if code, _, _ := get(t, ts.URL+"/omi/events"); code != http.StatusServiceUnavailable {
		t.Fatalf("second spectator = %d", code)
	}
	// Deleting a board.
	push(t, ts, ranking.Push{Contest: "staff", Kind: "delete"})
	if code, _, _ := get(t, ts.URL+"/staff/?key="+key); code != 404 {
		t.Fatalf("deleted board = %d", code)
	}
	s.saveAll()
	s3, _ := New(config.RankingWeb{DataDir: dir, PushToken: token}, logging.Discard())
	if s3.board("omi", false) == nil || s3.board("staff", false) != nil {
		t.Fatal("persistence")
	}
}
