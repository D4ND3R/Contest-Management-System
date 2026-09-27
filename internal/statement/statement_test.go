package statement

import (
	"bytes"
	"compress/zlib"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/pdf"
)

// TestMathML: the formulas statements use most, as MathML.
func TestMathML(t *testing.T) {
	for _, c := range []struct {
		tex  string
		want []string
	}{
		{`1 \le N \le 10^5`, []string{`<mn>1</mn><mo>≤</mo><mi>N</mi>`, `<msup><mn>10</mn><mn>5</mn></msup>`}},
		{`a_1, a_2, \ldots, a_n`, []string{`<msub><mi>a</mi><mn>1</mn></msub>`, `<mo>…</mo>`}},
		{`\frac{n(n+1)}{2}`, []string{`<mfrac>`, `<mo stretchy="false">(</mo>`}},
		{`\sqrt{x^2+y^2}`, []string{`<msqrt>`}},
		{`\sqrt[3]{n}`, []string{`<mroot>`}},
		{`\left\lfloor \frac{a}{b} \right\rfloor`, []string{`<mo fence="true" stretchy="true">⌊</mo>`, `⌋</mo>`}},
		{`\sum_{i=1}^{n} i`, []string{`<munderover><mo movablelimits="true">∑</mo>`}},
		{`x \bmod m`, []string{`<mi>mod</mi>`}},
		{`\binom{n}{k}`, []string{`<mfrac linethickness="0">`}},
		{`\mathbb{R}, \mathbb{N}`, []string{`ℝ`, `ℕ`}},
		{`\text{si } x > 0`, []string{`<mtext>si</mtext>`}},
		{`\begin{cases} 1 & n = 0 \\ n f(n-1) & n > 0 \end{cases}`, []string{`<mtable columnalign="left left">`, `<mo fence="true" stretchy="true">{</mo>`}},
		{`\alpha \ne \beta`, []string{`<mi>α</mi><mo>≠</mo><mi>β</mi>`}},
		{`\not\in`, []string{`<mo>∉</mo>`}},
		{`f'(x)`, []string{`<msup><mi>f</mi><mo>′</mo></msup>`}},
	} {
		m, warn := ParseMath(c.tex, c.tex == `\sum_{i=1}^{n} i`)
		got := m.MathML()
		if len(warn) > 0 {
			t.Errorf("%s: warnings %v", c.tex, warn)
		}
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s:\n got %s\nwant %s", c.tex, got, w)
			}
		}
	}
	// Unknown commands are shown, not dropped, and reported.
	m, warn := ParseMath(`\foo{x}`, false)
	if len(warn) != 1 || !strings.Contains(m.MathML(), `<merror><mtext>\foo</mtext></merror>`) {
		t.Fatalf("unknown command: %v %s", warn, m.MathML())
	}
}

// TestMarkdown: blocks and inline markup, formulas, and money that is not
// a formula.
func TestMarkdown(t *testing.T) {
	d := ParseMarkdown("# Suma\n\nDados $N$ números **positivos** y *x*.\nCuesta $5 y $10.\n\n## Entrada\n\n" +
		"- uno\n- dos\n\n1. a\n2. b\n\n| a | b |\n|:-:|--:|\n| $x$ | 2 |\n\n```\nint main() {}\n```\n\n$$\\sum_{i=1}^n i$$\n\n{{examples}}\n\n> nota\n\n![figura](fig.png)\n")
	if d.Title != "Suma" || len(d.Warnings) != 0 {
		t.Fatalf("title %q, warnings %v", d.Title, d.Warnings)
	}
	html := d.HTML(HTMLOptions{Examples: []Example{{Input: "1 2\n", Output: "3\n"}},
		Image: func(name string) string { return "/att/" + name }})
	for _, w := range []string{
		`<p>Dados <math><mi>N</mi></math> números <strong>positivos</strong> y <em>x</em>. Cuesta $5 y $10.</p>`,
		`<h3>Entrada</h3>`, `<ul><li>uno</li><li>dos</li></ul>`, `<ol><li>a</li><li>b</li></ol>`,
		`<th class="c">a</th><th class="r">b</th>`, `<td class="c"><math><mi>x</mi></math></td>`,
		`<pre><code>int main() {}</code></pre>`, `<div class="display"><math display="block">`,
		`<blockquote><p>nota</p></blockquote>`, `<img src="/att/fig.png" alt="figura"`,
	} {
		if !strings.Contains(html, w) {
			t.Errorf("missing %s in\n%s", w, html)
		}
	}
	// The examples go where the marker is: before the quote.
	if strings.Index(html, `class="example"`) > strings.Index(html, "<blockquote>") {
		t.Errorf("examples not at the marker:\n%s", html)
	}
}

