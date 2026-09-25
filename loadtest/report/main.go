// Command report turns the files of one loadtest/run.sh run into a
// Markdown report.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type metric map[string]any

func num(m metric, k string) float64 {
	if v, ok := m[k].(float64); ok {
		return v
	}
	return 0
}

func read(dir, name string) string {
	b, _ := os.ReadFile(filepath.Join(dir, name))
	return strings.TrimSpace(string(b))
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: report RESULTS_DIR")
		os.Exit(2)
	}
	dir := os.Args[1]
	var summary struct {
		Metrics map[string]metric `json:"metrics"`
	}
	if b, err := os.ReadFile(filepath.Join(dir, "k6-summary.json")); err == nil {
		_ = json.Unmarshal(b, &summary)
	}
	meta := map[string]string{}
	for _, f := range strings.Fields(read(dir, "meta.txt")) {
		if k, v, ok := strings.Cut(f, "="); ok {
			meta[k] = v
		}
	}
	for _, line := range strings.Split(read(dir, "meta.txt"), "\n") {
		if v, ok := strings.CutPrefix(line, "cpu_model="); ok {
			meta["cpu_model"] = v
		}
	}
	fmt.Printf("# Load test %s\n\n", filepath.Base(dir))
	fmt.Printf("%s contestants (logging in, browsing every 5–15 s, submitting every 2–4 min, then every 20–40 s in the last %s min) with their event streams open, and %s ranking spectators, for %s + %s minutes.\n\n",
		meta["contestants"], meta["burst_min"], meta["spectators"], meta["normal_min"], meta["burst_min"])
	fmt.Printf("Layout: CPU %s runs the web servers, dispatcher, monitor, PostgreSQL (durable commits) and Valkey (append-only); CPU %s the sandbox; k6 on CPUs %s. Host: %s CPUs (%s), %s GiB, kernel %s.\n\n",
		meta["web_cpu"], meta["judge_cpu"], meta["k6_cpus"], meta["host_cpus"], meta["cpu_model"], meta["host_mem_gib"], meta["kernel"])

	fmt.Println("## Web latency (ms)")
	fmt.Println()
	fmt.Println("| Requests | count | p50 | p95 | p99 | max | failed |")
	fmt.Println("|----------|------:|----:|----:|----:|----:|-------:|")
	for _, k := range []struct{ label, kind string }{
		{"Contest pages (overview, task, submissions)", "page"}, {"Submit", "submit"}, {"Login (argon2id)", "login"}, {"Ranking page", "ranking"},
	} {
		d := summary.Metrics["http_req_duration{kind:"+k.kind+"}"]
		if d == nil {
			continue
		}
		failed := summary.Metrics["http_req_failed{kind:"+k.kind+"}"]
		fmt.Printf("| %s | %.0f | %.1f | %.1f | %.1f | %.0f | %.2f%% |\n", k.label, num(d, "count"), num(d, "med"), num(d, "p(95)"), num(d, "p(99)"),
			num(d, "max"), 100*num(failed, "value"))
	}
	fmt.Println()
	checks := summary.Metrics["checks"]
	fmt.Printf("Checks passed: %.2f%%. Submissions accepted by the web server: %.0f (rejected: %.0f).\n\n",
		100*num(checks, "value"), num(summary.Metrics["cms_submissions_accepted"], "count"), num(summary.Metrics["cms_submissions_rejected"], "count"))

	serverLatency(dir)
	if sse := read(dir, "sse.txt"); sse != "" {
		m := map[string]string{}
		for _, f := range strings.Fields(sse) {
			if k, v, ok := strings.Cut(f, "="); ok {
				m[k] = v
			}
		}
		fmt.Println("## Ranking event streams (loadtest/spectators)")
		fmt.Println()
		fmt.Printf("%s more spectators followed the ranking over server-sent events (opened in %s s; at most %s open at once; %s refused or failed, %s closed by the server). %s ranking updates reached them; %s reached every stream open at the time. Delay between the first and the last spectator receiving the same update: p50 %s ms, p95 %s ms, p99 %s ms, max %s ms.\n\n",
			m["spectators"], m["ramp_s"], m["peak_open"], m["failures"], m["drops"], m["events"], m["reached_all"],
			m["spread_ms_p50"], m["spread_ms_p95"], m["spread_ms_p99"], m["spread_ms_max"])
	}

	fmt.Println("## Judging")
	fmt.Println()
	lat := strings.Split(read(dir, "judge-latency.txt"), "|")
	drained := fmt.Sprintf("the queue drained %s s after the load ended", meta["drain_seconds"])
	if p := meta["pending"]; p != "" && p != "0" {
		drained = fmt.Sprintf("%s were still waiting %s s after the load ended, when the test stopped (latencies are of the judged ones)", p, meta["drain_seconds"])
	}
	fmt.Printf("%s submissions; from submission to score: p50 %s s, p95 %s s, max %s s; %s.\n\n",
		read(dir, "submissions.txt"), get(lat, 0), get(lat, 1), get(lat, 2), drained)
	sub, judged := perMinute(read(dir, "submitted-per-minute.txt")), perMinute(read(dir, "judged-per-minute.txt"))
	var minutes []string
	seen := map[string]bool{}
	for _, m := range append(keys(sub), keys(judged)...) {
		if !seen[m] {
			seen[m] = true
			minutes = append(minutes, m)
		}
	}
	sort.Strings(minutes)
	fmt.Println("| Minute (UTC) | submitted | judged |")
	fmt.Println("|--------------|----------:|-------:|")
	best := 0
	for _, m := range minutes {
		fmt.Printf("| %s | %d | %d |\n", m, sub[m], judged[m])
		best = max(best, judged[m])
	}
	fmt.Printf("\nPeak judging throughput: %d submissions per minute on one judging core.\n\n", best)

	fmt.Println("## Resources (sampled every 5 s)")
	fmt.Println()
	var peakRSS, peakBacklog, n, sumWeb, peakWeb, sumJudge, peakK6 int
	f, err := os.Open(filepath.Join(dir, "samples.txt"))
	if err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			load := strings.Contains(sc.Text(), " phase=load ")
			if load {
				n++
			}
			for _, fld := range strings.Fields(sc.Text()) {
				k, v, _ := strings.Cut(fld, "=")
				x, _ := strconv.Atoi(v)
				switch k {
				case "rss_mib":
					peakRSS = max(peakRSS, x)
				case "backlog":
					peakBacklog = max(peakBacklog, x)
				case "web_cpu":
					if load {
						sumWeb += x
						peakWeb = max(peakWeb, x)
					}
				case "judge_cpu":
					if load {
						sumJudge += x
					}
				case "k6_cpu":
					peakK6 = max(peakK6, x)
				}
			}
		}
		f.Close()
	}
	n = max(n, 1)
	fmt.Printf("While k6 ran, the web CPU (%s) was busy %d%% on average and %d%% in the busiest 5 s; the judging CPU (%s) %d%% on average. Peak resident memory of the CMS processes %d MiB; largest judging backlog %d submissions. The busiest k6 CPU peaked at %d%% (near 100%% the generator, not CMS, limits the load).\n",
		meta["web_cpu"], sumWeb/n, peakWeb, meta["judge_cpu"], sumJudge/n, peakRSS, peakBacklog, peakK6)
}

