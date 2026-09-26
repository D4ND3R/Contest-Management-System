package statement

import (
	"strings"
	"unicode"

	"github.com/D4ND3R/Contest-Management-System/internal/pdf"
)

// mbox is a laid-out piece of a formula: width, height above the baseline
// and depth below it; draw paints it with its baseline at (x, y).
type mbox struct {
	w, h, d float64
	cls     OpClass
	draw    func(pg *pdf.Page, x, y float64)
}

func emptyBox() *mbox { return &mbox{draw: func(*pdf.Page, float64, float64) {}} }

// mstyle is the TeX style: display, text, script or scriptscript.
type mstyle struct {
	size    float64 // font size of this style
	display bool
	level   int // 0 text/display, 1 script, 2 scriptscript
}

func (s mstyle) script() mstyle {
	return mstyle{size: s.size * scriptRatio(s.level), display: false, level: min(s.level+1, 2)}
}

// fracStyle is the style of a fraction's numerator and denominator.
func (s mstyle) frac() mstyle {
	if s.display {
		return mstyle{size: s.size, display: false, level: s.level}
	}
	return s.script()
}

func scriptRatio(level int) float64 {
	if level == 0 {
		return 0.72
	}
	return 0.8
}

// axis is the height of the math axis (fraction bars, operators).
func (s mstyle) axis() float64 { return 0.25 * s.size }

// layoutMath lays a formula out at the given text size.
func layoutMath(m *Math, size float64) *mbox {
	st := mstyle{size: size, display: m.Display}
	return mlayout(m.Root, st)
}

func mlayout(n *MNode, st mstyle) *mbox {
	if n == nil {
		return emptyBox()
	}
	switch n.Kind {
	case MRow:
		return mrow(n.Kids, st)
	case MIdent:
		b := glyphs(n.Text, st.size, identFont(n))
		return b
	case MNum:
		f := pdf.TimesRoman
		if n.Variant == "bold" {
			f = pdf.TimesBold
		}
		return glyphs(n.Text, st.size, f)
	case MOp:
		return mop(n, st)
	case MText:
		f := pdf.TimesRoman
		switch n.Variant {
		case "bold":
			f = pdf.TimesBold
		case "italic":
			f = pdf.TimesItalic
		case "tt":
			f = pdf.Courier
		}
		return glyphs(n.Text, st.size, f)
	case MFrac:
		return mfrac(n, st)
	case MSqrt:
		return msqrt(n, st)
	case MScripts:
		return mscripts(n, st)
	case MFenced:
		body := mlayout(kid(n, 0), st)
		return fenced(n.Open, n.Close, body, st)
	case MSpace:
		w := n.Em * st.size
		return &mbox{w: w, draw: func(*pdf.Page, float64, float64) {}}
	case MTable:
		return mtable(n, st)
	case MAccent:
		return maccent(n, st)
	case MError:
		b := glyphs(n.Text, st.size*0.9, pdf.Courier)
		inner := b.draw
		b.draw = func(pg *pdf.Page, x, y float64) {
			pg.SetColor(0.75, 0.1, 0.1)
			inner(pg, x, y)
			pg.ResetColor()
		}
		return b
	}
	return emptyBox()
}

// identFont picks the font of an identifier: italic single letters,
// upright names and Greek capitals.
func identFont(n *MNode) pdf.Font {
	switch n.Variant {
	case "bold", "bb", "frak":
		return pdf.TimesBold
	case "tt":
		return pdf.Courier
	case "normal", "sf":
		return pdf.TimesRoman
	}
	r := []rune(n.Text)
	if len(r) == 1 && (unicode.IsLower(r[0]) || r[0] < 128) {
		return pdf.TimesItalic
	}
	if len(r) == 1 {
		return pdf.TimesItalic
	}
	return pdf.TimesRoman
}

// glyphs is a box of text in a font; characters the font lacks come from
// Symbol, then from fallbacks.
func glyphs(s string, size float64, f pdf.Font) *mbox {
	type seg struct {
		f pdf.Font
		s string
		w float64
	}
	var segs []seg
	add := func(f pdf.Font, s string) {
		if n := len(segs); n > 0 && segs[n-1].f == f {
			segs[n-1].s += s
			return
		}
		segs = append(segs, seg{f: f, s: s})
	}
	for _, r := range s {
		if fb, ok := mathFallback[r]; ok {
			for _, r2 := range fb {
				addRune(add, r2, f)
			}
			continue
		}
		addRune(add, r, f)
	}
	b := &mbox{}
	for i := range segs {
		segs[i].w = segs[i].f.Width(segs[i].s, size)
		b.w += segs[i].w
	}
	b.h, b.d = textMetrics(s, size)
	b.draw = func(pg *pdf.Page, x, y float64) {
		for _, sg := range segs {
			pg.Draw(x, y, size, sg.f, sg.s)
			x += sg.w
		}
	}
	return b
}

