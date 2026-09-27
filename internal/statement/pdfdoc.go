package statement

import (
	"fmt"
	"strings"

	"github.com/D4ND3R/Contest-Management-System/internal/pdf"
)

// PDFOptions says what goes around the statement in the PDF.
type PDFOptions struct {
	Title    string      // the task's title
	Subtitle string      // the contest's name
	Info     [][2]string // limits and files: {"Time limit", "1 s"}, ...
	Labels   Labels
	Examples []Example
	// Image returns an attached image's bytes (JPEG or PNG).
	Image func(name string) ([]byte, bool)
	// Footer is written on every page before "n / N" (task name).
	Footer string
}

// Layout constants, in points.
const (
	pageMargin = 56.0
	bodySize   = 11.0
	bodyLead   = 1.32 // line height as a multiple of the size
	codeSize   = 9.2
	paraGap    = 7.0
)

// PDF typesets the statement.
func (d *Doc) PDF(o PDFOptions) []byte {
	if o.Labels.Input == "" {
		o.Labels = DefaultLabels
	}
	t := &typesetter{doc: pdf.New(), o: o}
	t.doc.Title = o.Title
	t.doc.Compress = true
	t.newPage()
	t.header()
	examples := append(append([]Example(nil), d.Examples...), o.Examples...)
	t.examples = examples
	t.blocks(d.Blocks, 0)
	if !t.examplesDone && len(examples) > 0 {
		t.examplesSection()
	}
	t.footers()
	return t.doc.Bytes()
}

type typesetter struct {
	doc          *pdf.Doc
	o            PDFOptions
	page         *pdf.Page
	y            float64 // top of the free space
	examples     []Example
	examplesDone bool
}

func (t *typesetter) width() float64 { return pdf.A4Width - 2*pageMargin }

func (t *typesetter) bottom() float64 { return pageMargin + 14 }

func (t *typesetter) newPage() {
	t.page = t.doc.AddPage()
	t.y = pdf.A4Height - pageMargin
}

// need starts a new page unless h points fit.
func (t *typesetter) need(h float64) {
	if t.y-h < t.bottom() {
		t.newPage()
	}
}

func (t *typesetter) header() {
	w := t.width()
	x := pageMargin
	title := t.o.Title
	size := 20.0
	for pdf.HelveticaBold.Width(title, size) > w && size > 12 {
		size--
	}
	t.page.Draw(x, t.y-size, size, pdf.HelveticaBold, title)
	t.y -= size + 6
	if t.o.Subtitle != "" {
		t.page.SetColor(0.35, 0.38, 0.45)
		t.page.Draw(x, t.y-10, 10, pdf.Helvetica, t.o.Subtitle)
		t.page.ResetColor()
		t.y -= 16
	}
	if len(t.o.Info) > 0 {
		// Two columns of "label: value" in a tinted box.
		rows := (len(t.o.Info) + 1) / 2
		lh := 15.0
		h := float64(rows)*lh + 10
		t.y -= 4
		t.page.FillColorRect(x, t.y-h, w, h, 0.94, 0.95, 0.97)
		for i, kv := range t.o.Info {
			col := i % 2
			row := i / 2
			cx := x + 10 + float64(col)*w/2
			cy := t.y - 5 - float64(row+1)*lh + 4
			t.page.Draw(cx, cy, 9.5, pdf.HelveticaBold, kv[0]+":")
			t.page.Draw(cx+pdf.HelveticaBold.Width(kv[0]+": ", 9.5), cy, 9.5, pdf.Helvetica, kv[1])
		}
		t.y -= h + 4
	}
	t.page.Line(x, t.y, x+w, t.y, 0.6)
	t.y -= 14
}

func (t *typesetter) footers() {
	n := t.doc.Pages()
	for i := 0; i < n; i++ {
		pg := t.doc.Page(i)
		s := fmt.Sprintf("%d / %d", i+1, n)
		if t.o.Footer != "" {
			s = t.o.Footer + "  ·  " + s
		}
		pg.SetColor(0.45, 0.48, 0.55)
		pg.Draw(pdf.A4Width/2-pdf.Helvetica.Width(s, 8)/2, pageMargin-18, 8, pdf.Helvetica, s)
		pg.ResetColor()
	}
}

