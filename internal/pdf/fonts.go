package pdf

import (
	"fmt"
	"strings"
)

// Font is one of the standard PDF fonts every reader has: nothing is
// embedded. The text fonts use the WinAnsi encoding (Western European
// languages); Symbol has Greek letters and mathematical signs.
type Font int

// The fonts. The first three keep the resource names F1-F3 of the original
// API (Text, Mono).
const (
	Helvetica Font = iota
	HelveticaBold
	Courier
	HelveticaOblique
	HelveticaBoldOblique
	TimesRoman
	TimesBold
	TimesItalic
	TimesBoldItalic
	CourierBold
	Symbol
	numFonts
)

var fontInfo = [numFonts]struct {
	base   string
	widths *[256]uint16
}{
	Helvetica:            {"Helvetica", &widthHelvetica},
	HelveticaBold:        {"Helvetica-Bold", &widthHelveticaBold},
	Courier:              {"Courier", &widthCourier},
	HelveticaOblique:     {"Helvetica-Oblique", &widthHelveticaOblique},
	HelveticaBoldOblique: {"Helvetica-BoldOblique", &widthHelveticaBoldOblique},
	TimesRoman:           {"Times-Roman", &widthTimesRoman},
	TimesBold:            {"Times-Bold", &widthTimesBold},
	TimesItalic:          {"Times-Italic", &widthTimesItalic},
	TimesBoldItalic:      {"Times-BoldItalic", &widthTimesBoldItalic},
	CourierBold:          {"Courier-Bold", &widthCourierBold},
	Symbol:               {"Symbol", &widthSymbol},
}

func (f Font) resource() string { return fmt.Sprintf("F%d", int(f)+1) }

// object is the font's dictionary.
func (f Font) object() string {
	if f == Symbol {
		return "<< /Type /Font /Subtype /Type1 /BaseFont /Symbol >>"
	}
	return fmt.Sprintf("<< /Type /Font /Subtype /Type1 /BaseFont /%s /Encoding /WinAnsiEncoding >>", fontInfo[f].base)
}

// Encode converts s to the font's single-byte encoding. Runes the font
// cannot show become '?' (text fonts) or are dropped (Symbol); CanShow
// tells beforehand.
func (f Font) Encode(s string) []byte {
	if f != Symbol {
		return encode(s)
	}
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if b, ok := symbolCode(r); ok {
			out = append(out, b)
		}
	}
	return out
}

// CanShow reports whether the font has a glyph for r.
func (f Font) CanShow(r rune) bool {
	if f == Symbol {
		_, ok := symbolCode(r)
		return ok
	}
	return r == '\t' || (r >= 0x20 && r < 0x7f) || (r >= 0xa0 && r <= 0xff) || winAnsi[r] != 0
}

// Width returns the width of s in points at the given size.
func (f Font) Width(s string, size float64) float64 {
	w := fontInfo[f].widths
	total := 0
	for _, b := range f.Encode(s) {
		v := int(w[b])
		if v == 0 {
			v = 500
		}
		total += v
	}
	return float64(total) * size / 1000
}

// Ascent and descent of the fonts, in em (close enough for all of them).
const (
	Ascent  = 0.72
	Descent = 0.22
)

// Draw draws s in font f with its baseline starting at (x, y).
func (p *Page) Draw(x, y, size float64, f Font, s string) {
	if s == "" {
		return
	}
	fmt.Fprintf(&p.buf, "BT /%s %s Tf %s %s Td (%s) Tj ET\n", f.resource(), num(size), num(x), num(y), escape(f.Encode(s)))
}

// SetColor sets the colour of what is drawn next (text, fills and
// strokes), as RGB from 0 to 1.
func (p *Page) SetColor(r, g, b float64) {
	fmt.Fprintf(&p.buf, "%s %s %s rg %s %s %s RG\n", num(r), num(g), num(b), num(r), num(g), num(b))
}

// ResetColor goes back to black.
func (p *Page) ResetColor() { p.buf.WriteString("0 g 0 G\n") }

// FillColorRect fills a rectangle with an RGB colour.
func (p *Page) FillColorRect(x, y, w, h, r, g, b float64) {
	fmt.Fprintf(&p.buf, "q %s %s %s rg %s %s %s %s re f Q\n", num(r), num(g), num(b), num(x), num(y), num(w), num(h))
}

