// Package pdf is a minimal PDF 1.4 writer for printable documents
// (credential sheets, result lists, certificates): pages of text in the
// standard Helvetica fonts (WinAnsi encoding, which covers Spanish and the
// other Western European languages), lines, rectangles and images (JPEG
// as is, others as compressed RGB). No external dependencies and no
// embedded fonts, so documents are tiny.
package pdf

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// A4 page size in points.
const (
	A4Width  = 595.28
	A4Height = 841.89
)

// Doc is a document being built.
type Doc struct {
	pages  []*Page
	images []*Image
	Title  string
}

// Page is one page; coordinates are in points from the bottom-left corner.
type Page struct {
	w, h float64
	buf  bytes.Buffer
}

// New returns an empty document.
func New() *Doc { return &Doc{} }

// AddPage appends an A4 portrait page.
func (d *Doc) AddPage() *Page {
	p := &Page{w: A4Width, h: A4Height}
	d.pages = append(d.pages, p)
	return p
}

// AddLandscapePage appends an A4 landscape page (wide tables).
func (d *Doc) AddLandscapePage() *Page {
	p := &Page{w: A4Height, h: A4Width}
	d.pages = append(d.pages, p)
	return p
}

// Size returns the page's width and height in points.
func (p *Page) Size() (w, h float64) { return p.w, p.h }

// Pages is the number of pages.
func (d *Doc) Pages() int { return len(d.pages) }

func num(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

// Text draws s with its baseline starting at (x, y).
func (p *Page) Text(x, y, size float64, bold bool, s string) {
	font := "F1"
	if bold {
		font = "F2"
	}
	fmt.Fprintf(&p.buf, "BT /%s %s Tf %s %s Td (%s) Tj ET\n", font, num(size), num(x), num(y), escape(encode(s)))
}

// Mono draws s in Courier (every character MonoWidth·size wide), for
// source code.
func (p *Page) Mono(x, y, size float64, s string) {
	fmt.Fprintf(&p.buf, "BT /F3 %s Tf %s %s Td (%s) Tj ET\n", num(size), num(x), num(y), escape(encode(s)))
}

// MonoWidth is the advance of a Courier character in em.
const MonoWidth = 0.6

// TextCentered draws s centred on x.
func (p *Page) TextCentered(x, y, size float64, bold bool, s string) {
	p.Text(x-Width(s, size, bold)/2, y, size, bold, s)
}

// TextRight draws s ending at x.
func (p *Page) TextRight(x, y, size float64, bold bool, s string) {
	p.Text(x-Width(s, size, bold), y, size, bold, s)
}

// Fit shortens s with an ellipsis so that it is at most w points wide.
func Fit(s string, w, size float64, bold bool) string {
	if Width(s, size, bold) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && Width(string(r)+"…", size, bold) > w {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

// Line draws a line.
func (p *Page) Line(x1, y1, x2, y2, width float64) {
	fmt.Fprintf(&p.buf, "%s w %s %s m %s %s l S\n", num(width), num(x1), num(y1), num(x2), num(y2))
}

// DashedLine draws a dashed line (cut marks).
func (p *Page) DashedLine(x1, y1, x2, y2, width float64) {
	fmt.Fprintf(&p.buf, "[4 3] 0 d %s w %s %s m %s %s l S [] 0 d\n", num(width), num(x1), num(y1), num(x2), num(y2))
}

// Rect strokes a rectangle.
func (p *Page) Rect(x, y, w, h, width float64) {
	fmt.Fprintf(&p.buf, "%s w %s %s %s %s re S\n", num(width), num(x), num(y), num(w), num(h))
}

// FillRect fills a rectangle with a grey level (0 black … 1 white).
func (p *Page) FillRect(x, y, w, h, grey float64) {
	fmt.Fprintf(&p.buf, "%s g %s %s %s %s re f 0 g\n", num(grey), num(x), num(y), num(w), num(h))
}

// Write serialises the document.
func (d *Doc) Write(w io.Writer) error {
	if len(d.pages) == 0 {
		d.AddPage()
	}
	var out bytes.Buffer
	var offsets []int
	obj := func(body string) int {
		offsets = append(offsets, out.Len())
		n := len(offsets)
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", n, body)
		return n
	}
	out.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	// 1: catalog, 2: pages (bodies written in order; numbering fixed below).
	n := len(d.pages)
	pagesID := 2
	fontReg, fontBold, fontMono := 3, 4, 5
	first := 6 // page i uses objects first+2i (page) and first+2i+1 (content)
	// Images follow the pages; every page may use any of them.
	xobjects := ""
	if len(d.images) > 0 {
		var refs []string
		for i := range d.images {
			refs = append(refs, fmt.Sprintf("/Im%d %d 0 R", i+1, first+2*n+i))
		}
		xobjects = " /XObject << " + strings.Join(refs, " ") + " >>"
	}
	kids := make([]string, n)
	for i := range d.pages {
		kids[i] = fmt.Sprintf("%d 0 R", first+2*i)
	}
	obj(fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R >>", pagesID))
	obj(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), n))
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>")
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Courier /Encoding /WinAnsiEncoding >>")
	for i, p := range d.pages {
		obj(fmt.Sprintf("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 %s %s] /Resources << /Font << /F1 %d 0 R /F2 %d 0 R /F3 %d 0 R >>%s >> /Contents %d 0 R >>",
			pagesID, num(p.w), num(p.h), fontReg, fontBold, fontMono, xobjects, first+2*i+1))
		content := p.buf.Bytes()
		obj(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content)+0, content))
	}
	for _, im := range d.images {
		obj(im.object())
	}
	info := 0
	if d.Title != "" {
		info = obj(fmt.Sprintf("<< /Title (%s) /Producer (CMS) >>", escape(encode(d.Title))))
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, o := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", o)
	}
	trailer := fmt.Sprintf("<< /Size %d /Root 1 0 R", len(offsets)+1)
	if info != 0 {
		trailer += fmt.Sprintf(" /Info %d 0 R", info)
	}
	fmt.Fprintf(&out, "trailer\n%s >>\nstartxref\n%d\n%%%%EOF\n", trailer, xref)
	_, err := w.Write(out.Bytes())
	return err
}