func (t *typesetter) blocks(bs []Block, indent float64) {
	for i, b := range bs {
		// A heading keeps at least two lines of what follows.
		if _, ok := b.(*Heading); ok && i+1 < len(bs) {
			t.need(bodySize * 5)
		}
		t.block(b, indent)
	}
}

func (t *typesetter) block(b Block, indent float64) {
	x := pageMargin + indent
	w := t.width() - indent
	switch b := b.(type) {
	case *Heading:
		size := map[int]float64{1: 15, 2: 13, 3: 11.5, 4: 11}[min(max(b.Level, 1), 4)]
		t.y -= 6
		t.need(size * 2)
		t.paragraph(b.Text, x, w, size, pdf.HelveticaBold, false)
		t.y -= 2
	case *Para:
		t.paragraph(b.Text, x, w, bodySize, pdf.TimesRoman, true)
		t.y -= paraGap
	case *List:
		for i, it := range b.Items {
			mark := "•"
			if b.Ordered {
				mark = fmt.Sprintf("%d.", b.Start+i)
			}
			t.need(bodySize * 1.6)
			// The mark goes on the baseline of the item's first line.
			markPage, markY := t.page, t.y-bodySize
			if len(it) > 0 {
				if p, ok := it[0].(*Para); ok {
					markPage, markY = t.paragraph(p.Text, pageMargin+indent+18, t.width()-indent-18, bodySize, pdf.TimesRoman, true)
					t.y -= 3
					it = it[1:]
				}
			}
			t.blocksTight(it, indent+18)
			markPage.Draw(x+14-pdf.TimesRoman.Width(mark, bodySize), markY, bodySize, pdf.TimesRoman, mark)
		}
		t.y -= paraGap - 3
	case *Code:
		t.code(b.Text, x, w)
		t.y -= paraGap
	case *Display:
		box := layoutMath(b.Math, bodySize)
		h := box.h + box.d + 8
		t.need(h)
		bx := x + (w-box.w)/2
		if box.w > w {
			bx = x
		}
		box.draw(t.page, bx, t.y-4-box.h)
		t.y -= h + 2
	case *Table:
		t.table(b, x, w)
		t.y -= paraGap
	case *Quote:
		startPage, startY := t.page, t.y
		t.blocks(b.Blocks, indent+14)
		if t.page == startPage {
			t.page.SetColor(0.7, 0.72, 0.78)
			t.page.Line(x+4, startY, x+4, t.y+paraGap, 2)
			t.page.ResetColor()
		}
	case *Rule:
		t.need(12)
		t.page.Line(x, t.y-5, x+w, t.y-5, 0.5)
		t.y -= 12
	case *Image:
		t.image(b, x, w)
	case *ExamplesHere:
		if !t.examplesDone && len(t.examples) > 0 {
			t.examplesSection()
		}
	}
}

// blocksTight lays out list items (smaller gaps).
func (t *typesetter) blocksTight(bs []Block, indent float64) {
	for _, b := range bs {
		if p, ok := b.(*Para); ok {
			t.paragraph(p.Text, pageMargin+indent, t.width()-indent, bodySize, pdf.TimesRoman, true)
			t.y -= 3
			continue
		}
		t.block(b, indent)
	}
}

// ---------------------------------------------------------------- text

// item is a piece of a line: a word (or part of one), a space or a box.
type item struct {
	w, h, d float64
	space   bool
	brk     bool
	draw    func(pg *pdf.Page, x, y float64)
	link    string
}

// fontFor applies a style to a base font.
func fontFor(base pdf.Font, italic, bold bool) pdf.Font {
	switch base {
	case pdf.TimesRoman, pdf.TimesItalic, pdf.TimesBold, pdf.TimesBoldItalic:
		switch {
		case italic && bold:
			return pdf.TimesBoldItalic
		case italic:
			return pdf.TimesItalic
		case bold:
			return pdf.TimesBold
		}
		return pdf.TimesRoman
	case pdf.Helvetica, pdf.HelveticaBold, pdf.HelveticaOblique, pdf.HelveticaBoldOblique:
		switch {
		case italic && bold, italic && base == pdf.HelveticaBold:
			return pdf.HelveticaBoldOblique
		case italic:
			return pdf.HelveticaOblique
		case bold:
			return pdf.HelveticaBold
		}
		return base
	}
	return base
}