// TestLaTeX: olymp.sty problems (Polygon), Polygon's $$$ delimiters and
// the usual text commands.
func TestLaTeX(t *testing.T) {
	d := ParseLaTeXIn(`\documentclass{article}\begin{document}
\begin{problem}{Suma}{standard input}{standard output}{1 second}{256 megabytes}
Sea $$$n$$$ un entero {\bf grande} y \textit{positivo}. Usa \texttt{long long} --- o no.

\InputFile
Un entero $n$ ($1 \le n \le 10^{9}$).
\begin{itemize}
\item primero
\item[b)] segundo
\end{itemize}
\begin{tabular}{|l|c|}
\hline
Subtarea & Puntos \\
1 & 20 \\
\end{tabular}
\Examples
\begin{example}
\exmp{3
}{6
}%
\end{example}
\Note
Porque $$$$$$\frac{n(n+1)}{2}$$$$$$ y \'arbol.
\end{problem}
\end{document}`, "es")
	if d.Title != "Suma" || len(d.Examples) != 1 || d.Examples[0].Input != "3\n" || d.Examples[0].Output != "6\n" {
		t.Fatalf("title %q, examples %+v", d.Title, d.Examples)
	}
	html := d.HTML(HTMLOptions{Labels: LabelsFor("es")})
	for _, w := range []string{
		`<strong>grande</strong>`, `<em>positivo</em>`, `<code>long long</code> — o no.`, `<h3>Entrada</h3>`,
		`<li><strong>b)</strong> segundo</li>`, `<td>Subtarea</td><td class="c">Puntos</td>`, `<h2 class="st-examples">Ejemplos</h2>`,
		`<h3>Nota</h3>`, `<math display="block"><mfrac>`, `árbol`,
	} {
		if !strings.Contains(html, w) {
			t.Errorf("missing %s in\n%s", w, html)
		}
	}
	if strings.Index(html, "st-examples") > strings.Index(html, "<h3>Nota</h3>") {
		t.Errorf("examples should come before the note:\n%s", html)
	}
	if w := ParseLaTeX(`\weird{x} y`).Warnings; len(w) != 1 || w[0] != `unsupported command \weird` {
		t.Errorf("warnings: %v", w)
	}
}

// TestHTMLImport: Polygon's HTML layout, formulas in text, and nothing
// active kept.
func TestHTMLImport(t *testing.T) {
	d := ParseHTML(`<html><head><title>X</title><script>alert(1)</script></head><body>
<div class="problem-statement"><div class="header"><div class="title">A. Suma</div><div class="time-limit">1 s</div></div>
<div class="legend"><p>Sea $$$n$$$ con 10<sup>9</sup> y <b>nada</b> más.<script>evil()</script></p></div>
<div class="input-specification"><div class="section-title">Entrada</div><p>Un entero.</p></div>
<div class="sample-tests"><div class="sample-test"><div class="input"><pre>1 2
</pre></div><div class="output"><pre>3
</pre></div></div></div></div></body></html>`)
	html := d.HTML(HTMLOptions{})
	if d.Title != "A. Suma" || len(d.Examples) != 1 || d.Examples[0].Input != "1 2\n" {
		t.Fatalf("title %q examples %+v", d.Title, d.Examples)
	}
	for _, w := range []string{`<math><mi>n</mi></math>`, `<msup><mrow></mrow><mtext>9</mtext></msup>`, `<strong>nada</strong>`, `<h3>Entrada</h3>`} {
		if !strings.Contains(html, w) {
			t.Errorf("missing %s in\n%s", w, html)
		}
	}
	if strings.Contains(html, "evil") || strings.Contains(html, "alert") || strings.Contains(html, "<script") {
		t.Fatalf("active content kept:\n%s", html)
	}
}

// TestHTMLEscapes: statement text never becomes markup.
func TestHTMLEscapes(t *testing.T) {
	html := ParseMarkdown("<script>x</script> [a](javascript:alert(1)) $<b>$").HTML(HTMLOptions{})
	if strings.Contains(html, "<script>") || strings.Contains(html, "javascript:") || strings.Contains(html, "<b>") {
		t.Fatalf("unescaped:\n%s", html)
	}
}

