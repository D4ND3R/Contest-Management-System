package statement

import (
	"fmt"
	"html"
	"strings"
)

// MathML renders a formula as MathML (browsers lay it out natively).
func (m *Math) MathML() string {
	var sb strings.Builder
	if m.Display {
		sb.WriteString(`<math display="block">`)
	} else {
		sb.WriteString(`<math>`)
	}
	w := &mlWriter{sb: &sb, display: m.Display}
	w.node(m.Root)
	sb.WriteString(`</math>`)
	return sb.String()
}

type mlWriter struct {
	sb      *strings.Builder
	display bool
}

func (w *mlWriter) printf(format string, a ...any) { fmt.Fprintf(w.sb, format, a...) }

func esc(s string) string { return html.EscapeString(s) }

// class maps a variant to a CSS class (MathML Core only knows "normal").
func variantClass(v string) string {
	switch v {
	case "bold":
		return ` class="m-bf"`
	case "tt":
		return ` class="m-tt"`
	case "sf":
		return ` class="m-sf"`
	case "italic":
		return ` class="m-it"`
	}
	return ""
}

func (w *mlWriter) node(n *MNode) {
	if n == nil {
		w.sb.WriteString("<mrow></mrow>")
		return
	}
	switch n.Kind {
	case MRow:
		if len(n.Kids) == 1 {
			w.node(n.Kids[0])
			return
		}
		w.sb.WriteString("<mrow>")
		for _, k := range n.Kids {
			w.node(k)
		}
		w.sb.WriteString("</mrow>")
	case MIdent:
		t := styleLetters(n.Text, n.Variant)
		attr := variantClass(n.Variant)
		if n.Variant == "normal" && len([]rune(t)) == 1 {
			attr = ` mathvariant="normal"`
		}
		w.printf("<mi%s>%s</mi>", attr, esc(t))
	case MNum:
		w.printf("<mn%s>%s</mn>", variantClass(n.Variant), esc(n.Text))
	case MOp:
		if n.Variant == "word" {
			w.printf("<mi>%s</mi>", esc(n.Text))
			return
		}
		attr := ""
		if n.Size > 0 {
			s := []float64{0, 1.2, 1.8, 2.4, 3}[n.Size]
			attr = fmt.Sprintf(` stretchy="true" symmetric="true" minsize="%.1fem" maxsize="%.1fem"`, s, s)
		} else if n.Class == ClassOpen || n.Class == ClassClose {
			attr = ` stretchy="false"`
		}
		if n.Class == ClassLarge && n.Limits {
			attr += ` movablelimits="true"`
		}
		w.printf("<mo%s>%s</mo>", attr, esc(n.Text))
	case MText:
		w.printf("<mtext%s>%s</mtext>", variantClass(n.Variant), esc(n.Text))
	case MFrac:
		if n.NoRule {
			w.sb.WriteString(`<mfrac linethickness="0">`)
		} else {
			w.sb.WriteString("<mfrac>")
		}
		w.node(kid(n, 0))
		w.node(kid(n, 1))
		w.sb.WriteString("</mfrac>")
	case MSqrt:
		if len(n.Kids) > 1 && n.Kids[1] != nil {
			w.sb.WriteString("<mroot>")
			w.node(n.Kids[0])
			w.node(n.Kids[1])
			w.sb.WriteString("</mroot>")
			return
		}
		w.sb.WriteString("<msqrt>")
		w.node(kid(n, 0))
		w.sb.WriteString("</msqrt>")
	case MScripts:
		base, sub, sup := n.Kids[0], n.Kids[1], n.Kids[2]
		under := n.Limits && (w.display || base.Kind != MOp || base.Class != ClassLarge)
		tags := [3]string{"msub", "msup", "msubsup"}
		if under {
			tags = [3]string{"munder", "mover", "munderover"}
		}
		switch {
		case sub != nil && sup != nil:
			w.printf("<%s>", tags[2])
			w.node(base)
			w.node(sub)
			w.node(sup)
			w.printf("</%s>", tags[2])
		case sub != nil:
			w.printf("<%s>", tags[0])
			w.node(base)
			w.node(sub)
			w.printf("</%s>", tags[0])
		case sup != nil:
			w.printf("<%s>", tags[1])
			w.node(base)
			w.node(sup)
			w.printf("</%s>", tags[1])
		default:
			w.node(base)
		}
	case MFenced:
		w.sb.WriteString("<mrow>")
		if n.Open != "" {
			w.printf(`<mo fence="true" stretchy="true">%s</mo>`, esc(n.Open))
		}
		w.node(kid(n, 0))
		if n.Close != "" {
			w.printf(`<mo fence="true" stretchy="true">%s</mo>`, esc(n.Close))
		}
		w.sb.WriteString("</mrow>")
	case MSpace:
		if n.Em > 0 {
			w.printf(`<mspace width="%.3fem"></mspace>`, n.Em)
		}
	case MTable:
		align := make([]string, 0, 8)
		cols := 0
		for _, r := range n.Rows {
			cols = max(cols, len(r))
		}
		for i := 0; i < cols; i++ {
			a := byte('c')
			if n.Align != "" {
				a = n.Align[min(i, len(n.Align)-1)]
				if len(n.Align) == 2 && n.Align == "rl" {
					a = "rl"[i%2]
				}
			}
			align = append(align, map[byte]string{'l': "left", 'c': "center", 'r': "right"}[a])
		}
		w.printf(`<mtable columnalign="%s">`, strings.Join(align, " "))
		for _, r := range n.Rows {
			w.sb.WriteString("<mtr>")
			for _, c := range r {
				w.sb.WriteString("<mtd>")
				w.node(c)
				w.sb.WriteString("</mtd>")
			}
			w.sb.WriteString("</mtr>")
		}
		w.sb.WriteString("</mtable>")
	case MAccent:
		mark := n.Text
		stretch := ""
		switch mark {
		case "¯":
			mark, stretch = "‾", ` stretchy="true"`
		case "_":
			mark, stretch = "_", ` stretchy="true"`
		case "→", "⏞", "⏟":
			stretch = ` stretchy="true"`
		}
		if n.Under {
			w.sb.WriteString(`<munder accentunder="true">`)
			w.node(kid(n, 0))
			w.printf(`<mo%s>%s</mo></munder>`, stretch, esc(mark))
			return
		}
		w.sb.WriteString(`<mover accent="true">`)
		w.node(kid(n, 0))
		w.printf(`<mo%s>%s</mo></mover>`, stretch, esc(mark))
	case MError:
		w.printf(`<merror><mtext>%s</mtext></merror>`, esc(n.Text))
	}
}

