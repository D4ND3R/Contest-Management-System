// Package checkers compares contestant outputs with the expected ones.
//
// Built-in comparators run in-process and stream both files, so they cost
// microseconds per megabyte and never load whole outputs in memory:
//
//	exact       byte-for-byte equality
//	white_diff  line-by-line equality ignoring the amount of whitespace
//	            between tokens and trailing blank lines (CMS "diff")
//	float       token-by-token equality where numeric tokens may differ by
//	            an absolute or relative tolerance
//
// Custom checkers (CMS protocol and testlib) run inside the sandbox; see the
// tasktypes package. This package parses their output.
package checkers

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// Messages shown to contestants (translated by the web UI).
const (
	MsgCorrect = "Output is correct"
	MsgWrong   = "Output isn't correct"
	MsgPartial = "Output is partially correct"
)

// Outcome of a comparison.
type Outcome struct {
	Score   float64 // in [0, 1]
	Message string
}

// Correct and Wrong are the binary outcomes of the built-in comparators.
var (
	Correct = Outcome{1, MsgCorrect}
	Wrong   = Outcome{0, MsgWrong}
)

// Kind names a built-in comparator.
type Kind string

const (
	Exact     Kind = "exact"
	WhiteDiff Kind = "white_diff"
	Float     Kind = "float"
)

// Params configure the float comparator.
type Params struct {
	AbsTol float64
	RelTol float64
}

// Compare runs a built-in comparator.
func Compare(kind Kind, expected, actual io.Reader, p Params) (Outcome, error) {
	var ok bool
	var err error
	switch kind {
	case Exact:
		ok, err = CompareExact(expected, actual)
	case WhiteDiff, "":
		ok, err = CompareWhite(expected, actual)
	case Float:
		ok, err = CompareFloat(expected, actual, p.AbsTol, p.RelTol)
	default:
		return Outcome{}, fmt.Errorf("unknown comparator %q", kind)
	}
	if err != nil {
		return Outcome{}, err
	}
	if ok {
		return Correct, nil
	}
	return Wrong, nil
}

const bufSize = 64 << 10

// CompareExact reports whether both streams are byte-identical.
func CompareExact(expected, actual io.Reader) (bool, error) {
	a := bufio.NewReaderSize(expected, bufSize)
	b := bufio.NewReaderSize(actual, bufSize)
	bufA := make([]byte, bufSize)
	bufB := make([]byte, bufSize)
	for {
		na, errA := io.ReadFull(a, bufA)
		nb, errB := io.ReadFull(b, bufB)
		if na != nb || !bytes.Equal(bufA[:na], bufB[:nb]) {
			return false, nil
		}
		endA := errA == io.EOF || errA == io.ErrUnexpectedEOF
		endB := errB == io.EOF || errB == io.ErrUnexpectedEOF
		if errA != nil && !endA {
			return false, errA
		}
		if errB != nil && !endB {
			return false, errB
		}
		if endA || endB {
			return endA && endB, nil
		}
	}
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

// lineReader yields lines of arbitrary length.
type lineReader struct {
	r   *bufio.Reader
	buf []byte
}

// next returns the next line without its terminator; ok=false at EOF.
func (l *lineReader) next() ([]byte, bool, error) {
	l.buf = l.buf[:0]
	for {
		chunk, err := l.r.ReadSlice('\n')
		l.buf = append(l.buf, chunk...)
		switch {
		case err == nil:
			return l.buf, true, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return l.buf, len(l.buf) > 0, nil
		default:
			return nil, false, err
		}
	}
}

// fields splits a line into whitespace-separated tokens (no allocation of strings).
func fields(line []byte, dst [][]byte) [][]byte {
	dst = dst[:0]
	i := 0
	for i < len(line) {
		for i < len(line) && isSpace(line[i]) {
			i++
		}
		j := i
		for j < len(line) && !isSpace(line[j]) {
			j++
		}
		if j > i {
			dst = append(dst, line[i:j])
		}
		i = j
	}
	return dst
}

func blank(line []byte) bool {
	for _, c := range line {
		if !isSpace(c) {
			return false
		}
	}
	return true
}

// CompareWhite implements the CMS white-diff: files are compared line by
// line; two lines match when they have the same whitespace-separated tokens;
// when one file ends, the remaining lines of the other must be blank.
func CompareWhite(expected, actual io.Reader) (bool, error) {
	a := &lineReader{r: bufio.NewReaderSize(expected, bufSize)}
	b := &lineReader{r: bufio.NewReaderSize(actual, bufSize)}
	var fa, fb [][]byte
	for {
		la, okA, err := a.next()
		if err != nil {
			return false, err
		}
		lb, okB, err := b.next()
		if err != nil {
			return false, err
		}
		switch {
		case !okA && !okB:
			return true, nil
		case !okA:
			if !blank(lb) {
				return false, nil
			}
		case !okB:
			if !blank(la) {
				return false, nil
			}
		default:
			fa = fields(la, fa)
			fb = fields(lb, fb)
			if len(fa) != len(fb) {
				return false, nil
			}
			for i := range fa {
				if !bytes.Equal(fa[i], fb[i]) {
					return false, nil
				}
			}
		}
	}
}

// tokenReader yields whitespace-separated tokens across lines.
type tokenReader struct {
	r   *bufio.Reader
	buf []byte
}

func (t *tokenReader) next() ([]byte, bool, error) {
	t.buf = t.buf[:0]
	for {
		c, err := t.r.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return t.buf, len(t.buf) > 0, nil
			}
			return nil, false, err
		}
		if isSpace(c) {
			if len(t.buf) > 0 {
				return t.buf, true, nil
			}
			continue
		}
		t.buf = append(t.buf, c)
		if len(t.buf) > 1<<20 {
			return nil, false, errors.New("token longer than 1 MiB")
		}
	}
}

