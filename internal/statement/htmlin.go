package statement

import (
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// ParseHTML reads an HTML statement into the document model: the usual
// text elements, tables, images, TeX formulas in the text ($...$, \(...\),
// \[...\], $$...$$ and Polygon's $$$...$$$), and Polygon's layout (title
// header, section titles and sample tests). Scripts, styles and anything
// active are dropped.
func ParseHTML(src string) *Doc {
	root, err := html.Parse(strings.NewReader(src))
	d := &Doc{}
	if err != nil {
		d.Blocks = ParseMarkdown(src).Blocks
		return d
	}
	h := &htmlReader{doc: d}
	if t := find(root, atom.Title); t != nil {
		d.Title = strings.TrimSpace(textOf(t))
	}
	body := find(root, atom.Body)
	if body == nil {
		body = root
	}
	d.Blocks = h.blocks(body)
	d.Warnings = h.warn
	return d
}

type htmlReader struct {
	doc  *Doc
	warn []string
}

func find(n *html.Node, a atom.Atom) *html.Node {
	if n.Type == html.ElementNode && n.DataAtom == a {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if f := find(c, a); f != nil {
			return f
		}
	}
	return nil
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func hasClass(n *html.Node, c string) bool {
	for _, f := range strings.Fields(attr(n, "class")) {
		if f == c {
			return true
		}
	}
	return false
}

// textOf is the text content of a node.
func textOf(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		if n.Type == html.ElementNode && n.DataAtom == atom.Br {
			sb.WriteByte('\n')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}

var blockAtoms = map[atom.Atom]bool{
	atom.P: true, atom.Div: true, atom.H1: true, atom.H2: true, atom.H3: true, atom.H4: true, atom.H5: true,
	atom.H6: true, atom.Ul: true, atom.Ol: true, atom.Pre: true, atom.Table: true, atom.Blockquote: true,
	atom.Hr: true, atom.Section: true, atom.Article: true, atom.Main: true, atom.Center: true, atom.Figure: true,
	atom.Dl: true, atom.Header: true, atom.Footer: true, atom.Nav: true, atom.Aside: true, atom.Details: true,
}

func isBlockNode(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	if n.DataAtom == atom.Img {
		// an image alone on its line
		return true
	}
	return blockAtoms[n.DataAtom]
}

// blocks reads the children of n.
func (h *htmlReader) blocks(n *html.Node) []Block {
	var out []Block
	var inl []*html.Node
	flush := func() {
		if len(inl) == 0 {
			return
		}
		var in []Inline
		for _, c := range inl {
			in = append(in, h.inlines(c)...)
		}
		in = trimInlines(in)
		if len(in) > 0 {
			out = append(out, &Para{Text: in})
		}
		inl = nil
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.CommentNode {
			continue
		}
		if !isBlockNode(c) {
			inl = append(inl, c)
			continue
		}
		flush()
		out = append(out, h.block(c)...)
	}
	flush()
	return out
}

func (h *htmlReader) block(n *html.Node) []Block {
	switch n.DataAtom {
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		level := int(n.Data[1] - '0')
		return []Block{&Heading{Level: level, Text: trimInlines(h.children(n))}}
	case atom.P:
		in := trimInlines(h.children(n))
		if len(in) == 0 {
			return nil
		}
		return []Block{&Para{Text: in}}
	case atom.Ul, atom.Ol:
		l := &List{Ordered: n.DataAtom == atom.Ol, Start: 1}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && c.DataAtom == atom.Li {
				l.Items = append(l.Items, h.blocks(c))
			}
		}
		return []Block{l}
	case atom.Pre:
		return []Block{&Code{Text: strings.Trim(textOf(n), "\n")}}
	case atom.Table:
		return []Block{h.table(n)}
	case atom.Blockquote:
		return []Block{&Quote{Blocks: h.blocks(n)}}
	case atom.Hr:
		return []Block{&Rule{}}
	case atom.Img:
		return []Block{&Image{Src: attr(n, "src"), Alt: attr(n, "alt")}}
	case atom.Dl:
		var out []Block
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			if c.DataAtom == atom.Dt {
				out = append(out, &Para{Text: []Inline{&Styled{Style: 'b', Text: trimInlines(h.children(c))}}})
			} else if c.DataAtom == atom.Dd {
				out = append(out, &Quote{Blocks: h.blocks(c)})
			}
		}
		return out
	}
	// Polygon's layout.
	switch {
	case hasClass(n, "header") && find(n, atom.Div) != nil:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && hasClass(c, "title") {
				h.doc.Title = strings.TrimSpace(textOf(c))
			}
		}
		return nil
	case hasClass(n, "section-title"):
		return []Block{&Heading{Level: 2, Text: trimInlines(h.children(n))}}
	case hasClass(n, "sample-tests") || hasClass(n, "sample-test"):
		h.samples(n)
		return []Block{&ExamplesHere{}}
	}
	return h.blocks(n)
}

