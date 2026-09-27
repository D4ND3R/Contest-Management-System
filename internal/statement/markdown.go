package statement

import (
	"regexp"
	"strconv"
	"strings"
)

// ParseMarkdown reads a Markdown statement (CommonMark basics, GFM tables
// and strike-through) with TeX formulas: $...$ and \(...\) inline, $$...$$
// and \[...\] displayed.
func ParseMarkdown(src string) *Doc {
	p := &mdParser{}
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(src, "\r\n", "\n"), "\t", "    "), "\n")
	d := &Doc{Blocks: p.blocks(lines)}
	d.Warnings = p.warn
	// A first-level heading at the very top is the title.
	if len(d.Blocks) > 0 {
		if h, ok := d.Blocks[0].(*Heading); ok && h.Level == 1 {
			d.Title = plainText(h.Text)
			d.Blocks = d.Blocks[1:]
		}
	}
	return d
}

type mdParser struct{ warn []string }

var (
	mdHeading = regexp.MustCompile(`^ {0,3}(#{1,6})\s+(.*?)\s*#*\s*$`)
	mdFence   = regexp.MustCompile("^ {0,3}(```+|~~~+)\\s*([^`]*)$")
	mdRule    = regexp.MustCompile(`^ {0,3}([-*_])(\s*[-*_]){2,}\s*$`)
	mdBullet  = regexp.MustCompile(`^( *)([-*+])\s+(.*)$`)
	mdOrdered = regexp.MustCompile(`^( *)(\d{1,9})[.)]\s+(.*)$`)
	mdTableSe = regexp.MustCompile(`^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$`)
	mdImage   = regexp.MustCompile(`^\s*!\[([^\]]*)\]\(\s*<?([^)\s>]+)>?(?:\s+"[^"]*")?\s*\)\s*$`)
	mdSetext  = regexp.MustCompile(`^ {0,3}(=+|-+)\s*$`)
)

func blank(s string) bool { return strings.TrimSpace(s) == "" }

func indentOf(s string) int { return len(s) - len(strings.TrimLeft(s, " ")) }

func (p *mdParser) blocks(lines []string) []Block {
	var out []Block
	for i := 0; i < len(lines); {
		line := lines[i]
		if blank(line) {
			i++
			continue
		}
		t := strings.TrimSpace(line)
		// fenced code
		if m := mdFence.FindStringSubmatch(line); m != nil {
			fence := m[1]
			var body []string
			i++
			for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), fence) {
				body = append(body, lines[i])
				i++
			}
			i++
			out = append(out, &Code{Text: strings.Join(dedent(body), "\n")})
			continue
		}
		// displayed formula
		if strings.HasPrefix(t, "$$") || strings.HasPrefix(t, `\[`) {
			closer := "$$"
			if strings.HasPrefix(t, `\[`) {
				closer = `\]`
			}
			body := strings.TrimSpace(t[2:])
			if strings.HasSuffix(body, closer) && len(body) >= len(closer) {
				body = strings.TrimSuffix(body, closer)
				i++
			} else {
				var parts []string
				if body != "" {
					parts = append(parts, body)
				}
				i++
				for i < len(lines) {
					l := strings.TrimSpace(lines[i])
					i++
					if strings.HasSuffix(l, closer) {
						parts = append(parts, strings.TrimSuffix(l, closer))
						break
					}
					parts = append(parts, l)
				}
				body = strings.Join(parts, "\n")
			}
			m, w := ParseMath(body, true)
			p.warn = append(p.warn, w...)
			out = append(out, &Display{Math: m})
			continue
		}
		if m := mdHeading.FindStringSubmatch(line); m != nil {
			out = append(out, &Heading{Level: len(m[1]), Text: p.inlines(m[2])})
			i++
			continue
		}
		if t == "{{examples}}" {
			out = append(out, &ExamplesHere{})
			i++
			continue
		}
		if mdRule.MatchString(line) {
			out = append(out, &Rule{})
			i++
			continue
		}
		if m := mdImage.FindStringSubmatch(line); m != nil {
			out = append(out, &Image{Alt: m[1], Src: m[2]})
			i++
			continue
		}
		// block quote
		if strings.HasPrefix(t, ">") {
			var body []string
			for i < len(lines) && !blank(lines[i]) {
				l := strings.TrimSpace(lines[i])
				l = strings.TrimPrefix(l, ">")
				l = strings.TrimPrefix(l, " ")
				body = append(body, l)
				i++
			}
			out = append(out, &Quote{Blocks: p.blocks(body)})
			continue
		}
		// list
		if mdBullet.MatchString(line) || mdOrdered.MatchString(line) {
			var l *List
			l, i = p.list(lines, i)
			out = append(out, l)
			continue
		}
		// indented code
		if indentOf(line) >= 4 {
			var body []string
			for i < len(lines) && (blank(lines[i]) || indentOf(lines[i]) >= 4) {
				body = append(body, lines[i])
				i++
			}
			for len(body) > 0 && blank(body[len(body)-1]) {
				body = body[:len(body)-1]
			}
			out = append(out, &Code{Text: strings.Join(dedent(body), "\n")})
			continue
		}
		// table: a row, then a separator
		if strings.Contains(line, "|") && i+1 < len(lines) && mdTableSe.MatchString(lines[i+1]) && strings.Contains(lines[i+1], "-") {
			tb := &Table{Header: true, Align: tableAlign(lines[i+1])}
			tb.Rows = append(tb.Rows, p.cells(line))
			i += 2
			for i < len(lines) && strings.Contains(lines[i], "|") && !blank(lines[i]) {
				tb.Rows = append(tb.Rows, p.cells(lines[i]))
				i++
			}
			out = append(out, tb)
			continue
		}
		// paragraph (maybe a setext heading)
		var para []string
		for i < len(lines) && !blank(lines[i]) {
			l := lines[i]
			if len(para) > 0 && (mdHeading.MatchString(l) || mdFence.MatchString(l) || strings.HasPrefix(strings.TrimSpace(l), "$$") ||
				strings.HasPrefix(strings.TrimSpace(l), `\[`) || strings.HasPrefix(strings.TrimSpace(l), ">") ||
				mdBullet.MatchString(l) || mdOrdered.MatchString(l)) {
				break
			}
			if len(para) > 0 && mdSetext.MatchString(l) {
				level := 1
				if strings.HasPrefix(strings.TrimSpace(l), "-") {
					level = 2
				}
				out = append(out, &Heading{Level: level, Text: p.inlines(strings.Join(para, " "))})
				para = nil
				i++
				break
			}
			para = append(para, l)
			i++
		}
		if len(para) > 0 {
			out = append(out, &Para{Text: p.inlines(joinLines(para))})
		}
	}
	return out
}