func addRune(add func(pdf.Font, string), r rune, f pdf.Font) {
	// Greek and mathematical signs look best in Symbol.
	if r > 127 && pdf.Symbol.CanShow(r) && !(f.CanShow(r) && isLatinLetter(r)) {
		add(pdf.Symbol, string(r))
		return
	}
	if f.CanShow(r) {
		add(f, string(r))
		return
	}
	if pdf.Symbol.CanShow(r) {
		add(pdf.Symbol, string(r))
		return
	}
	add(f, "?")
}

func isLatinLetter(r rune) bool { return unicode.IsLetter(r) && unicode.In(r, unicode.Latin) }

// mathFallback spells the signs Symbol lacks with ones it has.
var mathFallback = map[rune]string{
	'⋯': "⋅⋅⋅", '⋮': ":", '⋱': "⋅", '≪': "<<", '≫': ">>", '↦': "|→", '⟶': "→", '≃': "≅", '⪯': "≤", '⪰': "≥",
	'≺': "<", '≻': ">", 'ℓ': "l", 'ℏ': "h", '∘': "°", '∙': "•", '⋆': "∗", '∖': "\\", '∓': "±", '∣': "|",
	'∥': "||", '‖': "||", '⊤': "T", '∄': "∃", '∐': "∏", '∬': "∫∫", '∮': "∫", '⋃': "∪", '⋂': "∩", '⨁': "⊕",
	'⨂': "⊗", '⋁': "∨", '⋀': "∧", '⨆': "∪", '≔': ":=", '⊢': "|-", '⊨': "|=", '≰': "≤", '≱': "≥", '⊘': "/",
	'⊔': "∪", '⊓': "∩", '⊙': "⊗", '◁': "<", '✓': "v", '△': "∆", 'ϵ': "ε", 'ϱ': "ρ", '̸': "",
	'−': "−", '⏞': "", '⏟': "",
}

// textMetrics estimates height and depth of a string.
func textMetrics(s string, size float64) (h, d float64) {
	h = 0.46
	for _, r := range s {
		switch {
		case strings.ContainsRune("acemnorsuvwxz", r):
		case strings.ContainsRune("gpqy", r):
			d = max(d, 0.22)
		case strings.ContainsRune("j", r):
			d, h = max(d, 0.22), max(h, 0.68)
		case strings.ContainsRune("()[]{}|⌊⌋⌈⌉⟨⟩", r):
			h, d = max(h, 0.74), max(d, 0.24)
		case strings.ContainsRune(",;", r):
			d = max(d, 0.14)
		case strings.ContainsRune("βγζημξρφχψϕς∫∏∑", r):
			h, d = max(h, 0.7), max(d, 0.22)
		case strings.ContainsRune("=+−-×<>≤≥≠≈≡∼~⋅·:∗", r):
			h = max(h, 0.55)
		default:
			h = max(h, 0.69)
		}
	}
	return h * size, d * size
}

func mop(n *MNode, st mstyle) *mbox {
	var b *mbox
	switch {
	case n.Variant == "word":
		b = glyphs(n.Text, st.size, pdf.TimesRoman)
	case n.Class == ClassLarge:
		size := st.size
		if st.display {
			size *= 1.45
		} else if st.level == 0 {
			size *= 1.1
		}
		b = glyphs(n.Text, size, pdf.TimesRoman)
		// Center the operator on the axis.
		shift := st.axis() - (b.h-b.d)/2
		inner := b.draw
		b.h, b.d = b.h+shift, b.d-shift
		b.draw = func(pg *pdf.Page, x, y float64) { inner(pg, x, y+shift) }
	case n.Size > 0:
		total := []float64{0, 1.2, 1.8, 2.4, 3}[n.Size] * st.size
		b = delimiter(n.Text, total, st)
	case n.Text == "⌊" || n.Text == "⌋" || n.Text == "⌈" || n.Text == "⌉":
		// No standard font has them: drawn at the height of a parenthesis.
		b = delimiter(n.Text, 0.98*st.size, st)
	case strings.HasSuffix(n.Text, "̸"):
		base := glyphs(strings.TrimSuffix(n.Text, "̸"), st.size, pdf.TimesRoman)
		slash := glyphs("/", st.size, pdf.TimesRoman)
		b = &mbox{w: base.w, h: max(base.h, slash.h), d: max(base.d, slash.d)}
		b.draw = func(pg *pdf.Page, x, y float64) {
			base.draw(pg, x, y)
			slash.draw(pg, x+(base.w-slash.w)/2, y)
		}
	default:
		b = glyphs(n.Text, st.size, pdf.TimesRoman)
	}
	b.cls = n.Class
	return b
}

