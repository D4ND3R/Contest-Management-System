package rankingpush

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"sync"

	// Photos come as JPEG, PNG or GIF.
	_ "image/gif"
	_ "image/png"

	"github.com/D4ND3R/Contest-Management-System/internal/blob"
	"github.com/D4ND3R/Contest-Management-System/internal/ranking"
)

// thumbSize is the side of a scoreboard photo, in pixels (shown at about
// 2em, twice that on the participant's page, on high-density screens).
const thumbSize = 128

// maxPixels bounds the photos decoded (50 megapixels, far above a camera's).
const maxPixels = 50 << 20

// photos turns the participants' photos (up to 16 MiB each) into small
// square thumbnails for the scoreboards, in the background: a board shows
// a photo once its thumbnail is ready, so a contest start is never slowed
// by decoding hundreds of pictures.
type photos struct {
	blobs blob.Store
	// ready calls back when a thumbnail is done (the boards need a push).
	ready func()

	mu     sync.Mutex
	thumbs map[string]string // original digest → thumbnail digest ("" = unusable)
	queued map[string]bool
	work   chan string
}

func newPhotos(blobs blob.Store, ready func()) *photos {
	return &photos{blobs: blobs, ready: ready, thumbs: map[string]string{}, queued: map[string]bool{}, work: make(chan string, 4096)}
}

// apply replaces the rows' photos by their thumbnails, dropping those not
// ready yet (and asking for them).
func (ph *photos) apply(b *ranking.Board) {
	if !b.Photos {
		for i := range b.Rows {
			b.Rows[i].Photo = ""
		}
		return
	}
	ph.mu.Lock()
	defer ph.mu.Unlock()
	for i := range b.Rows {
		orig := b.Rows[i].Photo
		if orig == "" {
			continue
		}
		thumb, done := ph.thumbs[orig]
		b.Rows[i].Photo = thumb
		if !done && !ph.queued[orig] {
			select {
			case ph.work <- orig:
				ph.queued[orig] = true
			default: // full: asked again on the next push
			}
		}
	}
}

// run makes the thumbnails, one at a time.
func (ph *photos) run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case orig := <-ph.work:
			thumb := ""
			if data, err := blob.ReadAll(ctx, ph.blobs, orig); err == nil {
				if small, err := thumbnail(data); err == nil {
					if info, err := ph.blobs.PutBytes(ctx, small); err == nil {
						thumb = info.Digest
					}
				}
			}
			if ctx.Err() != nil {
				return nil
			}
			ph.mu.Lock()
			ph.thumbs[orig] = thumb
			delete(ph.queued, orig)
			ph.mu.Unlock()
			if thumb != "" && len(ph.work) == 0 {
				ph.ready()
			}
		}
	}
}

// thumbnail crops the centre square of an image and scales it down to at
// most thumbSize pixels a side, as a JPEG. Each pixel averages a 4×4 grid
// of samples of its source block: smooth enough for a small face, and
// cheap on a 12-megapixel photo.
func thumbnail(data []byte) ([]byte, error) {
	// A small file can declare a gigantic canvas: check before decoding.
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxPixels {
		return nil, fmt.Errorf("photo of %d×%d pixels", cfg.Width, cfg.Height)
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	r := src.Bounds()
	side := min(r.Dx(), r.Dy())
	x0, y0 := r.Min.X+(r.Dx()-side)/2, r.Min.Y+(r.Dy()-side)/2
	n := min(thumbSize, side)
	dst := image.NewRGBA(image.Rect(0, 0, n, n))
	const grid = 4
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			var sr, sg, sb, cnt uint32
			for j := 0; j < grid; j++ {
				sy := y0 + (y*grid+j)*side/(n*grid)
				for i := 0; i < grid; i++ {
					sx := x0 + (x*grid+i)*side/(n*grid)
					cr, cg, cb, _ := src.At(sx, sy).RGBA()
					sr, sg, sb, cnt = sr+cr>>8, sg+cg>>8, sb+cb>>8, cnt+1
				}
			}
			dst.SetRGBA(x, y, color.RGBA{uint8(sr / cnt), uint8(sg / cnt), uint8(sb / cnt), 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
