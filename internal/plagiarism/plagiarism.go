// Package plagiarism finds suspiciously similar submissions of a task.
//
// Every source is reduced to a token stream that ignores what copying
// usually changes: whitespace and layout, comments, preprocessor lines,
// identifier names, and the values of literals (identifiers become one
// token, numbers another, strings a third; keywords and operators stay).
// Hashes of every k consecutive tokens are winnowed (the minimum of each
// window of w hashes is kept, Schleimer et al. 2003, the method behind
// MOSS), so any common run of at least k+w-1 tokens is always found,
// whatever surrounds it.
//
// Code handed to the contestants (stubs, templates) is removed token by
// token: every token covered by a k-gram of that code is marked, and no
// fingerprint may include a marked token, so neither the template nor its
// boundary with the contestant's own code counts. Fingerprints that many
// submissions share (reading the input, a common idiom) are ignored. The
// similarity of a pair is the share of the smaller submission's own
// fingerprints found in the other one.
package plagiarism

import (
	"sort"
	"strings"

	"github.com/D4ND3R/Contest-Management-System/internal/highlight"
)

const (
	// K is the number of tokens hashed together.
	K = 12
	// W is the winnowing window.
	W = 8
	// MinShared is the minimum number of shared fingerprints reported.
	MinShared = 4
)

// Doc is one fingerprinted submission.
type Doc struct {
	// ID identifies the submission.
	ID int64
	// Group: documents of the same group are never paired (a team's
	// members, one contestant's submissions).
	Group int64
	// Tokens is the length of the normalised token stream.
	Tokens int
	fps    []fingerprint
}

type fingerprint struct {
	hash uint64
	// line is the source line (0-based) where the k-gram starts, and end
	// the line where it ends.
	line, end int32
}

// token is a normalised token: the hash of its text and its line.
type token struct {
	hash uint64
	line int32
}

// strHash is 64-bit FNV-1a, without allocating.
func strHash(s string) uint64 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

var (
	identHash  = strHash("I")
	numberHash = strHash("N")
	stringHash = strHash("S")
)

// Source is a file of a submission.
type Source struct {
	Name string
	Data string
}

// tokens normalises the files with a known syntax; lines count across
// all the files, one after the other (see Offsets).
func tokens(files []Source) []token {
	size := 0
	for _, f := range files {
		size += len(f.Data)
	}
	out := make([]token, 0, size/2+16)
	var base int32
	for _, f := range files {
		data := crlf(f.Data)
		s := highlight.ForFile(f.Name)
		if s == nil {
			base += int32(strings.Count(data, "\n")) + 1
			continue
		}
		highlight.Tokens(data, s, func(kind byte, text string, line int) {
			var h uint64
			switch kind {
			case highlight.Identifier:
				h = identHash
			case highlight.Number:
				h = numberHash
			case highlight.String:
				h = stringHash
			case highlight.Comment, highlight.Preprocessor:
				return
			default: // keywords and operators
				h = strHash(text)
			}
			out = append(out, token{hash: h, line: base + int32(line)})
		})
		base += int32(strings.Count(data, "\n")) + 1
	}
	return out
}

