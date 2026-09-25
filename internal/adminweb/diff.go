package adminweb

import "strings"

// diffLine is one line of a unified line diff.
type diffLine struct {
	Op   string // " ", "-", "+"
	Text string
	A, B int // 1-based line numbers (0 when absent)
}

// lineDiff computes a line diff of a and b with the Myers O((N+M)D)
// algorithm. When the edit distance exceeds maxD the remaining lines are
// reported as a replacement (bounded time for unrelated files).
func lineDiff(a, b []string, maxD int) []diffLine {
	n, m := len(a), len(b)
	// Trim the common prefix and suffix (the usual case for resubmissions).
	pre := 0
	for pre < n && pre < m && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < n-pre && suf < m-pre && a[n-1-suf] == b[m-1-suf] {
		suf++
	}
	var out []diffLine
	for i := 0; i < pre; i++ {
		out = append(out, diffLine{" ", a[i], i + 1, i + 1})
	}
	mid := myers(a[pre:n-suf], b[pre:m-suf], maxD)
	for _, l := range mid {
		if l.A > 0 {
			l.A += pre
		}
		if l.B > 0 {
			l.B += pre
		}
		out = append(out, l)
	}
	for i := 0; i < suf; i++ {
		ai, bi := n-suf+i, m-suf+i
		out = append(out, diffLine{" ", a[ai], ai + 1, bi + 1})
	}
	return out
}

func myers(a, b []string, maxD int) []diffLine {
	n, m := len(a), len(b)
	if n == 0 || m == 0 {
		return replace(a, b)
	}
	offset := n + m
	v := make([]int, 2*offset+2)
	var trace [][]int
	found := false
	for d := 0; d <= n+m && d <= maxD; d++ {
		snap := make([]int, len(v))
		copy(snap, v)
		trace = append(trace, snap)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
				x = v[offset+k+1]
			} else {
				x = v[offset+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x, y = x+1, y+1
			}
			v[offset+k] = x
			if x >= n && y >= m {
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		return replace(a, b)
	}
	// Backtrack.
	var rev []diffLine
	x, y := n, m
	for d := len(trace) - 1; d >= 0 && (x > 0 || y > 0); d-- {
		vv := trace[d]
		k := x - y
		var prevK int
		if k == -d || (k != d && vv[offset+k-1] < vv[offset+k+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := vv[offset+prevK]
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			x, y = x-1, y-1
			rev = append(rev, diffLine{" ", a[x], x + 1, y + 1})
		}
		if d == 0 {
			break
		}
		if x == prevX {
			y--
			rev = append(rev, diffLine{"+", b[y], 0, y + 1})
		} else {
			x--
			rev = append(rev, diffLine{"-", a[x], x + 1, 0})
		}
	}
	out := make([]diffLine, len(rev))
	for i := range rev {
		out[i] = rev[len(rev)-1-i]
	}
	return out
}

func replace(a, b []string) []diffLine {
	var out []diffLine
	for i, l := range a {
		out = append(out, diffLine{"-", l, i + 1, 0})
	}
	for i, l := range b {
		out = append(out, diffLine{"+", l, 0, i + 1})
	}
	return out
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