// histogram accumulates the buckets (upper bound in seconds → cumulative
// count) of several series of cms_http_request_duration_seconds.
type histogram map[float64]float64

// quantile interpolates linearly inside the bucket holding q, as
// Prometheus' histogram_quantile does; +Inf when q is beyond the last
// finite bound.
func (h histogram) quantile(q float64) float64 {
	bounds := make([]float64, 0, len(h))
	for b := range h {
		bounds = append(bounds, b)
	}
	sort.Float64s(bounds)
	if len(bounds) == 0 || h[bounds[len(bounds)-1]] == 0 {
		return 0
	}
	rank := q * h[bounds[len(bounds)-1]]
	prevB, prevC := 0.0, 0.0
	for _, b := range bounds {
		if c := h[b]; c >= rank {
			if b > 1e300 {
				return b
			}
			if c == prevC {
				return b
			}
			return prevB + (b-prevB)*(rank-prevC)/(c-prevC)
		}
		prevB, prevC = b, h[b]
	}
	return bounds[len(bounds)-1]
}

// under is the share of observations of at most bound seconds (a bucket
// bound).
func (h histogram) under(bound float64) float64 {
	total := 0.0
	for b, c := range h {
		if b > 1e300 {
			total = c
		}
	}
	if total == 0 {
		return 0
	}
	return h[bound] / total
}