type style struct {
	base              pdf.Font
	size              float64
	italic, bold      bool
	underline, strike bool
	link              string
}

func (t *typesetter) items(in []Inline, st style, out []item) []item {
	for _, i := range in {
		switch i := i.(type) {
		case *Text:
			out = t.textItems(i.S, st, out)
		case *Styled:
			s2 := st
			switch i.Style {
			case 'i':
				s2.italic = !s2.italic
			case 'b':
				s2.bold = true
			case 'u':
				s2.underline = true
			case 's':
				s2.strike = true
			}
			out = t.items(i.Text, s2, out)
		case *CodeSpan:
			s2 := st
			s2.base = pdf.Courier
			s2.size = st.size * 0.92
			s2.italic, s2.bold = false, false
			out = t.textItems(i.S, s2, out)
		case *InlineMath:
			box := layoutMath(i.Math, st.size)
			out = append(out, item{w: box.w, h: box.h, d: box.d, draw: box.draw})
		case *Link:
			s2 := st
			s2.link = i.URL
			out = t.items(i.Text, s2, out)
		case *Break:
			out = append(out, item{brk: true})
		}
	}
	return out
}

// textItems splits text into words and spaces.
func (t *typesetter) textItems(s string, st style, out []item) []item {
	f := fontFor(st.base, st.italic, st.bold)
	spaceW := f.Width(" ", st.size)
	words := strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == '\n' || r == '\t' })
	if strings.HasPrefix(s, " ") || strings.HasPrefix(s, "\n") {
		out = append(out, item{w: spaceW, space: true})
	}
	for i, w := range words {
		if i > 0 {
			out = append(out, item{w: spaceW, space: true})
		}
		b := glyphs(w, st.size, f)
		it := item{w: b.w, h: max(b.h, st.size*0.7), d: max(b.d, st.size*0.2), draw: b.draw, link: st.link}
		if st.link != "" || st.underline || st.strike {
			inner := it.draw
			wd := b.w
			link, under, strike := st.link, st.underline || st.link != "", st.strike
			size := st.size
			it.draw = func(pg *pdf.Page, x, y float64) {
				if link != "" {
					pg.SetColor(0.1, 0.3, 0.75)
				}
				inner(pg, x, y)
				if under {
					pg.Line(x, y-size*0.12, x+wd, y-size*0.12, 0.5)
				}
				if strike {
					pg.Line(x, y+size*0.28, x+wd, y+size*0.28, 0.5)
				}
				if link != "" {
					pg.ResetColor()
					pg.Link(x, y-size*0.2, wd, size, link)
				}
			}
		}
		out = append(out, it)
	}
	if len(words) > 0 && (strings.HasSuffix(s, " ") || strings.HasSuffix(s, "\n")) {
		out = append(out, item{w: spaceW, space: true})
	}
	return out
}

// paragraph breaks items into lines (justified when asked) and draws
// them; it returns the page and baseline of the first line.
func (t *typesetter) paragraph(in []Inline, x, w, size float64, base pdf.Font, justify bool) (*pdf.Page, float64) {
	firstPage, firstBase := t.page, t.y-size
	its := t.items(in, style{base: base, size: size}, nil)
	var lines [][]item
	var cur []item
	curW := 0.0
	push := func() {
		// no spaces at the ends of a line
		for len(cur) > 0 && cur[len(cur)-1].space {
			cur = cur[:len(cur)-1]
		}
		lines = append(lines, cur)
		cur, curW = nil, 0
	}
	for _, it := range its {
		if it.brk {
			push()
			continue
		}
		if it.space && len(cur) == 0 {
			continue
		}
		if !it.space && curW+it.w > w && len(cur) > 0 {
			push()
		}
		cur = append(cur, it)
		curW += it.w
	}
	if len(cur) > 0 {
		push()
	}
	for li, line := range lines {
		h, d := size*0.75, size*0.25
		natural := 0.0
		spaces := 0
		for _, it := range line {
			h, d = max(h, it.h), max(d, it.d)
			natural += it.w
			if it.space {
				spaces++
			}
		}
		lead := (bodyLead - 1) * size
		t.need(h + d + lead)
		base := t.y - lead/2 - h
		if li == 0 {
			firstPage, firstBase = t.page, base
		}
		extra := 0.0
		last := li == len(lines)-1
		if justify && !last && spaces > 0 && w > natural {
			extra = (w - natural) / float64(spaces)
			if extra > size*0.6 {
				extra = 0
			}
		}
		cx := x
		for _, it := range line {
			if it.space {
				cx += it.w + extra
				continue
			}
			if it.draw != nil {
				it.draw(t.page, cx, base)
			}
			cx += it.w
		}
		t.y = base - d - lead/2
	}
	return firstPage, firstBase
}

