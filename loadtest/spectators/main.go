// Command spectators holds many ranking event streams (server-sent events)
// open at once, as a crowd following the scoreboard, and measures how the
// ranking web server fans each update out: when every spectator got it
// relative to the first one. k6 pays a whole virtual user per stream, so
// the 10,000-spectator target of SPEC.md (F7) is played by this instead.
//
//	spectators -url http://127.0.0.1:18890/load/events -n 10000 -for 8m -out sse.txt
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// event is one update, identified by its sequence number, as the
// spectators received it.
type event struct {
	first, last time.Time
	got         int
	open        int64 // streams open when the first spectator got it
}

type stats struct {
	mu       sync.Mutex
	events   map[int64]*event
	open     atomic.Int64
	peak     atomic.Int64
	connects atomic.Int64
	failures atomic.Int64
	drops    atomic.Int64 // streams the server closed (reconnected)
}

func (s *stats) received(seq int64, at time.Time) {
	s.mu.Lock()
	e := s.events[seq]
	if e == nil {
		e = &event{first: at, open: s.open.Load()}
		s.events[seq] = e
	}
	e.last = at
	e.got++
	s.mu.Unlock()
}

func main() {
	url := flag.String("url", "http://127.0.0.1:18890/load/events", "ranking event stream")
	n := flag.Int("n", 10000, "spectators")
	rate := flag.Int("rate", 1000, "new connections per second while ramping up")
	dur := flag.Duration("for", 5*time.Minute, "how long to keep the streams open")
	out := flag.String("out", "", "summary file (default stdout)")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *dur)
	defer cancel()
	client := &http.Client{Transport: &http.Transport{
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		MaxIdleConnsPerHost: -1,
		DisableCompression:  true,
		WriteBufferSize:     1 << 10,
		ReadBufferSize:      4 << 10,
	}}
	s := &stats{events: map[int64]*event{}}
	var wg sync.WaitGroup
	tick := time.NewTicker(time.Second / time.Duration(max(*rate, 1)))
	start := time.Now()
	for i := 0; i < *n && ctx.Err() == nil; i++ {
		<-tick.C
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				if !follow(ctx, client, *url, s) {
					select { // refused or failed: back off, as a browser does
					case <-ctx.Done():
					case <-time.After(3 * time.Second):
					}
				}
			}
		}()
	}
	tick.Stop()
	ramp := time.Since(start)
	wg.Wait()

	w := os.Stdout
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer f.Close()
		w = f
	}
	report(w, s, *n, ramp)
}

// follow reads one stream until it ends; false when it could not open.
func follow(ctx context.Context, client *http.Client, url string, s *stats) bool {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() == nil {
			s.failures.Add(1)
		}
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		s.failures.Add(1)
		return false
	}
	s.connects.Add(1)
	if o := s.open.Add(1); o > s.peak.Load() {
		s.peak.Store(o) // racy maximum, close enough for a report
	}
	defer s.open.Add(-1)
	r := bufio.NewReaderSize(resp.Body, 4<<10)
	for {
		line, err := r.ReadSlice('\n')
		if err != nil && err != bufio.ErrBufferFull {
			if ctx.Err() == nil {
				s.drops.Add(1)
			}
			return true
		}
		// Row updates carry {"seq":N,...}; reloads have no sequence.
		if data, ok := strings.CutPrefix(string(line), "data: {\"seq\":"); ok {
			end := strings.IndexAny(data, ",}")
			if seq, err := strconv.ParseInt(data[:max(end, 0)], 10, 64); err == nil {
				s.received(seq, time.Now())
			}
		}
	}
}

func report(w *os.File, s *stats, n int, ramp time.Duration) {
	var spread []float64
	reachedAll := 0
	for _, e := range s.events {
		spread = append(spread, e.last.Sub(e.first).Seconds()*1000)
		if int64(e.got) >= e.open {
			reachedAll++
		}
	}
	sort.Float64s(spread)
	q := func(p float64) float64 {
		if len(spread) == 0 {
			return 0
		}
		return spread[min(len(spread)-1, int(p*float64(len(spread))))]
	}
	fmt.Fprintf(w, "spectators=%d ramp_s=%.1f peak_open=%d connects=%d failures=%d drops=%d\n",
		n, ramp.Seconds(), s.peak.Load(), s.connects.Load(), s.failures.Load(), s.drops.Load())
	fmt.Fprintf(w, "events=%d reached_all=%d spread_ms_p50=%.1f spread_ms_p95=%.1f spread_ms_p99=%.1f spread_ms_max=%.1f\n",
		len(spread), reachedAll, q(.5), q(.95), q(.99), q(1))
}