// Bytes returns the serialised document.
func (d *Doc) Bytes() []byte {
	var b bytes.Buffer
	d.Write(&b)
	return b.Bytes()
}

// winAnsi maps the runes of Windows-1252 that differ from Latin-1.
var winAnsi = map[rune]byte{
	'€': 0x80, '‚': 0x82, 'ƒ': 0x83, '„': 0x84, '…': 0x85, '†': 0x86, '‡': 0x87, 'ˆ': 0x88, '‰': 0x89,
	'Š': 0x8a, '‹': 0x8b, 'Œ': 0x8c, 'Ž': 0x8e, '‘': 0x91, '’': 0x92, '“': 0x93, '”': 0x94, '•': 0x95,
	'–': 0x96, '—': 0x97, '˜': 0x98, '™': 0x99, 'š': 0x9a, '›': 0x9b, 'œ': 0x9c, 'ž': 0x9e, 'Ÿ': 0x9f,
}

// encode converts s to WinAnsi bytes; unsupported runes become '?'.
func encode(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		switch {
		case r < 0x80 && r >= 0x20:
			out = append(out, byte(r))
		case r >= 0xa0 && r <= 0xff:
			out = append(out, byte(r))
		default:
			if b, ok := winAnsi[r]; ok {
				out = append(out, b)
			} else if r == '\t' {
				out = append(out, ' ')
			} else {
				out = append(out, '?')
			}
		}
	}
	return out
}

func escape(b []byte) string {
	var sb strings.Builder
	for _, c := range b {
		switch c {
		case '(', ')', '\\':
			sb.WriteByte('\\')
			sb.WriteByte(c)
		default:
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

// Widths of the printable ASCII characters (32–126) in 1/1000 em, from the
// standard font metrics.
var helvetica = [95]int{
	278, 278, 355, 556, 556, 889, 667, 191, 333, 333, 389, 584, 278, 333, 278, 278,
	556, 556, 556, 556, 556, 556, 556, 556, 556, 556, 278, 278, 584, 584, 584, 556,
	1015, 667, 667, 722, 722, 667, 611, 778, 722, 278, 500, 667, 556, 833, 722, 778,
	667, 778, 722, 667, 611, 722, 667, 944, 667, 667, 611, 278, 278, 278, 469, 556,
	333, 556, 556, 500, 556, 556, 278, 556, 556, 222, 222, 500, 222, 833, 556, 556,
	556, 556, 333, 500, 278, 556, 500, 722, 500, 500, 500, 334, 260, 334, 584,
}

var helveticaBold = [95]int{
	278, 333, 474, 556, 556, 889, 722, 238, 333, 333, 389, 584, 278, 333, 278, 278,
	556, 556, 556, 556, 556, 556, 556, 556, 556, 556, 333, 333, 584, 584, 584, 611,
	975, 722, 722, 722, 722, 667, 611, 778, 722, 278, 556, 722, 611, 833, 722, 778,
	667, 778, 722, 667, 611, 722, 667, 944, 667, 667, 611, 333, 278, 333, 584, 556,
	333, 556, 611, 556, 611, 556, 333, 611, 611, 278, 278, 556, 278, 889, 611, 611,
	611, 611, 389, 556, 333, 611, 556, 778, 556, 556, 500, 389, 280, 389, 584,
}

// latinBase maps accented Latin-1 letters to a letter of similar width.
var latinBase = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ä", "a", "ã", "a", "å", "a", "é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i", "ó", "o", "ò", "o", "ô", "o", "ö", "o", "õ", "o", "ú", "u",
	"ù", "u", "û", "u", "ü", "u", "ñ", "n", "ç", "c", "ý", "y", "Á", "A", "À", "A", "Â", "A", "Ä", "A",
	"Ã", "A", "Å", "A", "É", "E", "È", "E", "Ê", "E", "Ë", "E", "Í", "I", "Ì", "I", "Î", "I", "Ï", "I",
	"Ó", "O", "Ò", "O", "Ô", "O", "Ö", "O", "Õ", "O", "Ú", "U", "Ù", "U", "Û", "U", "Ü", "U", "Ñ", "N",
	"Ç", "C", "Ý", "Y", "¿", "?", "¡", "!",
)

// Width returns the width of s in points.
func Width(s string, size float64, bold bool) float64 {
	table := &helvetica
	if bold {
		table = &helveticaBold
	}
	total := 0
	for _, r := range latinBase.Replace(s) {
		if r >= 32 && r <= 126 {
			total += table[r-32]
		} else {
			total += 556
		}
	}
	return float64(total) * size / 1000
}
