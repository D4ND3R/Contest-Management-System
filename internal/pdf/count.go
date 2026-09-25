package pdf

import (
	"bytes"
	"compress/zlib"
	"errors"
	"io"
	"regexp"
	"strconv"
)

// ErrPageCount is returned when a document's pages cannot be counted.
var ErrPageCount = errors.New("pdf: cannot count the pages")

var (
	typePagesRe = regexp.MustCompile(`/Type\s*/Pages(?:[\s/<>\[\]()]|$)`)
	kidsRe      = regexp.MustCompile(`/Kids\b`)
	countRe     = regexp.MustCompile(`/Count\s+(\d+)`)
	objStmRe    = regexp.MustCompile(`/Type\s*/ObjStm\b`)
	flateRe     = regexp.MustCompile(`/FlateDecode\b`)
)

// maxInflate bounds what CountPages decompresses from object streams.
const maxInflate = 64 << 20

// CountPages returns the number of pages of a PDF document: the largest
// /Count of its page tree nodes (the root's), looking into compressed
// object streams too. It never runs the document.
func CountPages(data []byte) (int, error) {
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		return 0, ErrPageCount
	}
	c := counter{budget: maxInflate}
	c.scan(data, true)
	if c.best <= 0 {
		return 0, ErrPageCount
	}
	return c.best, nil
}

type counter struct {
	best   int
	budget int
}

// scan walks the top-level dictionaries of b (skipping strings, comments
// and stream data), counts page tree nodes and, when streams is set,
// inflates object streams and scans them too.
func (c *counter) scan(b []byte, streams bool) {
	for i := 0; i < len(b); {
		switch {
		case b[i] == '%':
			i = skipLine(b, i)
		case b[i] == '(':
			i = skipString(b, i)
		case b[i] == '<' && i+1 < len(b) && b[i+1] == '<':
			end := dictEnd(b, i)
			dict := b[i:end]
			c.node(dict)
			i = end
			// A stream follows its dictionary.
			j := i
			for j < len(b) && isSpace(b[j]) {
				j++
			}
			if !bytes.HasPrefix(b[j:], []byte("stream")) {
				continue
			}
			j += len("stream")
			if j < len(b) && b[j] == '\r' {
				j++
			}
			if j < len(b) && b[j] == '\n' {
				j++
			}
			k := bytes.Index(b[j:], []byte("endstream"))
			if k < 0 {
				return
			}
			if streams && objStmRe.Match(own(dict)) && flateRe.Match(own(dict)) {
				if inner := c.inflate(b[j : j+k]); inner != nil {
					c.scan(inner, false)
				}
			}
			i = j + k + len("endstream")
		default:
			i++
		}
	}
}

// node records dict's /Count when it is a page tree node.
func (c *counter) node(dict []byte) {
	top := own(dict)
	if !typePagesRe.Match(top) || !kidsRe.Match(top) {
		return
	}
	if m := countRe.FindSubmatch(top); m != nil {
		if n, err := strconv.Atoi(string(m[1])); err == nil && n > c.best {
			c.best = n
		}
	}
}

func (c *counter) inflate(data []byte) []byte {
	if c.budget <= 0 {
		return nil
	}
	zr, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	defer zr.Close()
	out, _ := io.ReadAll(io.LimitReader(zr, int64(c.budget)))
	c.budget -= len(out)
	return out
}

// own returns dict (starting with "<<") without its nested dictionaries,
// so that only its own keys are matched.
func own(dict []byte) []byte {
	out := make([]byte, 0, len(dict))
	for i := 2; i < len(dict)-2; {
		switch {
		case dict[i] == '(':
			j := skipString(dict, i)
			out = append(out, dict[i:j]...)
			i = j
		case dict[i] == '<' && i+1 < len(dict) && dict[i+1] == '<':
			i = dictEnd(dict, i)
			out = append(out, ' ')
		default:
			out = append(out, dict[i])
			i++
		}
	}
	return out
}

// dictEnd returns the index just past the ">>" closing the dictionary
// opened at b[i:] ("<<"), or len(b).
func dictEnd(b []byte, i int) int {
	depth := 0
	for i < len(b) {
		switch {
		case b[i] == '(':
			i = skipString(b, i)
		case b[i] == '<' && i+1 < len(b) && b[i+1] == '<':
			depth++
			i += 2
		case b[i] == '>' && i+1 < len(b) && b[i+1] == '>':
			depth--
			i += 2
			if depth == 0 {
				return i
			}
		case b[i] == '<':
			// Hex string.
			for i < len(b) && b[i] != '>' {
				i++
			}
			i++
		default:
			i++
		}
	}
	return len(b)
}

// skipString returns the index just past the literal string at b[i:].
func skipString(b []byte, i int) int {
	depth := 0
	for i < len(b) {
		switch b[i] {
		case '\\':
			i += 2
			continue
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
		i++
	}
	return len(b)
}

func skipLine(b []byte, i int) int {
	for i < len(b) && b[i] != '\n' && b[i] != '\r' {
		i++
	}
	return i
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\n' || c == '\r' || c == '\t' || c == '\f' || c == 0
}