// code draws preformatted text in a tinted box, wrapping long lines.
func (t *typesetter) code(s string, x, w float64) {
	lines := wrapMono(strings.Split(strings.TrimRight(s, "\n"), "\n"), w-12, codeSize)
	lh := codeSize * 1.25
	for len(lines) > 0 {
		t.need(lh + 8)
		fit := int((t.y - t.bottom() - 8) / lh)
		n := min(max(fit, 1), len(lines))
		h := float64(n)*lh + 8
		t.page.FillColorRect(x, t.y-h, w, h, 0.95, 0.96, 0.97)
		for i := 0; i < n; i++ {
			t.page.Draw(x+6, t.y-4-float64(i+1)*lh+codeSize*0.28, codeSize, pdf.Courier, lines[i])
		}
		t.y -= h
		lines = lines[n:]
		if len(lines) > 0 {
			t.newPage()
		}
	}
}

// wrapMono wraps lines of a monospace text to a width.
func wrapMono(lines []string, w, size float64) []string {
	per := max(int(w/(size*0.6)), 8)
	var out []string
	for _, l := range lines {
		l = strings.ReplaceAll(l, "\t", "    ")
		r := []rune(l)
		for len(r) > per {
			out = append(out, string(r[:per]))
			r = r[per:]
		}
		out = append(out, string(r))
	}
	return out
}

// table lays out a table: column widths from the content, cells wrapped.
func (t *typesetter) table(tb *Table, x, w float64) {
	cols := 0
	for _, r := range tb.Rows {
		cols = max(cols, len(r))
	}
	if cols == 0 {
		return
	}
	size := bodySize - 1
	pad := 4.0
	natural := make([]float64, cols)
	for _, r := range tb.Rows {
		for j, c := range r {
			sum := 0.0
			for _, it := range t.items(c, style{base: pdf.TimesRoman, size: size}, nil) {
				sum += it.w
			}
			natural[j] = max(natural[j], sum+2*pad+2)
		}
	}
	total := 0.0
	for _, v := range natural {
		total += v
	}
	colW := make([]float64, cols)
	for j := range colW {
		if total <= w {
			colW[j] = natural[j]
		} else {
			colW[j] = max(natural[j]*w/total, 30)
		}
	}
	tw := 0.0
	for _, v := range colW {
		tw += v
	}
	for i, r := range tb.Rows {
		header := i == 0 && tb.Header
		base := pdf.TimesRoman
		if header {
			base = pdf.TimesBold
		}
		// Lay each cell out on a scratch page to learn its height.
		heights := make([]float64, cols)
		rowH := size * bodyLead
		for j, c := range r {
			heights[j] = t.measure(c, colW[j]-2*pad, size, base)
			rowH = max(rowH, heights[j]+2*pad)
		}
		t.need(rowH)
		if header {
			t.page.FillColorRect(x, t.y-rowH, tw, rowH, 0.93, 0.94, 0.96)
		}
		cx := x
		top := t.y
		for j := 0; j < cols; j++ {
			if j < len(r) {
				save := t.y
				cellX := cx + pad
				if j < len(tb.Align) && tb.Align[j] != 'l' {
					natW := natural[j] - 2*pad - 2
					if natW < colW[j]-2*pad {
						if tb.Align[j] == 'c' {
							cellX += (colW[j] - 2*pad - natW) / 2
						} else {
							cellX += colW[j] - 2*pad - natW
						}
					}
				}
				t.y = top - pad + 2
				t.paragraph(r[j], cellX, colW[j]-2*pad, size, base, false)
				t.y = save
			}
			t.page.Rect(cx, top-rowH, colW[j], rowH, 0.5)
			cx += colW[j]
		}
		t.y = top - rowH
	}
}

