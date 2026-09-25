package rankingweb

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"sync"

	"github.com/D4ND3R/Contest-Management-System/internal/ranking"
)

// board is one contest's scoreboard in memory: the last board pushed, its
// history, the cached JSON snapshot and the connected spectators.
type board struct {
	mu      sync.RWMutex
	b       *ranking.Board
	seq     int64
	keyHash string
	history map[string][]ranking.Point
	// Caches rebuilt on every change.
	json, gz []byte
	etag     string
	pages    map[string][]byte // rendered page per language
	subs     map[chan []byte]struct{}
	dirty    bool // not yet saved to disk
}

func newBoard() *board {
	return &board{history: map[string][]ranking.Point{}, subs: map[chan []byte]struct{}{}}
}

// rebuild refreshes the caches (under b.mu).
func (bd *board) rebuild() {
	data, _ := json.Marshal(bd.b)
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	zw.Write(data)
	zw.Close()
	bd.json, bd.gz = data, buf.Bytes()
	bd.etag = `"s` + strconv.FormatInt(bd.seq, 10) + `"`
	bd.pages = map[string][]byte{}
	bd.dirty = true
}

// allowed reports whether key opens the board (always for public boards).
func (bd *board) allowed(key string) bool {
	if bd.keyHash == "" {
		return true
	}
	sum := sha256.Sum256([]byte(key))
	return subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(bd.keyHash)) == 1
}

// broadcast sends a frame to every spectator without blocking: a slow
// client loses events and re-syncs on reconnection.
func (bd *board) broadcast(frame []byte) {
	for ch := range bd.subs {
		select {
		case ch <- frame:
		default:
		}
	}
}

// apply merges a delta into the board.
func (bd *board) apply(p *ranking.Push) {
	idx := map[string]int{}
	for i, r := range bd.b.Rows {
		idx[r.Key] = i
	}
	for _, r := range p.Rows {
		if i, ok := idx[r.Key]; ok {
			bd.b.Rows[i] = r
		} else {
			idx[r.Key] = len(bd.b.Rows)
			bd.b.Rows = append(bd.b.Rows, r)
		}
	}
	if len(p.Removed) > 0 {
		gone := map[string]bool{}
		for _, k := range p.Removed {
			gone[k] = true
		}
		rows := bd.b.Rows[:0]
		for _, r := range bd.b.Rows {
			if !gone[r.Key] {
				rows = append(rows, r)
			}
		}
		bd.b.Rows = rows
	}
	bd.b.SortRows()
	for k, pts := range p.History {
		bd.history[k] = append(bd.history[k], pts...)
	}
	bd.seq = p.Seq
}