// spacing between two classes, in eighteenths of an em (TeX's table; the
// medium and thick spaces vanish in scripts).
func spacing(a, b OpClass, st mstyle) float64 {
	thin, med, thick := 3.0, 4.0, 5.0
	if st.level > 0 {
		med, thick = 0, 0
	}
	switch a {
	case ClassOrd, ClassClose:
		switch b {
		case ClassLarge:
			return thin
		case ClassBin:
			return med
		case ClassRel:
			return thick
		}
	case ClassLarge:
		switch b {
		case ClassOrd, ClassLarge:
			return thin
		case ClassRel:
			return thick
		}
	case ClassBin:
		switch b {
		case ClassOrd, ClassLarge, ClassOpen:
			return med
		}
	case ClassRel:
		switch b {
		case ClassOrd, ClassLarge, ClassOpen:
			return thick
		}
	case ClassPunct:
		if b != ClassClose {
			return thin
		}
	}
	return 0
}

func mrow(kids []*MNode, st mstyle) *mbox {
	var boxes []*mbox
	prev := ClassOpen // start of a row: a leading minus is unary
	for _, k := range kids {
		if k == nil {
			continue
		}
		b := mlayout(k, st)
		if k.Kind != MOp {
			b.cls = ClassOrd
			if k.Kind == MScripts && k.Kids[0] != nil && k.Kids[0].Kind == MOp {
				b.cls = k.Kids[0].Class
			}
		}
		if k.Kind == MSpace {
			b.cls = -1
		}
		if b.cls == ClassBin && (prev == ClassBin || prev == ClassOpen || prev == ClassRel || prev == ClassPunct || prev == ClassLarge) {
			b.cls = ClassOrd
		}
		if b.cls != -1 {
			prev = b.cls
		}
		boxes = append(boxes, b)
	}
	// a trailing binary operator is ordinary too
	if n := len(boxes); n > 0 && boxes[n-1].cls == ClassBin {
		boxes[n-1].cls = ClassOrd
	}
	out := &mbox{cls: ClassOrd}
	xs := make([]float64, len(boxes))
	last := OpClass(-1)
	for i, b := range boxes {
		if b.cls != -1 && last != -1 {
			out.w += spacing(last, b.cls, st) / 18 * st.size
		}
		xs[i] = out.w
		out.w += b.w
		out.h = max(out.h, b.h)
		out.d = max(out.d, b.d)
		if b.cls != -1 {
			last = b.cls
		}
	}
	if len(boxes) == 1 {
		out.cls = boxes[0].cls
	}
	out.draw = func(pg *pdf.Page, x, y float64) {
		for i, b := range boxes {
			b.draw(pg, x+xs[i], y)
		}
	}
	return out
}

func mfrac(n *MNode, st mstyle) *mbox {
	fs := st.frac()
	num := mlayout(kid(n, 0), fs)
	den := mlayout(kid(n, 1), fs)
	rule := 0.045 * st.size
	if n.NoRule {
		rule = 0
	}
	gap := 0.12 * st.size
	if st.display {
		gap = 0.18 * st.size
	}
	pad := 0.1 * st.size
	w := max(num.w, den.w) + 2*pad
	axis := st.axis()
	numShift := axis + rule/2 + gap + num.d
	denShift := axis - rule/2 - gap - den.h
	b := &mbox{w: w, h: numShift + num.h, d: -denShift + den.d}
	b.draw = func(pg *pdf.Page, x, y float64) {
		num.draw(pg, x+(w-num.w)/2, y+numShift)
		den.draw(pg, x+(w-den.w)/2, y+denShift)
		if rule > 0 {
			pg.Line(x+pad*0.5, y+axis, x+w-pad*0.5, y+axis, rule)
		}
	}
	return b
}