// serverLatency reports the latency measured inside the web servers (the
// request histograms scraped from their /metrics right after k6), the
// figure SPEC.md's targets are stated in. Event streams are left out:
// they last as long as the connection.
func serverLatency(dir string) {
	groups := []struct {
		label string
		file  string
		match func(route string) bool
		h     histogram
	}{
		{"Contest web server: pages and submissions", "cws-metrics.txt", func(r string) bool {
			return !strings.HasSuffix(r, "/events") && r != "POST /{contest}/login" && r != "GET /metrics" && r != "GET /healthz"
		}, histogram{}},
		{"Contest web server: login (argon2id)", "cws-metrics.txt", func(r string) bool { return r == "POST /{contest}/login" }, histogram{}},
		{"Ranking web server (page and JSON)", "rws-metrics.txt", func(r string) bool {
			return !strings.HasSuffix(r, "/events") && r != "GET /metrics" && r != "GET /healthz" && r != "POST /push" && !strings.HasPrefix(r, "PUT ")
		}, histogram{}},
	}
	found := false
	for i := range groups {
		g := &groups[i]
		f, err := os.Open(filepath.Join(dir, g.file))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := sc.Text()
			rest, ok := strings.CutPrefix(line, "cms_http_request_duration_seconds_bucket{")
			if !ok {
				continue
			}
			labels, value, ok := strings.Cut(rest, "} ")
			if !ok {
				continue
			}
			var route, le string
			for _, l := range strings.Split(labels, "\",") {
				k, v, _ := strings.Cut(l, "=\"")
				v = strings.TrimSuffix(v, "\"")
				switch k {
				case "route":
					route = v
				case "le":
					le = v
				}
			}
			if !g.match(route) {
				continue
			}
			b, err1 := strconv.ParseFloat(le, 64)
			c, err2 := strconv.ParseFloat(value, 64)
			if err1 == nil && err2 == nil {
				g.h[b] += c
				found = true
			}
		}
		f.Close()
	}
	if !found {
		return
	}
	fmt.Println("## Server-side latency (ms)")
	fmt.Println()
	fmt.Println("Measured inside the servers (request histograms), which leaves out the network and k6. SPEC.md's contest web target: p95 < 15 ms, p99 < 40 ms.")
	fmt.Println()
	fmt.Println("| Requests | count | p50 | p95 | p99 | ≤ 15 ms | ≤ 40 ms |")
	fmt.Println("|----------|------:|----:|----:|----:|--------:|--------:|")
	ms := func(v float64) string {
		if v > 1e300 {
			return "> 2500"
		}
		return fmt.Sprintf("%.1f", 1000*v)
	}
	for _, g := range groups {
		total := g.h[math.Inf(1)]
		if total == 0 {
			continue
		}
		fmt.Printf("| %s | %.0f | %s | %s | %s | %.1f%% | %.1f%% |\n", g.label, total, ms(g.h.quantile(.5)), ms(g.h.quantile(.95)), ms(g.h.quantile(.99)),
			100*g.h.under(.015), 100*g.h.under(.04))
	}
	fmt.Println()
}

func get(xs []string, i int) string {
	if i < len(xs) {
		return strings.TrimSpace(xs[i])
	}
	return "?"
}

func perMinute(s string) map[string]int {
	m := map[string]int{}
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(line, "|")
		if ok {
			m[k], _ = strconv.Atoi(v)
		}
	}
	return m
}

func keys(m map[string]int) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