// Path strokes a polyline (points as x1, y1, x2, y2, ...).
func (p *Page) Path(width float64, pts ...float64) {
	if len(pts) < 4 {
		return
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s w 1 J 1 j %s %s m", num(width), num(pts[0]), num(pts[1]))
	for i := 2; i+1 < len(pts); i += 2 {
		fmt.Fprintf(&sb, " %s %s l", num(pts[i]), num(pts[i+1]))
	}
	sb.WriteString(" S 0 J 0 j\n")
	p.buf.WriteString(sb.String())
}

// symbolCode maps a rune to its code in the Symbol font's own encoding.
func symbolCode(r rune) (byte, bool) {
	if r >= 0x20 && r < 0x7f {
		switch r {
		// ASCII letters are Greek in Symbol; these are safe as they are.
		case ' ', '!', '#', '%', '&', '(', ')', '+', ',', '.', '/', ':', ';', '<', '=', '>', '?', '[', ']', '_', '{', '|', '}':
			return byte(r), true
		}
		if r >= '0' && r <= '9' {
			return byte(r), true
		}
		return 0, false
	}
	b, ok := symbolMap[r]
	return b, ok
}

// symbolMap is the Unicode value of every glyph of the Symbol font that is
// not plain ASCII (Adobe's symbol.txt).
var symbolMap = map[rune]byte{
	'∀': 34, '∃': 36, '∋': 39, '∗': 42, '−': 45, '≅': 64,
	'Α': 65, 'Β': 66, 'Χ': 67, 'Δ': 68, '∆': 68, 'Ε': 69, 'Φ': 70, 'Γ': 71, 'Η': 72, 'Ι': 73, 'ϑ': 74,
	'Κ': 75, 'Λ': 76, 'Μ': 77, 'Ν': 78, 'Ο': 79, 'Π': 80, 'Θ': 81, 'Ρ': 82, 'Σ': 83, 'Τ': 84, 'Υ': 85,
	'ς': 86, 'Ω': 87, '\u2126': 87, 'Ξ': 88, 'Ψ': 89, 'Ζ': 90, '∴': 92, '⊥': 94,
	'α': 97, 'β': 98, 'χ': 99, 'δ': 100, 'ε': 101, 'φ': 102, 'γ': 103, 'η': 104, 'ι': 105, 'ϕ': 106,
	'κ': 107, 'λ': 108, 'μ': 109, 'µ': 109, 'ν': 110, 'ο': 111, 'π': 112, 'θ': 113, 'ρ': 114, 'σ': 115,
	'τ': 116, 'υ': 117, 'ϖ': 118, 'ω': 119, 'ξ': 120, 'ψ': 121, 'ζ': 122, '∼': 126,
	'€': 160, 'ϒ': 161, '′': 162, '≤': 163, '⁄': 164, '∞': 165, 'ƒ': 166, '♣': 167, '♦': 168, '♥': 169,
	'♠': 170, '↔': 171, '←': 172, '↑': 173, '→': 174, '↓': 175, '°': 176, '±': 177, '″': 178, '≥': 179,
	'×': 180, '∝': 181, '∂': 182, '•': 183, '÷': 184, '≠': 185, '≡': 186, '≈': 187, '…': 188,
	'↵': 191, 'ℵ': 192, 'ℑ': 193, 'ℜ': 194, '℘': 195, '⊗': 196, '⊕': 197, '∅': 198, '∩': 199, '∪': 200,
	'⊃': 201, '⊇': 202, '⊄': 203, '⊂': 204, '⊆': 205, '∈': 206, '∉': 207, '∠': 208, '∇': 209,
	'∏': 213, '√': 214, '⋅': 215, '¬': 216, '∧': 217, '∨': 218, '⇔': 219, '⇐': 220, '⇑': 221,
	'⇒': 222, '⇓': 223, '◊': 224, '⟨': 225, '〈': 225, '∑': 229, '⟩': 241, '〉': 241, '∫': 242,
	'⎛': 230, '⎜': 231, '⎝': 232, '⎡': 233, '⎢': 234, '⎣': 235, '⎧': 236, '⎨': 237, '⎩': 238, '⎪': 239,
	'⎞': 246, '⎟': 247, '⎠': 248, '⎤': 249, '⎥': 250, '⎦': 251, '⎫': 252, '⎬': 253, '⎭': 254,
}

// Curve strokes a path made of cubic Bézier segments starting at (x, y):
// each segment is c1x, c1y, c2x, c2y, x, y.
func (p *Page) Curve(width, x, y float64, segs ...[6]float64) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s w 1 J 1 j %s %s m", num(width), num(x), num(y))
	for _, s := range segs {
		fmt.Fprintf(&sb, " %s %s %s %s %s %s c", num(s[0]), num(s[1]), num(s[2]), num(s[3]), num(s[4]), num(s[5]))
	}
	sb.WriteString(" S 0 J 0 j\n")
	p.buf.WriteString(sb.String())
}

// Link makes the rectangle (x, y, w, h) a link to url.
func (p *Page) Link(x, y, w, h float64, url string) {
	p.links = append(p.links, link{x, y, w, h, url})
}

type link struct {
	x, y, w, h float64
	url        string
}