func msqrt(n *MNode, st mstyle) *mbox {
	body := mlayout(kid(n, 0), st)
	var idx *mbox
	if len(n.Kids) > 1 && n.Kids[1] != nil {
		idx = mlayout(n.Kids[1], st.script().script())
	}
	line := 0.045 * st.size
	gap := 0.12 * st.size
	sign := 0.55 * st.size
	top := body.h + gap
	bottom := body.d + 0.06*st.size
	lead := 0.0
	if idx != nil {
		lead = max(0, idx.w-0.3*st.size)
	}
	b := &mbox{w: lead + sign + body.w + 0.1*st.size, h: top + line, d: bottom}
	if idx != nil {
		b.h = max(b.h, top*0.55+idx.h+idx.d)
	}
	b.draw = func(pg *pdf.Page, x, y float64) {
		x0 := x + lead
		mid := y - bottom + (top+bottom)*0.45
		pg.Path(line, x0+0.04*st.size, mid, x0+0.16*st.size, mid+0.05*st.size, x0+0.3*st.size, y-bottom,
			x0+sign-0.04*st.size, y+top, x0+sign+body.w+0.08*st.size, y+top)
		body.draw(pg, x0+sign, y)
		if idx != nil {
			idx.draw(pg, x, y+top*0.55+idx.d)
		}
	}
	return b
}

func mscripts(n *MNode, st mstyle) *mbox {
	base := mlayout(n.Kids[0], st)
	ss := st.script()
	var sub, sup *mbox
	if n.Kids[1] != nil {
		sub = mlayout(n.Kids[1], ss)
	}
	if n.Kids[2] != nil {
		sup = mlayout(n.Kids[2], ss)
	}
	isOp := n.Kids[0] != nil && n.Kids[0].Kind == MOp && n.Kids[0].Class == ClassLarge
	if n.Limits && (st.display || !isOp) {
		// limits: centred below and above
		gap := 0.12 * st.size
		w := base.w
		if sub != nil {
			w = max(w, sub.w)
		}
		if sup != nil {
			w = max(w, sup.w)
		}
		b := &mbox{w: w, h: base.h, d: base.d, cls: base.cls}
		if sup != nil {
			b.h += gap + sup.d + sup.h
		}
		if sub != nil {
			b.d += gap + sub.h + sub.d
		}
		b.draw = func(pg *pdf.Page, x, y float64) {
			base.draw(pg, x+(w-base.w)/2, y)
			if sup != nil {
				sup.draw(pg, x+(w-sup.w)/2, y+base.h+gap+sup.d)
			}
			if sub != nil {
				sub.draw(pg, x+(w-sub.w)/2, y-base.d-gap-sub.h)
			}
		}
		return b
	}
	supShift := max(0.42*st.size, base.h-0.28*st.size)
	subShift := max(0.2*st.size, base.d+0.08*st.size)
	if sup != nil && sub != nil {
		subShift = max(subShift, 0.28*st.size)
		// keep a gap between them
		if gap := (supShift - sup.d) - (sub.h - subShift); gap < 0.1*st.size {
			supShift += 0.1*st.size - gap
		}
	}
	kern := 0.03 * st.size
	w := base.w + kern
	extra := 0.0
	if sup != nil {
		extra = sup.w
	}
	if sub != nil {
		extra = max(extra, sub.w)
	}
	b := &mbox{w: w + extra + 0.02*st.size, h: base.h, d: base.d, cls: base.cls}
	if sup != nil {
		b.h = max(b.h, supShift+sup.h)
	}
	if sub != nil {
		b.d = max(b.d, subShift+sub.d)
	}
	b.draw = func(pg *pdf.Page, x, y float64) {
		base.draw(pg, x, y)
		if sup != nil {
			sup.draw(pg, x+w, y+supShift)
		}
		if sub != nil {
			sub.draw(pg, x+w, y-subShift)
		}
	}
	return b
}