// measure returns the height a paragraph takes at a width.
func (t *typesetter) measure(in []Inline, w, size float64, base pdf.Font) float64 {
	scratch := &typesetter{doc: pdf.New(), o: t.o}
	scratch.newPage()
	start := scratch.y
	scratch.paragraph(in, 0, w, size, base, false)
	return start - scratch.y
}

func (t *typesetter) image(im *Image, x, w float64) {
	if t.o.Image == nil {
		return
	}
	data, ok := t.o.Image(im.Src)
	if !ok {
		return
	}
	img, err := t.doc.AddImage(data)
	if err != nil {
		return
	}
	iw := float64(img.Width) * 0.75 // 96 dpi pixels to points
	ih := float64(img.Height) * 0.75
	if iw > w {
		ih *= w / iw
		iw = w
	}
	if maxH := (pdf.A4Height - 2*pageMargin) * 0.6; ih > maxH {
		iw *= maxH / ih
		ih = maxH
	}
	t.need(ih + 8)
	t.page.Image(img, x+(w-iw)/2, t.y-ih-4, iw, ih)
	t.y -= ih + 8
	if im.Alt != "" {
		t.paragraph([]Inline{&Styled{Style: 'i', Text: []Inline{&Text{S: im.Alt}}}}, x, w, bodySize-1.5, pdf.TimesRoman, false)
		t.y -= paraGap
	}
}

// examplesSection typesets the examples: input and output side by side.
func (t *typesetter) examplesSection() {
	t.examplesDone = true
	x := pageMargin
	w := t.width()
	t.y -= 6
	t.need(60)
	t.paragraph([]Inline{&Text{S: t.o.Labels.Examples}}, x, w, 13, pdf.HelveticaBold, false)
	t.y -= 4
	half := (w - 10) / 2
	lh := codeSize * 1.25
	for i, e := range t.examples {
		in := wrapMono(strings.Split(strings.TrimRight(e.Input, "\n"), "\n"), half-12, codeSize)
		out := wrapMono(strings.Split(strings.TrimRight(e.Output, "\n"), "\n"), half-12, codeSize)
		const maxLines = 60
		if len(in) > maxLines {
			in = append(in[:maxLines], "…")
		}
		if len(out) > maxLines {
			out = append(out[:maxLines], "…")
		}
		t.need(min(float64(max(len(in), len(out))), 6)*lh + 40)
		t.paragraph([]Inline{&Text{S: fmt.Sprintf(t.o.Labels.Example, i+1)}}, x, w, 10.5, pdf.HelveticaBold, false)
		t.y -= 2
		// labels
		t.page.SetColor(0.35, 0.38, 0.45)
		t.page.Draw(x, t.y-9, 8.5, pdf.HelveticaBold, strings.ToUpper(t.o.Labels.Input))
		t.page.Draw(x+half+10, t.y-9, 8.5, pdf.HelveticaBold, strings.ToUpper(t.o.Labels.Output))
		t.page.ResetColor()
		t.y -= 13
		for len(in) > 0 || len(out) > 0 {
			t.need(lh + 8)
			fit := max(int((t.y-t.bottom()-8)/lh), 1)
			n := min(fit, max(len(in), len(out)))
			h := float64(n)*lh + 8
			t.page.FillColorRect(x, t.y-h, half, h, 0.95, 0.96, 0.97)
			t.page.FillColorRect(x+half+10, t.y-h, half, h, 0.95, 0.96, 0.97)
			for k := 0; k < n; k++ {
				ly := t.y - 4 - float64(k+1)*lh + codeSize*0.28
				if k < len(in) {
					t.page.Draw(x+6, ly, codeSize, pdf.Courier, in[k])
				}
				if k < len(out) {
					t.page.Draw(x+half+16, ly, codeSize, pdf.Courier, out[k])
				}
			}
			t.y -= h
			in = in[min(n, len(in)):]
			out = out[min(n, len(out)):]
			if len(in) > 0 || len(out) > 0 {
				t.newPage()
			}
		}
		t.y -= 6
		if len(e.Note) > 0 {
			t.paragraph([]Inline{&Styled{Style: 'b', Text: []Inline{&Text{S: t.o.Labels.Note}}}}, x, w, bodySize-0.5, pdf.TimesRoman, false)
			t.y -= 2
			t.blocks(e.Note, 0)
		}
		t.y -= 4
	}
}