// joinLines keeps hard breaks (two trailing spaces or a backslash).
func joinLines(ls []string) string {
	var sb strings.Builder
	for i, l := range ls {
		if i > 0 {
			sb.WriteByte('\n')
		}
		switch {
		case strings.HasSuffix(l, "  ") && i < len(ls)-1:
			sb.WriteString(strings.TrimSpace(l) + "\x00")
		case strings.HasSuffix(l, "\\") && !strings.HasSuffix(l, "\\\\") && i < len(ls)-1:
			sb.WriteString(strings.TrimSpace(strings.TrimSuffix(l, "\\")) + "\x00")
		default:
			sb.WriteString(strings.TrimSpace(l))
		}
	}
	return sb.String()
}

func dedent(ls []string) []string {
	min := -1
	for _, l := range ls {
		if blank(l) {
			continue
		}
		if n := indentOf(l); min < 0 || n < min {
			min = n
		}
	}
	if min <= 0 {
		return ls
	}
	out := make([]string, len(ls))
	for i, l := range ls {
		if len(l) >= min {
			out[i] = l[min:]
		}
	}
	return out
}

func (p *mdParser) list(lines []string, i int) (*List, int) {
	first := lines[i]
	ordered := mdOrdered.MatchString(first) && !mdBullet.MatchString(first)
	base := indentOf(first)
	l := &List{Ordered: ordered, Start: 1}
	if ordered {
		m := mdOrdered.FindStringSubmatch(first)
		l.Start, _ = strconv.Atoi(m[2])
	}
	for i < len(lines) {
		line := lines[i]
		var m []string
		if ordered {
			m = mdOrdered.FindStringSubmatch(line)
		} else {
			m = mdBullet.FindStringSubmatch(line)
		}
		if m == nil || indentOf(line) != base {
			break
		}
		content := []string{m[len(m)-1]}
		contIndent := base + len(line) - len(strings.TrimLeft(line, " ")) - base
		_ = contIndent
		i++
		for i < len(lines) {
			l2 := lines[i]
			if blank(l2) {
				// A blank line continues the item only if indented text follows.
				if i+1 < len(lines) && !blank(lines[i+1]) && indentOf(lines[i+1]) > base {
					content = append(content, "")
					i++
					continue
				}
				break
			}
			if indentOf(l2) <= base && (mdBullet.MatchString(l2) || mdOrdered.MatchString(l2)) {
				break
			}
			if indentOf(l2) > base {
				content = append(content, strings.TrimPrefix(l2, strings.Repeat(" ", min(indentOf(l2), base+2))))
			} else {
				content = append(content, l2) // lazy continuation
			}
			i++
		}
		l.Items = append(l.Items, p.blocks(content))
		// skip blank lines between items
		for i < len(lines) && blank(lines[i]) && i+1 < len(lines) && indentOf(lines[i+1]) == base &&
			(mdBullet.MatchString(lines[i+1]) || mdOrdered.MatchString(lines[i+1])) {
			i++
		}
	}
	return l, i
}

