package statement

import (
	"fmt"
	"strings"
)

// HTMLOptions says how to write the HTML.
type HTMLOptions struct {
	// Image returns the URL of an attached image ("" leaves it out).
	Image func(name string) string
	// Labels are the translated words used around the examples.
	Labels Labels
	// Examples are shown after the statement (the task's own, plus those
	// written in the source).
	Examples []Example
}

// Labels are the words the renderers write themselves.
type Labels struct {
	Examples, Example, Input, Output, Note string
}

// DefaultLabels are the English labels.
var DefaultLabels = Labels{Examples: "Examples", Example: "Example %d", Input: "Input", Output: "Output", Note: "Explanation"}

// HTML renders the statement as an HTML fragment (formulas in MathML).
func (d *Doc) HTML(o HTMLOptions) string {
	if o.Labels.Input == "" {
		o.Labels = DefaultLabels
	}
	w := &htmlWriter{o: o}
	w.examples = append(append([]Example(nil), d.Examples...), o.Examples...)
	w.sb.WriteString(`<div class="statement">`)
	w.blocks(d.Blocks)
	if !w.examplesDone {
		w.writeExamples()
	}
	w.sb.WriteString(`</div>`)
	return w.sb.String()
}

func (w *htmlWriter) writeExamples() {
	w.examplesDone = true
	o := w.o
	examples := w.examples
	if len(examples) > 0 {
		fmt.Fprintf(&w.sb, `<h2 class="st-examples">%s</h2>`, esc(o.Labels.Examples))
		for i, e := range examples {
			fmt.Fprintf(&w.sb, `<section class="example"><h3>%s</h3><div class="io">`, esc(fmt.Sprintf(o.Labels.Example, i+1)))
			fmt.Fprintf(&w.sb, `<div><div class="io-label">%s</div><pre>%s</pre></div>`, esc(o.Labels.Input), esc(e.Input))
			fmt.Fprintf(&w.sb, `<div><div class="io-label">%s</div><pre>%s</pre></div>`, esc(o.Labels.Output), esc(e.Output))
			w.sb.WriteString(`</div>`)
			if len(e.Note) > 0 {
				fmt.Fprintf(&w.sb, `<div class="note"><div class="io-label">%s</div>`, esc(o.Labels.Note))
				w.blocks(e.Note)
				w.sb.WriteString(`</div>`)
			}
			w.sb.WriteString(`</section>`)
		}
	}
}

type htmlWriter struct {
	sb           strings.Builder
	o            HTMLOptions
	examples     []Example
	examplesDone bool
}

func (w *htmlWriter) blocks(bs []Block) {
	for _, b := range bs {
		w.block(b)
	}
}

func (w *htmlWriter) block(b Block) {
	switch b := b.(type) {
	case *Heading:
		l := min(max(b.Level+1, 2), 5) // the page's own h1 is the task title
		fmt.Fprintf(&w.sb, "<h%d>", l)
		w.inlines(b.Text)
		fmt.Fprintf(&w.sb, "</h%d>", l)
	case *Para:
		w.sb.WriteString("<p>")
		w.inlines(b.Text)
		w.sb.WriteString("</p>")
	case *List:
		if b.Ordered {
			if b.Start > 1 {
				fmt.Fprintf(&w.sb, `<ol start="%d">`, b.Start)
			} else {
				w.sb.WriteString("<ol>")
			}
		} else {
			w.sb.WriteString("<ul>")
		}
		for _, it := range b.Items {
			w.sb.WriteString("<li>")
			// A single paragraph item is written without <p>.
			if len(it) == 1 {
				if p, ok := it[0].(*Para); ok {
					w.inlines(p.Text)
					w.sb.WriteString("</li>")
					continue
				}
			}
			w.blocks(it)
			w.sb.WriteString("</li>")
		}
		if b.Ordered {
			w.sb.WriteString("</ol>")
		} else {
			w.sb.WriteString("</ul>")
		}
	case *Code:
		fmt.Fprintf(&w.sb, "<pre><code>%s</code></pre>", esc(b.Text))
	case *Display:
		fmt.Fprintf(&w.sb, `<div class="display">%s</div>`, b.Math.MathML())
	case *Table:
		w.sb.WriteString(`<div class="table-wrap"><table>`)
		for i, r := range b.Rows {
			tag := "td"
			if i == 0 && b.Header {
				tag = "th"
			}
			w.sb.WriteString("<tr>")
			for j, c := range r {
				cls := ""
				if j < len(b.Align) {
					switch b.Align[j] {
					case 'c':
						cls = ` class="c"`
					case 'r':
						cls = ` class="r"`
					}
				}
				fmt.Fprintf(&w.sb, "<%s%s>", tag, cls)
				w.inlines(c)
				fmt.Fprintf(&w.sb, "</%s>", tag)
			}
			w.sb.WriteString("</tr>")
		}
		w.sb.WriteString("</table></div>")
	case *Quote:
		w.sb.WriteString("<blockquote>")
		w.blocks(b.Blocks)
		w.sb.WriteString("</blockquote>")
	case *Rule:
		w.sb.WriteString("<hr>")
	case *ExamplesHere:
		if !w.examplesDone {
			w.writeExamples()
		}
	case *Image:
		src := ""
		if w.o.Image != nil {
			src = w.o.Image(b.Src)
		}
		if src != "" {
			fmt.Fprintf(&w.sb, `<figure><img src="%s" alt="%s" loading="lazy">`, esc(src), esc(b.Alt))
			if b.Alt != "" {
				fmt.Fprintf(&w.sb, "<figcaption>%s</figcaption>", esc(b.Alt))
			}
			w.sb.WriteString("</figure>")
		}
	}
}

func (w *htmlWriter) inlines(in []Inline) {
	for _, i := range in {
		switch i := i.(type) {
		case *Text:
			w.sb.WriteString(esc(i.S))
		case *Styled:
			tag := map[byte]string{'i': "em", 'b': "strong", 'u': "u", 's': "s"}[i.Style]
			if tag == "" {
				tag = "span"
			}
			fmt.Fprintf(&w.sb, "<%s>", tag)
			w.inlines(i.Text)
			fmt.Fprintf(&w.sb, "</%s>", tag)
		case *CodeSpan:
			fmt.Fprintf(&w.sb, "<code>%s</code>", esc(i.S))
		case *InlineMath:
			w.sb.WriteString(i.Math.MathML())
		case *Link:
			if safeURL(i.URL) {
				fmt.Fprintf(&w.sb, `<a href="%s" rel="noopener noreferrer">`, esc(i.URL))
				w.inlines(i.Text)
				w.sb.WriteString("</a>")
			} else {
				w.inlines(i.Text)
			}
		case *Break:
			w.sb.WriteString("<br>")
		}
	}
}

// safeURL accepts web and mail links only.
func safeURL(u string) bool {
	l := strings.ToLower(strings.TrimSpace(u))
	return strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "mailto:")
}