// fenced surrounds body with delimiters as tall as it.
func fenced(open, close string, body *mbox, st mstyle) *mbox {
	axis := st.axis()
	extent := max(body.h-axis, body.d+axis)
	total := max(2*extent*1.05, 0)
	var l, r *mbox
	if open != "" {
		l = delimiter(open, total, st)
	}
	if close != "" {
		r = delimiter(close, total, st)
	}
	b := &mbox{w: body.w, h: body.h, d: body.d}
	lw := 0.0
	if l != nil {
		lw = l.w
		b.w += l.w
		b.h, b.d = max(b.h, l.h), max(b.d, l.d)
	}
	if r != nil {
		b.w += r.w
		b.h, b.d = max(b.h, r.h), max(b.d, r.d)
	}
	b.draw = func(pg *pdf.Page, x, y float64) {
		if l != nil {
			l.draw(pg, x, y)
		}
		body.draw(pg, x+lw, y)
		if r != nil {
			r.draw(pg, x+lw+body.w, y)
		}
	}
	return b
}

// delimiter is a delimiter at least total high, centred on the axis:
// the font's glyph when that is tall enough, else drawn.
func delimiter(d string, total float64, st mstyle) *mbox {
	size := st.size
	glyphOK := total <= 1.15*size
	switch d {
	case "⌊", "⌋", "⌈", "⌉", "‖":
		glyphOK = false
	}
	if glyphOK {
		g := glyphs(d, size, pdf.TimesRoman)
		g.cls = ClassOpen
		return g
	}
	total = max(total, 0.95*size)
	axis := st.axis()
	top := axis + total/2
	bottom := axis - total/2
	lw := 0.05 * size
	w := 0.38 * size
	switch d {
	case "|":
		w = 0.25 * size
	case "‖":
		w = 0.38 * size
	case "{", "}":
		w = 0.45 * size
	case "⟨", "⟩":
		w = 0.4 * size
	}
	b := &mbox{w: w, h: top, d: -bottom, cls: ClassOpen}
	b.draw = func(pg *pdf.Page, x, y float64) {
		t, bt := y+top, y+bottom
		in, out := x+0.1*size, x+w-0.1*size
		mid := (t + bt) / 2
		switch d {
		case "(":
			pg.Curve(lw, out, t, [6]float64{in - 0.05*size, t - total*0.2, in - 0.05*size, bt + total*0.2, out, bt})
		case ")":
			pg.Curve(lw, in, t, [6]float64{out + 0.05*size, t - total*0.2, out + 0.05*size, bt + total*0.2, in, bt})
		case "[":
			pg.Path(lw, out, t, in, t, in, bt, out, bt)
		case "]":
			pg.Path(lw, in, t, out, t, out, bt, in, bt)
		case "⌊":
			pg.Path(lw, in, t, in, bt, out, bt)
		case "⌋":
			pg.Path(lw, out, t, out, bt, in, bt)
		case "⌈":
			pg.Path(lw, out, t, in, t, in, bt)
		case "⌉":
			pg.Path(lw, in, t, out, t, out, bt)
		case "|":
			pg.Path(lw, x+w/2, t, x+w/2, bt)
		case "‖":
			pg.Path(lw, x+w/2-0.07*size, t, x+w/2-0.07*size, bt)
			pg.Path(lw, x+w/2+0.07*size, t, x+w/2+0.07*size, bt)
		case "⟨":
			pg.Path(lw, out, t, in, mid, out, bt)
		case "⟩":
			pg.Path(lw, in, t, out, mid, in, bt)
		case "{":
			c := (in + out) / 2
			pg.Curve(lw, out, t,
				[6]float64{c, t, c, t, c, t - total*0.15},
				[6]float64{c, mid + total*0.05, c, mid, in, mid},
				[6]float64{c, mid, c, mid - total*0.05, c, bt + total*0.15},
				[6]float64{c, bt, c, bt, out, bt})
		case "}":
			c := (in + out) / 2
			pg.Curve(lw, in, t,
				[6]float64{c, t, c, t, c, t - total*0.15},
				[6]float64{c, mid + total*0.05, c, mid, out, mid},
				[6]float64{c, mid, c, mid - total*0.05, c, bt + total*0.15},
				[6]float64{c, bt, c, bt, in, bt})
		default:
			g := glyphs(d, total*0.9, pdf.TimesRoman)
			g.draw(pg, x, y+bottom+g.d)
		}
	}
	return b
}