func tableAlign(sep string) []byte {
	var out []byte
	for _, c := range splitRow(sep) {
		c = strings.TrimSpace(c)
		switch {
		case strings.HasPrefix(c, ":") && strings.HasSuffix(c, ":"):
			out = append(out, 'c')
		case strings.HasSuffix(c, ":"):
			out = append(out, 'r')
		default:
			out = append(out, 'l')
		}
	}
	return out
}

func splitRow(line string) []string {
	s := strings.TrimSpace(line)
	s = strings.TrimPrefix(s, "|")
	if strings.HasSuffix(s, "|") && !strings.HasSuffix(s, `\|`) {
		s = s[:len(s)-1]
	}
	var cells []string
	var cur strings.Builder
	inCode, inMath := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s) && s[i+1] == '|':
			cur.WriteByte('|')
			i++
		case c == '`':
			inCode = !inCode
			cur.WriteByte(c)
		case c == '$':
			inMath = !inMath
			cur.WriteByte(c)
		case c == '|' && !inCode && !inMath:
			cells = append(cells, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	return append(cells, cur.String())
}

func (p *mdParser) cells(line string) [][]Inline {
	var out [][]Inline
	for _, c := range splitRow(line) {
		out = append(out, p.inlines(strings.TrimSpace(c)))
	}
	return out
}

// inlines parses inline markup.
func (p *mdParser) inlines(s string) []Inline {
	var out []Inline
	var text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			out = append(out, &Text{S: text.String()})
			text.Reset()
		}
	}
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == 0: // hard break marker
			flush()
			out = append(out, &Break{})
			i++
			if i < len(s) && s[i] == '\n' {
				i++
			}
			continue
		case c == '\n':
			text.WriteByte(' ')
			i++
			continue
		case c == '\\' && i+1 < len(s):
			n := s[i+1]
			if n == '(' {
				if end := strings.Index(s[i+2:], `\)`); end >= 0 {
					flush()
					out = append(out, p.math(s[i+2:i+2+end], false))
					i += end + 4
					continue
				}
			}
			if n == '[' {
				if end := strings.Index(s[i+2:], `\]`); end >= 0 {
					flush()
					out = append(out, p.math(s[i+2:i+2+end], false))
					i += end + 4
					continue
				}
			}
			if strings.IndexByte("\\`*_{}[]()#+-.!|$~<>\"'", n) >= 0 {
				text.WriteByte(n)
				i += 2
				continue
			}
			text.WriteByte(c)
			i++
			continue
		case c == '`':
			n := 1
			for i+n < len(s) && s[i+n] == '`' {
				n++
			}
			ticks := s[i : i+n]
			if end := strings.Index(s[i+n:], ticks); end >= 0 {
				flush()
				code := s[i+n : i+n+end]
				if len(code) > 2 && code[0] == ' ' && code[len(code)-1] == ' ' {
					code = code[1 : len(code)-1]
				}
				out = append(out, &CodeSpan{S: strings.ReplaceAll(code, "\n", " ")})
				i += n + end + n
				continue
			}
			text.WriteString(ticks)
			i += n
			continue
		case c == '$':
			if tex, n, ok := dollarMath(s[i:]); ok {
				flush()
				out = append(out, p.math(tex, false))
				i += n
				continue
			}
		case c == '!' && i+1 < len(s) && s[i+1] == '[':
			if alt, url, n, ok := linkAt(s[i+1:]); ok {
				flush()
				out = append(out, &Text{S: "[" + alt + "]"})
				_ = url
				i += 1 + n
				continue
			}
		case c == '[':
			if label, url, n, ok := linkAt(s[i:]); ok {
				flush()
				out = append(out, &Link{URL: url, Text: p.inlines(label)})
				i += n
				continue
			}
		case c == '<':
			if end := strings.IndexByte(s[i:], '>'); end > 0 {
				u := s[i+1 : i+end]
				if safeURL(u) && !strings.ContainsAny(u, " \n") {
					flush()
					out = append(out, &Link{URL: u, Text: []Inline{&Text{S: u}}})
					i += end + 1
					continue
				}
			}
		case c == '*' || c == '_' || c == '~':
			if st, inner, n, ok := emphasis(s[i:], i == 0 || !isWordByte(s[i-1])); ok {
				flush()
				out = append(out, &Styled{Style: st, Text: p.inlines(inner)})
				i += n
				continue
			}
		}
		text.WriteByte(c)
		i++
	}
	flush()
	return out
}