// CompareFloat compares token by token; tokens that parse as finite numbers
// on both sides match when |a-b| <= absTol or |a-b| <= relTol*|expected|.
// Other tokens must be identical.
func CompareFloat(expected, actual io.Reader, absTol, relTol float64) (bool, error) {
	a := &tokenReader{r: bufio.NewReaderSize(expected, bufSize)}
	b := &tokenReader{r: bufio.NewReaderSize(actual, bufSize)}
	for {
		ta, okA, err := a.next()
		if err != nil {
			return false, err
		}
		tb, okB, err := b.next()
		if err != nil {
			return false, err
		}
		if !okA || !okB {
			return okA == okB, nil
		}
		if bytes.Equal(ta, tb) {
			continue
		}
		x, errX := strconv.ParseFloat(string(ta), 64)
		y, errY := strconv.ParseFloat(string(tb), 64)
		if errX != nil || errY != nil || math.IsNaN(x) || math.IsNaN(y) || math.IsInf(x, 0) || math.IsInf(y, 0) {
			return false, nil
		}
		d := math.Abs(x - y)
		if d <= absTol || d <= relTol*math.Abs(x) {
			continue
		}
		return false, nil
	}
}

// ParseCMSChecker interprets the output of a custom checker following the
// CMS protocol: the first line of stdout is the score in [0,1]; the first
// line of stderr is the message ("translate:success|wrong|partial" are
// mapped to the standard messages).
func ParseCMSChecker(stdout, stderr []byte) (Outcome, error) {
	line, _, _ := strings.Cut(strings.TrimSpace(string(stdout)), "\n")
	score, err := strconv.ParseFloat(strings.TrimSpace(line), 64)
	if err != nil || math.IsNaN(score) || score < 0 || score > 1 {
		return Outcome{}, fmt.Errorf("checker printed an invalid score %q", truncate(line, 64))
	}
	msg, _, _ := strings.Cut(strings.TrimSpace(string(stderr)), "\n")
	return Outcome{Score: score, Message: TranslateMessage(strings.TrimSpace(msg), score)}, nil
}

// TranslateMessage maps CMS "translate:" keys to standard messages and
// supplies a default when the checker printed nothing.
func TranslateMessage(msg string, score float64) string {
	switch msg {
	case "translate:success":
		return MsgCorrect
	case "translate:wrong":
		return MsgWrong
	case "translate:partial":
		return MsgPartial
	case "":
		switch {
		case score >= 1:
			return MsgCorrect
		case score <= 0:
			return MsgWrong
		default:
			return MsgPartial
		}
	}
	return truncate(msg, 512)
}

// ParseTestlib interprets a testlib checker run (argument order
// input, contestant output, answer). Exit codes: 0 OK, 1 WA, 2 presentation
// error, 3 checker failure (a judge error), 4 unexpected EOF, 7 partial
// ("points" in the message, interpreted as a fraction when <= 1 and as a
// percentage otherwise), 16+n partial credit of n percent (_pc(n)).
func ParseTestlib(exitCode int, stderr []byte) (Outcome, error) {
	msg, _, _ := strings.Cut(strings.TrimSpace(string(stderr)), "\n")
	msg = truncate(strings.TrimSpace(msg), 512)
	switch {
	case exitCode == 0:
		return Outcome{1, orDefault(msg, MsgCorrect)}, nil
	case exitCode == 1 || exitCode == 2 || exitCode == 4:
		return Outcome{0, orDefault(msg, MsgWrong)}, nil
	case exitCode == 3:
		return Outcome{}, fmt.Errorf("checker failed: %s", msg)
	case exitCode == 7:
		f := strings.Fields(msg)
		for i, w := range f {
			if (w == "points" || w == "points:") && i+1 < len(f) {
				if v, err := strconv.ParseFloat(f[i+1], 64); err == nil {
					if v > 1 {
						v /= 100
					}
					return Outcome{clamp01(v), msg}, nil
				}
			}
		}
		if len(f) > 0 {
			if v, err := strconv.ParseFloat(f[0], 64); err == nil {
				if v > 1 {
					v /= 100
				}
				return Outcome{clamp01(v), msg}, nil
			}
		}
		return Outcome{}, fmt.Errorf("testlib partial result without points: %q", msg)
	case exitCode >= 16 && exitCode <= 116:
		return Outcome{clamp01(float64(exitCode-16) / 100), orDefault(msg, MsgPartial)}, nil
	default:
		return Outcome{}, fmt.Errorf("checker exited with unexpected code %d: %s", exitCode, msg)
	}
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