// samples reads Polygon's sample tests: .input pre and .output pre pairs.
func (h *htmlReader) samples(n *html.Node) {
	var in string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.DataAtom == atom.Pre {
			p := n.Parent
			switch {
			case p != nil && hasClass(p, "input"):
				in = textOf(n)
			case p != nil && hasClass(p, "output"):
				h.doc.Examples = append(h.doc.Examples, Example{Input: exampleText(in), Output: exampleText(textOf(n))})
				in = ""
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
}

func (h *htmlReader) table(n *html.Node) *Table {
	t := &Table{}
	var rows func(*html.Node)
	rows = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			switch c.DataAtom {
			case atom.Thead, atom.Tbody, atom.Tfoot:
				rows(c)
			case atom.Tr:
				var cells [][]Inline
				header := true
				for td := c.FirstChild; td != nil; td = td.NextSibling {
					if td.Type == html.ElementNode && (td.DataAtom == atom.Td || td.DataAtom == atom.Th) {
						if td.DataAtom == atom.Td {
							header = false
						}
						cells = append(cells, trimInlines(h.children(td)))
					}
				}
				if len(t.Rows) == 0 && header {
					t.Header = true
				}
				t.Rows = append(t.Rows, cells)
			}
		}
	}
	rows(n)
	return t
}

func (h *htmlReader) children(n *html.Node) []Inline {
	var out []Inline
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		out = append(out, h.inlines(c)...)
	}
	return out
}

func (h *htmlReader) inlines(n *html.Node) []Inline {
	switch n.Type {
	case html.TextNode:
		return mathText(strings.ReplaceAll(n.Data, "\n", " "), &h.warn)
	case html.ElementNode:
	default:
		return nil
	}
	switch n.DataAtom {
	case atom.Script, atom.Style, atom.Head, atom.Title, atom.Noscript, atom.Iframe, atom.Object, atom.Embed,
		atom.Form, atom.Input, atom.Button, atom.Select, atom.Textarea, atom.Svg, atom.Math:
		return nil
	case atom.Br:
		return []Inline{&Break{}}
	case atom.B, atom.Strong:
		return []Inline{&Styled{Style: 'b', Text: h.children(n)}}
	case atom.I, atom.Em, atom.Cite, atom.Var, atom.Dfn:
		return []Inline{&Styled{Style: 'i', Text: h.children(n)}}
	case atom.U, atom.Ins:
		return []Inline{&Styled{Style: 'u', Text: h.children(n)}}
	case atom.S, atom.Strike, atom.Del:
		return []Inline{&Styled{Style: 's', Text: h.children(n)}}
	case atom.Code, atom.Tt, atom.Kbd, atom.Samp:
		return []Inline{&CodeSpan{S: textOf(n)}}
	case atom.A:
		return []Inline{&Link{URL: attr(n, "href"), Text: h.children(n)}}
	case atom.Sub, atom.Sup:
		body := &MNode{Kind: MText, Text: strings.TrimSpace(textOf(n))}
		s := &MNode{Kind: MScripts, Kids: []*MNode{{Kind: MRow}, nil, nil}}
		if n.DataAtom == atom.Sub {
			s.Kids[1] = body
		} else {
			s.Kids[2] = body
		}
		return []Inline{&InlineMath{Math: &Math{TeX: textOf(n), Root: s}}}
	case atom.Img:
		alt := attr(n, "alt")
		if alt == "" {
			return nil
		}
		return []Inline{&Text{S: "[" + alt + "]"}}
	}
	return h.children(n)
}

// mathText splits text at TeX formulas.
func mathText(s string, warn *[]string) []Inline {
	var out []Inline
	var text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			out = append(out, &Text{S: text.String()})
			text.Reset()
		}
	}
	for i := 0; i < len(s); {
		switch {
		case s[i] == '$':
			if tex, n, ok := dollarMath(s[i:]); ok {
				flush()
				display := strings.HasPrefix(s[i:], "$$") && !strings.HasPrefix(s[i:], "$$$")
				m, w := ParseMath(tex, display)
				*warn = append(*warn, w...)
				out = append(out, &InlineMath{Math: m})
				i += n
				continue
			}
		case strings.HasPrefix(s[i:], `\(`) || strings.HasPrefix(s[i:], `\[`):
			closer := `\)`
			if s[i+1] == '[' {
				closer = `\]`
			}
			if end := strings.Index(s[i+2:], closer); end >= 0 {
				flush()
				m, w := ParseMath(s[i+2:i+2+end], closer == `\]`)
				*warn = append(*warn, w...)
				out = append(out, &InlineMath{Math: m})
				i += end + 4
				continue
			}
		}
		text.WriteByte(s[i])
		i++
	}
	flush()
	return out
}