func (p *mdParser) math(tex string, display bool) Inline {
	m, w := ParseMath(tex, display)
	p.warn = append(p.warn, w...)
	return &InlineMath{Math: m}
}

func isWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80
}

// dollarMath recognises $x$ (not "$5 and $10"): the opening $ is followed
// by a non-space, the closing one preceded by a non-space and not followed
// by a digit. $$$x$$$ (Polygon's inline delimiters) is accepted too.
func dollarMath(s string) (string, int, bool) {
	if strings.HasPrefix(s, "$$$") {
		if end := strings.Index(s[3:], "$$$"); end > 0 {
			return s[3 : 3+end], end + 6, true
		}
	}
	if strings.HasPrefix(s, "$$") {
		if end := strings.Index(s[2:], "$$"); end > 0 {
			return s[2 : 2+end], end + 4, true
		}
		return "", 0, false
	}
	if len(s) < 3 || s[1] == ' ' || s[1] == '$' {
		return "", 0, false
	}
	for j := 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case '$':
			if s[j-1] == ' ' {
				continue
			}
			if j+1 < len(s) && s[j+1] >= '0' && s[j+1] <= '9' {
				continue
			}
			return s[1:j], j + 1, true
		}
	}
	return "", 0, false
}

// linkAt reads [label](url) at the start of s.
func linkAt(s string) (label, url string, n int, ok bool) {
	depth := 0
	for j := 0; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				if j+1 < len(s) && s[j+1] == '(' {
					end := strings.IndexByte(s[j+2:], ')')
					if end < 0 {
						return "", "", 0, false
					}
					u := strings.TrimSpace(s[j+2 : j+2+end])
					if k := strings.Index(u, ` "`); k > 0 {
						u = strings.TrimSpace(u[:k])
					}
					u = strings.Trim(u, "<>")
					return s[1:j], u, j + 3 + end, true
				}
				return "", "", 0, false
			}
		}
	}
	return "", "", 0, false
}

// emphasis reads *x*, **x**, _x_, __x__ or ~~x~~ at the start of s.
func emphasis(s string, leftFlank bool) (byte, string, int, bool) {
	c := s[0]
	n := 1
	for n < len(s) && s[n] == c && n < 3 {
		n++
	}
	if c == '~' && n != 2 {
		return 0, "", 0, false
	}
	if c == '_' && !leftFlank {
		return 0, "", 0, false
	}
	if n >= len(s) || s[n] == ' ' || s[n] == '\n' {
		return 0, "", 0, false
	}
	delim := s[:n]
	for j := n; j+n <= len(s); j++ {
		if s[j] == '\\' {
			j++
			continue
		}
		if s[j] == '`' || s[j] == '$' { // skip code and math
			if end := strings.IndexByte(s[j+1:], s[j]); end >= 0 {
				j += end + 1
				continue
			}
		}
		if strings.HasPrefix(s[j:], delim) && s[j-1] != ' ' && (j+n >= len(s) || s[j+n] != c) {
			if c == '_' && j+n < len(s) && isWordByte(s[j+n]) {
				continue
			}
			inner := s[n:j]
			switch {
			case c == '~':
				return 's', inner, j + n, true
			case n == 1:
				return 'i', inner, j + n, true
			case n == 2:
				return 'b', inner, j + n, true
			default:
				return 'b', delim[:1] + inner + delim[:1], j + n, true
			}
		}
	}
	return 0, "", 0, false
}

// plainText flattens inlines (titles).
func plainText(in []Inline) string {
	var sb strings.Builder
	for _, i := range in {
		switch i := i.(type) {
		case *Text:
			sb.WriteString(i.S)
		case *Styled:
			sb.WriteString(plainText(i.Text))
		case *CodeSpan:
			sb.WriteString(i.S)
		case *InlineMath:
			sb.WriteString(i.Math.TeX)
		case *Link:
			sb.WriteString(plainText(i.Text))
		}
	}
	return sb.String()
}