func kid(n *MNode, i int) *MNode {
	if i < len(n.Kids) {
		return n.Kids[i]
	}
	return nil
}

// styleLetters maps letters to the Unicode mathematical alphabets that
// MathML needs for blackboard, calligraphic and fraktur letters.
func styleLetters(s, variant string) string {
	var table map[rune]rune
	var base rune
	switch variant {
	case "bb":
		table, base = bbExceptions, 0x1D538
	case "cal":
		table, base = calExceptions, 0x1D49C
	case "frak":
		table, base = frakExceptions, 0x1D504
	default:
		return s
	}
	var sb strings.Builder
	for _, r := range s {
		switch {
		case table[r] != 0:
			sb.WriteRune(table[r])
		case r >= 'A' && r <= 'Z':
			sb.WriteRune(base + (r - 'A'))
		case r >= 'a' && r <= 'z' && variant != "bb":
			sb.WriteRune(base + 26 + (r - 'a'))
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

var bbExceptions = map[rune]rune{'C': 'ℂ', 'H': 'ℍ', 'N': 'ℕ', 'P': 'ℙ', 'Q': 'ℚ', 'R': 'ℝ', 'Z': 'ℤ'}
var calExceptions = map[rune]rune{'B': 'ℬ', 'E': 'ℰ', 'F': 'ℱ', 'H': 'ℋ', 'I': 'ℐ', 'L': 'ℒ', 'M': 'ℳ', 'R': 'ℛ',
	'e': 'ℯ', 'g': 'ℊ', 'o': 'ℴ'}
var frakExceptions = map[rune]rune{'C': 'ℭ', 'H': 'ℌ', 'I': 'ℑ', 'R': 'ℜ', 'Z': 'ℨ'}
