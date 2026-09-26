package webkit

import (
	"fmt"
	"hash/fnv"
	"html/template"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// UIFuncs are the template helpers of the shared design (every site adds
// them to its own).
func UIFuncs() template.FuncMap {
	return template.FuncMap{
		"icon":     Icon,
		"letter":   Letter,
		"lclass":   func(i int) string { return "c" + strconv.Itoa((i+1)%8) },
		"initials": Initials,
		"avatarc":  AvatarClass,
		"donut":    Donut,
		"share":    Percent,
	}
}

// Letter names the i-th task of a contest (0 → A, 25 → Z, 26 → AA).
func Letter(i int) string {
	if i < 0 {
		return ""
	}
	s := ""
	for i >= 0 {
		s = string(rune('A'+i%26)) + s
		i = i/26 - 1
	}
	return s
}

// Initials are the first letters of the first two words of a name (or the
// first two letters of a single word), for avatars.
func Initials(name string) string {
	var out []rune
	for _, w := range strings.FieldsFunc(name, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		out = append(out, []rune(w)[0])
		if len(out) == 2 {
			break
		}
	}
	if len(out) == 1 {
		if r := []rune(strings.TrimSpace(name)); len(r) > 1 && (unicode.IsLetter(r[1]) || unicode.IsDigit(r[1])) {
			out = append(out, r[1])
		}
	}
	return strings.ToUpper(string(out))
}

// AvatarClass picks one of the avatar colours from a name, so a person
// keeps the same colour everywhere.
func AvatarClass(name string) string {
	h := fnv.New32a()
	h.Write([]byte(name))
	return "c" + strconv.Itoa(int(h.Sum32()%8))
}

// Percent is part/total as a whole percentage ("0" when total is 0).
func Percent(part, total any) string {
	p, t := toFloat(part), toFloat(total)
	if t <= 0 {
		return "0"
	}
	v := 100 * p / t
	if v > 0 && v < 10 {
		return strconv.FormatFloat(v, 'f', 1, 64)
	}
	return strconv.FormatFloat(math.Round(v), 'f', 0, 64)
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case int:
		return float64(x)
	case int32:
		return float64(x)
	case int64:
		return float64(x)
	case float64:
		return x
	}
	return 0
}

// Donut draws a ring split in up to three parts (classes d1, d2, d3) with
// big text in the middle and a small caption under it.
func Donut(big, caption string, parts ...int) template.HTML {
	total := 0
	for _, p := range parts {
		total += p
	}
	const r = 48.0
	circ := 2 * math.Pi * r
	var b strings.Builder
	b.WriteString(`<svg viewBox="0 0 128 128" role="img" aria-label="` + template.HTMLEscapeString(big+" "+caption) + `">`)
	b.WriteString(`<circle class="track" cx="64" cy="64" r="48"/>`)
	if total > 0 {
		offset := 0.0
		for i, p := range parts {
			if p <= 0 {
				continue
			}
			l := circ * float64(p) / float64(total)
			// Start at 12 o'clock and go clockwise.
			fmt.Fprintf(&b, `<circle class="d%d" cx="64" cy="64" r="48" stroke-dasharray="%.2f %.2f" stroke-dashoffset="%.2f" transform="rotate(-90 64 64)"/>`,
				i+1, l, circ-l, -offset)
			offset += l
		}
	}
	b.WriteString(`<text x="64" y="66" text-anchor="middle">` + template.HTMLEscapeString(big) + `</text>`)
	b.WriteString(`<text class="sub" x="64" y="84" text-anchor="middle">` + template.HTMLEscapeString(caption) + `</text></svg>`)
	return template.HTML(b.String())
}

// Series is one line of a chart.
type Series struct {
	Class  string // s1, s2, s3 (colour)
	Area   string // a1, a2 or "" (filled under the line)
	Values []float64
}

// LineChart draws series over the same x axis (labels every few points)
// as an SVG sized by its container.
func LineChart(labels []string, series ...Series) template.HTML {
	const w, h, left, bottom, top, right = 460.0, 200.0, 34.0, 22.0, 10.0, 10.0
	n := 0
	max := 0.0
	for _, s := range series {
		if len(s.Values) > n {
			n = len(s.Values)
		}
		for _, v := range s.Values {
			max = math.Max(max, v)
		}
	}
	step := niceStep(max / 4)
	top4 := step * 4
	if top4 < max {
		top4 = step * math.Ceil(max/step)
	}
	if top4 == 0 {
		top4 = 4
		step = 1
	}
	pw, ph := w-left-right, h-top-bottom
	x := func(i int) float64 {
		if n <= 1 {
			return left + pw/2
		}
		return left + pw*float64(i)/float64(n-1)
	}
	y := func(v float64) float64 { return top + ph - ph*v/top4 }
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="chart" viewBox="0 0 %.0f %.0f" role="img">`, w, h)
	for v := 0.0; v <= top4+1e-9; v += step {
		fmt.Fprintf(&b, `<line class="grid" x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f"/><text x="%.1f" y="%.1f" text-anchor="end">%s</text>`,
			left, w-right, y(v), y(v), left-6, y(v)+4, strconv.FormatFloat(v, 'f', -1, 64))
	}
	every := 1
	if n > 6 {
		every = (n + 5) / 6
	}
	for i, l := range labels {
		if i%every == 0 || i == n-1 && (n-1)%every > every/2 {
			fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" text-anchor="middle">%s</text>`, x(i), h-4, template.HTMLEscapeString(l))
		}
	}
	for _, s := range series {
		if len(s.Values) == 0 {
			continue
		}
		var pts strings.Builder
		for i, v := range s.Values {
			if i > 0 {
				pts.WriteByte(' ')
			}
			fmt.Fprintf(&pts, "%.1f,%.1f", x(i), y(v))
		}
		if s.Area != "" {
			fmt.Fprintf(&b, `<polygon class="%s" points="%.1f,%.1f %s %.1f,%.1f"/>`, s.Area, x(0), y(0), pts.String(), x(len(s.Values)-1), y(0))
		}
		fmt.Fprintf(&b, `<polyline class="ln %s" points="%s"/>`, s.Class, pts.String())
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// niceStep rounds a raw axis step up to 1, 2 or 5 times a power of ten.
func niceStep(raw float64) float64 {
	if raw <= 0 {
		return 1
	}
	p := math.Pow(10, math.Floor(math.Log10(raw)))
	for _, m := range []float64{1, 2, 5, 10} {
		if raw <= m*p {
			return math.Max(1, m*p)
		}
	}
	return 10 * p
}
