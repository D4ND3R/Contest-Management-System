package rankingpush

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
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
	"github.com/D4ND3R/Contest-Management-System/internal/rankingweb"
	"github.com/D4ND3R/Contest-Management-System/internal/testutil"
)

// picture is a w×h PNG, red on the left half and blue on the right.
func picture(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{255, 0, 0, 255}
			if x >= w/2 {
				c = color.RGBA{0, 0, 255, 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

func TestThumbnail(t *testing.T) {
	small, err := thumbnail(picture(600, 400))
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(small))
	if err != nil {
		t.Fatal(err)
	}
	// The centre square, scaled to 128: red then blue, split in the middle.
	if b := img.Bounds(); b.Dx() != thumbSize || b.Dy() != thumbSize {
		t.Fatalf("size %v", b)
	}
	if r, _, bl, _ := img.At(10, 64).RGBA(); r>>8 < 200 || bl>>8 > 60 {
		t.Fatalf("left is not red: %v", img.At(10, 64))
	}
	if r, _, bl, _ := img.At(118, 64).RGBA(); bl>>8 < 200 || r>>8 > 60 {
		t.Fatalf("right is not blue: %v", img.At(118, 64))
	}
	// Small pictures are not enlarged; other data is refused.
	if s, _ := thumbnail(picture(40, 60)); s == nil {
		t.Fatal("small picture refused")
	} else if img, _ := jpeg.Decode(bytes.NewReader(s)); img.Bounds().Dx() != 40 {
		t.Fatalf("small picture resized to %v", img.Bounds())
	}
	if _, err := thumbnail([]byte("%PDF-1.4")); err == nil {
		t.Fatal("not an image, accepted")
	}
	// A PNG header declaring 100000×100000 pixels is refused before decoding.
	bomb := picture(1, 1)
	binary.BigEndian.PutUint32(bomb[16:], 100000)
	binary.BigEndian.PutUint32(bomb[20:], 100000)
	binary.BigEndian.PutUint32(bomb[29:], crc32.ChecksumIEEE(bomb[12:29])) // IHDR checksum
	if _, err := thumbnail(bomb); err == nil || !strings.Contains(err.Error(), "pixels") {
		t.Fatalf("huge canvas: %v", err)
	}
}

// TestScoreboardPhotos: with photos on, the scoreboard shows a thumbnail of
// each participant's photo once it is made; anonymous boards never do.
func TestScoreboardPhotos(t *testing.T) {
	pool := testutil.DB(t)
	rdb, ns := testutil.Redis(t)
	q := sqlc.New(pool)
	rws, err := rankingweb.New(config.RankingWeb{DataDir: t.TempDir(), PushToken: "tok"}, logging.Discard())
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(rws.Handler())
	defer ts.Close()
	store := blob.NewMem()
	p := New(pool, rdb, store, logging.Discard(), Options{URLs: []string{ts.URL}, Token: "tok", Secret: []byte("s"), Namespace: ns})
	ctx, cancel := context.WithCancel(bg)
	defer cancel()
	go p.photos.run(ctx)

	now := time.Now().UTC()
	c, _ := q.CreateContest(bg, db.NewContestParams("foto", now.Add(-time.Hour), now.Add(time.Hour)))
	pool.Exec(bg, "UPDATE contests SET ranking_show_photos = true WHERE id = $1", c.ID)
	orig, _ := store.PutBytes(bg, picture(900, 1200))
	for _, name := range []string{"ana", "beto"} {
		u, _ := q.CreateUser(bg, sqlc.CreateUserParams{Username: name, FirstName: name, PasswordHash: "x", PreferredLanguages: []string{}})
		if name == "ana" {
			pool.Exec(bg, "UPDATE users SET photo_digest = $2 WHERE id = $1", u.ID, orig.Digest)
		}
		q.CreateParticipation(bg, sqlc.CreateParticipationParams{ContestID: c.ID, UserID: u.ID, Ip: []netip.Prefix{}})
	}
	push := func() *[2]string {
		t.Helper()
		p.mu.Lock()
		p.state(c.ID).dirty, p.state(c.ID).full = true, true
		p.mu.Unlock()
		time.Sleep(p.opts.LiveInterval)
		p.Flush(bg)
		_, b := getJSON(t, ts.URL+"/foto/ranking.json")
		var got [2]string
		for i, r := range b.Rows {
			got[i] = r.Photo
		}
		return &got
	}
	push() // asks for the thumbnail
	deadline := time.Now().Add(10 * time.Second)
	var got *[2]string
	for {
		if got = push(); got[0] != "" || time.Now().After(deadline) {
			break
		}
	}
	if got[0] == "" || got[0] == orig.Digest || got[1] != "" {
		t.Fatalf("photos on the board %q (original %s)", got, orig.Digest)
	}
	resp, err := http.Get(ts.URL + "/assets/" + got[0])
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || http.DetectContentType(data) != "image/jpeg" || len(data) > 64<<10 {
		t.Fatalf("thumbnail asset: %d %s %d bytes", resp.StatusCode, http.DetectContentType(data), len(data))
	}
	resp, _ = http.Get(ts.URL + "/foto/")
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(page), `src="/assets/`+got[0]+`" alt="" class="photo"`) {
		t.Fatalf("scoreboard page without the photo:\n%s", page)
	}
	pool.Exec(bg, "UPDATE contests SET ranking_anonymous = true WHERE id = $1", c.ID)
	if got = push(); got[0] != "" || got[1] != "" {
		t.Fatalf("anonymous board with photos %q", got)
	}
}