// crlf normalises Windows line ends.
func crlf(s string) string {
	if strings.IndexByte(s, '\r') < 0 {
		return s
	}
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// Offsets returns the line where each file starts in the numbering of
// Matches.
func Offsets(files []Source) []int {
	out := make([]int, len(files))
	base := 0
	for i, f := range files {
		out[i] = base
		base += strings.Count(f.Data, "\n") + 1
	}
	return out
}

// kgrams returns the rolling hash of every K consecutive tokens.
func kgrams(toks []token) []uint64 {
	if len(toks) < K {
		return nil
	}
	const mul = 0x100000001b3
	var top uint64 = 1 // mul^(K-1)
	for i := 1; i < K; i++ {
		top *= mul
	}
	hashes := make([]uint64, len(toks)-K+1)
	var h uint64
	for i, t := range toks {
		if i >= K {
			h -= toks[i-K].hash * top
		}
		h = h*mul + t.hash
		if i >= K-1 {
			hashes[i-K+1] = mix(h)
		}
	}
	return hashes
}

// Base is the code handed to the contestants: the hashes of all its
// k-grams.
type Base map[uint64]struct{}

// NewBase builds the base of the given files (templates, stubs).
func NewBase(files []Source) Base {
	b := Base{}
	for _, f := range files {
		for _, h := range kgrams(tokens([]Source{f})) {
			b[h] = struct{}{}
		}
	}
	return b
}

// Fingerprint builds the document of a submission; it has no
// fingerprints when no file is in a known language or its own code is
// shorter than K tokens. base may be nil.
func Fingerprint(id, group int64, files []Source, base Base) *Doc {
	toks := tokens(files)
	d := &Doc{ID: id, Group: group, Tokens: len(toks)}
	hashes := kgrams(toks)
	if len(hashes) == 0 {
		return d
	}
	// Tokens covered by base code; blocked[i]: k-gram i includes one.
	var blocked []bool
	if len(base) > 0 {
		covered := make([]bool, len(toks))
		for i, h := range hashes {
			if _, ok := base[h]; ok {
				for j := i; j < i+K; j++ {
					covered[j] = true
				}
			}
		}
		blocked = make([]bool, len(hashes))
		run := 0 // covered tokens in the current k-gram
		for i := range toks {
			if covered[i] {
				run++
			}
			if i >= K && covered[i-K] {
				run--
			}
			if i >= K-1 {
				blocked[i-K+1] = run > 0
			}
		}
	}
	// Winnowing: the rightmost minimum of every window, each position
	// once; the minimum is only searched again when it leaves the window.
	free := func(i int) bool { return blocked == nil || !blocked[i] }
	last, m := -1, -1
	for start := 0; ; start++ {
		end := min(start+W, len(hashes))
		if m < start {
			m = -1
			for i := start; i < end; i++ {
				if free(i) && (m < 0 || hashes[i] <= hashes[m]) {
					m = i
				}
			}
		} else if i := end - 1; free(i) && hashes[i] <= hashes[m] {
			m = i
		}
		if m >= 0 && m != last {
			d.fps = append(d.fps, fingerprint{hash: hashes[m], line: toks[m].line, end: toks[m+K-1].line})
			last = m
		}
		if end == len(hashes) {
			break
		}
	}
	return d
}

// mix spreads the bits of a hash (the splitmix64 finaliser), so that the
// window minima are evenly spread.
func mix(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	return x ^ x>>31
}

// Pair is a reported pair of submissions.
type Pair struct {
	A, B   *Doc
	Shared int
	// Similarity is the share (0–1) of the smaller document's own
	// fingerprints found in the other.
	Similarity float64
}

// Options tune a comparison.
type Options struct {
	// Common: a fingerprint in more than this many documents is ignored
	// (0: max(5, n/10)).
	Common int
	// Threshold is the minimum similarity reported (0–1).
	Threshold float64
	// Limit bounds the number of pairs returned (0: 200).
	Limit int
}

// Compare returns the pairs of documents at least opt.Threshold similar,
// most similar first. The work is proportional to the fingerprints and to
// the pairs that share one, not to every pair.
func Compare(docs []*Doc, opt Options) []Pair {
	common := opt.Common
	if common <= 0 {
		common = max(5, len(docs)/10)
	}
	limit := opt.Limit
	if limit <= 0 {
		limit = 200
	}
	// Inverted index over distinct fingerprints.
	index := map[uint64][]int32{}
	for i, d := range docs {
		seen := map[uint64]bool{}
		for _, f := range d.fps {
			if seen[f.hash] {
				continue
			}
			seen[f.hash] = true
			index[f.hash] = append(index[f.hash], int32(i))
		}
	}
	own := make([]int, len(docs))
	shared := map[[2]int32]int{}
	for _, list := range index {
		if len(list) > common {
			continue
		}
		for _, i := range list {
			own[i]++
		}
		for x := 0; x < len(list); x++ {
			for y := x + 1; y < len(list); y++ {
				a, b := list[x], list[y]
				if docs[a].Group != 0 && docs[a].Group == docs[b].Group {
					continue
				}
				shared[[2]int32{a, b}]++
			}
		}
	}
	var out []Pair
	for k, n := range shared {
		if n < MinShared {
			continue
		}
		a, b := docs[k[0]], docs[k[1]]
		den := min(own[k[0]], own[k[1]])
		if den == 0 {
			continue
		}
		sim := float64(n) / float64(den)
		if sim > 1 {
			sim = 1
		}
		if sim >= opt.Threshold {
			out = append(out, Pair{A: a, B: b, Shared: n, Similarity: sim})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Similarity != out[j].Similarity {
			return out[i].Similarity > out[j].Similarity
		}
		if out[i].Shared != out[j].Shared {
			return out[i].Shared > out[j].Shared
		}
		return out[i].A.ID < out[j].A.ID || out[i].A.ID == out[j].A.ID && out[i].B.ID < out[j].B.ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Matches returns, for the side-by-side view, the source lines of a and
// of b covered by the fingerprints they share.
func Matches(a, b *Doc) (linesA, linesB map[int]bool) {
	inB := map[uint64]bool{}
	for _, f := range b.fps {
		inB[f.hash] = true
	}
	inA := map[uint64]bool{}
	linesA, linesB = map[int]bool{}, map[int]bool{}
	for _, f := range a.fps {
		if inB[f.hash] {
			inA[f.hash] = true
			for l := f.line; l <= f.end; l++ {
				linesA[int(l)] = true
			}
		}
	}
	for _, f := range b.fps {
		if inA[f.hash] {
			for l := f.line; l <= f.end; l++ {
				linesB[int(l)] = true
			}
		}
	}
	return linesA, linesB
}