func mtable(n *MNode, st mstyle) *mbox {
	cs := mstyle{size: st.size, display: false, level: st.level}
	cols := 0
	for _, r := range n.Rows {
		cols = max(cols, len(r))
	}
	cells := make([][]*mbox, len(n.Rows))
	colW := make([]float64, cols)
	rowH := make([]float64, len(n.Rows))
	rowD := make([]float64, len(n.Rows))
	for i, r := range n.Rows {
		for j, c := range r {
			b := mlayout(c, cs)
			cells[i] = append(cells[i], b)
			colW[j] = max(colW[j], b.w)
			rowH[i] = max(rowH[i], b.h, 0.7*st.size)
			rowD[i] = max(rowD[i], b.d, 0.25*st.size)
		}
	}
	align := func(j int) byte {
		if n.Align == "" {
			return 'c'
		}
		if n.Align == "rl" {
			return "rl"[j%2]
		}
		return n.Align[min(j, len(n.Align)-1)]
	}
	gapFor := func(j int) float64 { // gap after column j
		if n.Align == "rl" {
			if j%2 == 0 {
				return 0.15 * st.size
			}
			return 1.5 * st.size
		}
		return 0.9 * st.size
	}
	w := 0.0
	xs := make([]float64, cols)
	for j := 0; j < cols; j++ {
		xs[j] = w
		w += colW[j]
		if j < cols-1 {
			w += gapFor(j)
		}
	}
	rowGap := 0.3 * st.size
	total := 0.0
	for i := range n.Rows {
		total += rowH[i] + rowD[i]
		if i > 0 {
			total += rowGap
		}
	}
	top := st.axis() + total/2
	b := &mbox{w: w + 0.1*st.size, h: top, d: total - top}
	b.draw = func(pg *pdf.Page, x, y float64) {
		cy := y + top
		for i, r := range cells {
			if i > 0 {
				cy -= rowGap
			}
			base := cy - rowH[i]
			for j, c := range r {
				cx := x + xs[j]
				switch align(j) {
				case 'c':
					cx += (colW[j] - c.w) / 2
				case 'r':
					cx += colW[j] - c.w
				}
				c.draw(pg, cx, base)
			}
			cy = base - rowD[i]
		}
	}
	return b
}

func maccent(n *MNode, st mstyle) *mbox {
	body := mlayout(kid(n, 0), st)
	lw := 0.045 * st.size
	b := &mbox{w: body.w, h: body.h, d: body.d, cls: body.cls}
	switch n.Text {
	case "¯", "→":
		b.h = body.h + 0.18*st.size
		arrow := n.Text == "→"
		b.draw = func(pg *pdf.Page, x, y float64) {
			body.draw(pg, x, y)
			ly := y + body.h + 0.1*st.size
			pg.Line(x+0.05*st.size, ly, x+body.w, ly, lw)
			if arrow {
				pg.Path(lw, x+body.w-0.12*st.size, ly+0.07*st.size, x+body.w, ly, x+body.w-0.12*st.size, ly-0.07*st.size)
			}
		}
	case "_":
		b.d = body.d + 0.15*st.size
		b.draw = func(pg *pdf.Page, x, y float64) {
			body.draw(pg, x, y)
			ly := y - body.d - 0.08*st.size
			pg.Line(x, ly, x+body.w, ly, lw)
		}
	case "⏞", "⏟":
		under := n.Under
		if under {
			b.d = body.d + 0.3*st.size
		} else {
			b.h = body.h + 0.3*st.size
		}
		b.draw = func(pg *pdf.Page, x, y float64) {
			body.draw(pg, x, y)
			if under {
				ly := y - body.d - 0.1*st.size
				pg.Path(lw, x, ly, x, ly-0.1*st.size, x+body.w/2-0.05*st.size, ly-0.1*st.size, x+body.w/2, ly-0.2*st.size,
					x+body.w/2+0.05*st.size, ly-0.1*st.size, x+body.w, ly-0.1*st.size, x+body.w, ly)
			} else {
				ly := y + body.h + 0.1*st.size
				pg.Path(lw, x, ly, x, ly+0.1*st.size, x+body.w/2-0.05*st.size, ly+0.1*st.size, x+body.w/2, ly+0.2*st.size,
					x+body.w/2+0.05*st.size, ly+0.1*st.size, x+body.w, ly+0.1*st.size, x+body.w, ly)
			}
		}
	default:
		acc := glyphs(n.Text, st.size*0.9, pdf.TimesRoman)
		b.h = body.h + 0.2*st.size
		b.draw = func(pg *pdf.Page, x, y float64) {
			body.draw(pg, x, y)
			acc.draw(pg, x+(body.w-acc.w)/2+0.05*st.size, y+body.h-0.35*st.size)
		}
	}
	return b
}