// TestPDF: a statement typesets to a valid PDF with its title, limits,
// formulas and examples, in the standard fonts only.
func TestPDF(t *testing.T) {
	d := ParseMarkdown("Calcula $\\sum_{i=1}^{n} \\lfloor a_i / 2 \\rfloor \\le 10^9$ con α y ≥.\n\n## Entrada\n\nUn entero.\n")
	data := d.PDF(PDFOptions{Title: "A. Suma", Subtitle: "Olimpiada", Info: [][2]string{{"Límite de tiempo", "1 s"}},
		Labels: LabelsFor("es"), Examples: []Example{{Input: "1 2\n", Output: "3\n"}}})
	if !bytes.HasPrefix(data, []byte("%PDF-1.4")) {
		t.Fatal("not a PDF")
	}
	if n, err := pdf.CountPages(data); err != nil || n != 1 {
		t.Fatalf("pages %d, %v", n, err)
	}
	text := pdfText(t, data)
	for _, w := range []string{"A. Suma", "Olimpiada", "Entrada", "Ejemplos", "ENTRADA", "SALIDA", "1 2"} {
		if !strings.Contains(text, "("+w+")") {
			t.Errorf("%q not drawn", w)
		}
	}
	if !strings.Contains(text, "/F11 ") {
		t.Error("the Symbol font (F11) is not used for α, ≤ or ∑")
	}
	if strings.Contains(text, "?") {
		t.Errorf("a character could not be shown:\n%s", text)
	}
	if !bytes.Contains(data, []byte("/BaseFont /Symbol")) || bytes.Contains(data, []byte("/FontFile")) {
		t.Error("expected the standard fonts, nothing embedded")
	}
}

// pdfText inflates the page contents of a PDF written by package pdf.
func pdfText(t *testing.T, data []byte) string {
	t.Helper()
	var out strings.Builder
	re := regexp.MustCompile(`(?s)/Filter /FlateDecode >>\nstream\n(.*?)\nendstream`)
	for _, m := range re.FindAllSubmatch(data, -1) {
		zr, err := zlib.NewReader(bytes.NewReader(m[1]))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(zr)
		out.Write(b)
	}
	return out.String()
}

// TestMathBoxes: the layout of the constructions statements use has sane
// dimensions (a fraction is taller than a letter, limits deeper...).
func TestMathBoxes(t *testing.T) {
	size := 11.0
	box := func(tex string, display bool) *mbox {
		m, _ := ParseMath(tex, display)
		return layoutMath(m, size)
	}
	x := box("x", false)
	frac := box(`\frac{a}{b}`, false)
	sum := box(`\sum_{i=1}^{n} i`, true)
	sqrt := box(`\sqrt{x}`, false)
	if !(frac.h > x.h && frac.d > x.d && sum.d > frac.d && sqrt.h > x.h && x.w > 0) {
		t.Fatalf("x %+v frac %+v sum %+v sqrt %+v", *x, *frac, *sum, *sqrt)
	}
	// Spacing: a relation is wider than the same letters side by side.
	if box("a=b", false).w <= box("ab", false).w+box("=", false).w {
		t.Error("no space around a relation")
	}
	// A leading minus is unary: no medium space after it.
	if box("-a", false).w >= box("b-a", false).w-box("b", false).w {
		t.Error("unary minus spaced like a binary one")
	}
}

// TestParseFormats: content types choose the parser.
func TestParseFormats(t *testing.T) {
	for name, want := range map[string]string{"es.md": TypeMarkdown, "en.tex": TypeLaTeX, "a.HTML": TypeHTML, "x.pdf": TypePDF, "y.docx": ""} {
		if got := TypeForName(name); got != want {
			t.Errorf("%s: %q", name, got)
		}
	}
	if _, ok := Parse(TypePDF, nil); ok {
		t.Error("a PDF parsed")
	}
	d, ok := Parse(TypeLaTeX, []byte(`\section{A}`))
	if !ok || len(d.Blocks) != 1 {
		t.Errorf("latex: %v %+v", ok, d)
	}
	if FormatBytes(256<<20) != "256 MiB" || FormatBytes(1536<<20) != "1.5 GiB" || FormatSeconds(1500e6) != "1.5 s" {
		t.Error("formats")
	}
}
