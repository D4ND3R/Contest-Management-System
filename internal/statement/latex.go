package statement

import (
	"github.com/D4ND3R/Contest-Management-System/internal/i18n"
	"strings"
	"unicode/utf8"
)

// ParseLaTeX reads a LaTeX statement: a whole document or a fragment, the
// olymp.sty problem environment (Polygon and many olympiads), Polygon's
// statement sections ($$$...$$$ formulas) and the usual text commands.
// Unknown commands keep their text and are listed in Warnings.
func ParseLaTeX(src string) *Doc { return ParseLaTeXIn(src, "") }

// ParseLaTeXIn is ParseLaTeX with olymp.sty's section titles in the
// statement's language (when the interface has it).
func ParseLaTeXIn(src, lang string) *Doc {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	if i := strings.Index(src, `\begin{document}`); i >= 0 {
		src = src[i+len(`\begin{document}`):]
		if j := strings.Index(src, `\end{document}`); j >= 0 {
			src = src[:j]
		}
	}
	// Polygon: $$$$$$...$$$$$$ displayed, $$$...$$$ inline.
	src = strings.ReplaceAll(src, "$$$$$$", "\x01")
	src = strings.ReplaceAll(src, "$$$", "\x02")
	p := &texParser{src: src, doc: &Doc{}, lang: UILang(lang)}
	p.doc.Blocks = p.blocks("")
	p.doc.Warnings = p.warn
	return p.doc
}

// Section titles written by olymp.sty's commands, in English; the caller
// translates the statement's own language by writing its sections.
var olympSections = map[string]string{
	"InputFile": "Input", "OutputFile": "Output", "Example": "Example", "Examples": "Examples",
	"Note": "Note", "Notes": "Notes", "Scoring": "Scoring", "Interaction": "Interaction",
	"Explanation": "Explanation", "Explanations": "Explanations", "Specification": "Specification",
	"Constraints": "Constraints", "Subtasks": "Subtasks",
}

type texParser struct {
	src  string
	pos  int
	warn []string
	doc  *Doc
	lang string // interface language of the section titles
}

func (p *texParser) eof() bool { return p.pos >= len(p.src) }

func (p *texParser) peek() byte {
	if p.eof() {
		return 0
	}
	return p.src[p.pos]
}

func (p *texParser) warnf(s string) {
	for _, w := range p.warn {
		if w == s {
			return
		}
	}
	p.warn = append(p.warn, s)
}

// command reads a command name after the backslash.
func (p *texParser) command() string {
	if p.eof() {
		return ""
	}
	start := p.pos
	c := p.src[p.pos]
	if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '@') {
		_, n := utf8.DecodeRuneInString(p.src[p.pos:])
		p.pos += n
		return p.src[start:p.pos]
	}
	for !p.eof() {
		c := p.src[p.pos]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '@' {
			p.pos++
			continue
		}
		break
	}
	name := p.src[start:p.pos]
	if !p.eof() && p.src[p.pos] == '*' {
		p.pos++
		name += "*"
	}
	return name
}

// skipSpaces skips blanks after a command word (not blank lines).
func (p *texParser) skipSpaces() {
	for !p.eof() && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t') {
		p.pos++
	}
	if !p.eof() && p.src[p.pos] == '\n' && !strings.HasPrefix(p.src[p.pos+1:], "\n") {
		p.pos++
		for !p.eof() && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t') {
			p.pos++
		}
	}
}

// raw reads a braced argument verbatim.
func (p *texParser) raw() string {
	p.skipSpaces()
	if p.peek() != '{' {
		if p.eof() {
			return ""
		}
		if p.peek() == '\\' {
			p.pos++
			return "\\" + p.command()
		}
		p.pos++
		return p.src[p.pos-1 : p.pos]
	}
	p.pos++
	depth := 1
	start := p.pos
	for !p.eof() {
		switch p.src[p.pos] {
		case '\\':
			p.pos++
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				p.pos++
				return p.src[start : p.pos-1]
			}
		}
		p.pos++
	}
	return p.src[start:]
}

// optional reads [..] verbatim when present.
func (p *texParser) optional() (string, bool) {
	save := p.pos
	p.skipSpaces()
	if p.peek() != '[' {
		p.pos = save
		return "", false
	}
	end := strings.IndexByte(p.src[p.pos:], ']')
	if end < 0 {
		p.pos = save
		return "", false
	}
	s := p.src[p.pos+1 : p.pos+end]
	p.pos += end + 1
	return s, true
}

// sub parses a piece of LaTeX (an argument) into inlines.
func (p *texParser) sub(s string) []Inline {
	q := &texParser{src: s, doc: p.doc, lang: p.lang}
	blocks := q.blocks("")
	p.warn = append(p.warn, q.warn...)
	var out []Inline
	for i, b := range blocks {
		if para, ok := b.(*Para); ok {
			if i > 0 {
				out = append(out, &Break{})
			}
			out = append(out, para.Text...)
		}
	}
	return out
}

// subBlocks parses a piece of LaTeX into blocks.
func (p *texParser) subBlocks(s string) []Block {
	q := &texParser{src: s, doc: p.doc, lang: p.lang}
	b := q.blocks("")
	p.warn = append(p.warn, q.warn...)
	return b
}

// envBody reads up to \end{name} (nested environments of the same name
// included) and returns the text in between.
func (p *texParser) envBody(name string) string {
	begin, end := `\begin{`+name+`}`, `\end{`+name+`}`
	depth := 1
	start := p.pos
	for !p.eof() {
		switch {
		case strings.HasPrefix(p.src[p.pos:], begin):
			depth++
			p.pos += len(begin)
		case strings.HasPrefix(p.src[p.pos:], end):
			depth--
			if depth == 0 {
				body := p.src[start:p.pos]
				p.pos += len(end)
				return body
			}
			p.pos += len(end)
		default:
			p.pos++
		}
	}
	return p.src[start:]
}

// blocks parses until \end{env} (env "" = the end of input).
func (p *texParser) blocks(env string) []Block {
	var out []Block
	var cur []Inline
	var text strings.Builder
	flushText := func() {
		if text.Len() > 0 {
			cur = append(cur, &Text{S: text.String()})
			text.Reset()
		}
	}
	flush := func() {
		flushText()
		if len(cur) > 0 {
			cur = trimInlines(cur)
			if len(cur) > 0 {
				out = append(out, &Para{Text: cur})
			}
			cur = nil
		}
	}
	add := func(b Block) {
		flush()
		out = append(out, b)
	}
	inl := func(i ...Inline) {
		flushText()
		cur = append(cur, i...)
	}
	for !p.eof() {
		c := p.src[p.pos]
		switch c {
		case '%':
			for !p.eof() && p.src[p.pos] != '\n' {
				p.pos++
			}
			p.pos++ // a comment eats its newline
			continue
		case '\n':
			// A blank line ends the paragraph.
			j := p.pos + 1
			for j < len(p.src) && (p.src[j] == ' ' || p.src[j] == '\t') {
				j++
			}
			if j < len(p.src) && p.src[j] == '\n' {
				flush()
				p.pos = j + 1
				continue
			}
			text.WriteByte(' ')
			p.pos++
			continue
		case '~':
			text.WriteString(" ")
			p.pos++
			continue
		case '{':
			// {\bf text}: an old style switch applies to its group.
			if st, ok := p.switchGroup(); ok {
				inl(st...)
				continue
			}
			p.pos++
			continue
		case '}':
			p.pos++
			continue
		case '&':
			p.pos++
			continue
		case '`':
			if strings.HasPrefix(p.src[p.pos:], "``") {
				text.WriteString("“")
				p.pos += 2
			} else {
				text.WriteString("‘")
				p.pos++
			}
			continue
		case '\'':
			if strings.HasPrefix(p.src[p.pos:], "''") {
				text.WriteString("”")
				p.pos += 2
			} else {
				text.WriteString("’")
				p.pos++
			}
			continue
		case '-':
			switch {
			case strings.HasPrefix(p.src[p.pos:], "---"):
				text.WriteString("—")
				p.pos += 3
			case strings.HasPrefix(p.src[p.pos:], "--"):
				text.WriteString("–")
				p.pos += 2
			default:
				text.WriteByte('-')
				p.pos++
			}
			continue
		case '\x01', '\x02': // Polygon formulas
			end := strings.IndexByte(p.src[p.pos+1:], c)
			if end < 0 {
				p.pos++
				continue
			}
			tex := p.src[p.pos+1 : p.pos+1+end]
			p.pos += end + 2
			m, w := ParseMath(tex, c == '\x01')
			p.warn = append(p.warn, w...)
			if c == '\x01' {
				add(&Display{Math: m})
			} else {
				inl(&InlineMath{Math: m})
			}
			continue
		case '$':
			display := strings.HasPrefix(p.src[p.pos:], "$$")
			delim := "$"
			if display {
				delim = "$$"
			}
			end := strings.Index(p.src[p.pos+len(delim):], delim)
			if end < 0 {
				text.WriteByte('$')
				p.pos++
				continue
			}
			tex := p.src[p.pos+len(delim) : p.pos+len(delim)+end]
			p.pos += len(delim)*2 + end
			m, w := ParseMath(tex, display)
			p.warn = append(p.warn, w...)
			if display {
				add(&Display{Math: m})
			} else {
				inl(&InlineMath{Math: m})
			}
			continue
		case '\\':
			p.pos++
			name := p.command()
			switch name {
			case "(", "[":
				closer := `\)`
				if name == "[" {
					closer = `\]`
				}
				end := strings.Index(p.src[p.pos:], closer)
				if end < 0 {
					end = len(p.src) - p.pos
				}
				tex := p.src[p.pos : p.pos+end]
				p.pos = min(len(p.src), p.pos+end+2)
				m, w := ParseMath(tex, name == "[")
				p.warn = append(p.warn, w...)
				if name == "[" {
					add(&Display{Math: m})
				} else {
					inl(&InlineMath{Math: m})
				}
				continue
			case "end":
				e := p.raw()
				if e == env {
					flush()
					return out
				}
				continue
			case "begin":
				e := p.raw()
				if b, inline := p.environment(e); b != nil {
					for _, x := range b {
						add(x)
					}
				} else if inline != nil {
					inl(inline...)
				}
				continue
			}
			if h, ok := olympSections[name]; ok {
				if name == "Example" || name == "Examples" {
					add(&ExamplesHere{})
					p.skipSpaces()
					continue
				}
				add(&Heading{Level: 2, Text: []Inline{&Text{S: i18n.T(p.lang, h)}}})
				p.skipSpaces()
				continue
			}
			if b, i, handled := p.textCommand(name); handled {
				if b != nil {
					for _, x := range b {
						add(x)
					}
				}
				if i != nil {
					inl(i...)
				}
				continue
			}
			p.warnf("unsupported command \\" + name)
			continue
		}
		// plain text
		r, n := utf8.DecodeRuneInString(p.src[p.pos:])
		text.WriteRune(r)
		p.pos += n
	}
	flush()
	return out
}

// accents maps \'e and friends to precomposed letters.
var textAccents = map[string]map[string]string{
	"'":  {"a": "á", "e": "é", "i": "í", "o": "ó", "u": "ú", "y": "ý", "A": "Á", "E": "É", "I": "Í", "O": "Ó", "U": "Ú", "\\i": "í", "c": "ć", "n": "ń", "s": "ś", "z": "ź"},
	"`":  {"a": "à", "e": "è", "i": "ì", "o": "ò", "u": "ù", "A": "À", "E": "È", "O": "Ò", "\\i": "ì"},
	"^":  {"a": "â", "e": "ê", "i": "î", "o": "ô", "u": "û", "A": "Â", "E": "Ê", "O": "Ô", "\\i": "î"},
	"\"": {"a": "ä", "e": "ë", "i": "ï", "o": "ö", "u": "ü", "A": "Ä", "E": "Ë", "O": "Ö", "U": "Ü", "\\i": "ï"},
	"~":  {"n": "ñ", "N": "Ñ", "a": "ã", "o": "õ", "A": "Ã", "O": "Õ"},
	"c":  {"c": "ç", "C": "Ç"},
	"v":  {"c": "č", "s": "š", "z": "ž", "C": "Č", "S": "Š", "Z": "Ž", "r": "ř", "e": "ě"},
}

var textSymbols = map[string]string{
	"%": "%", "&": "&", "_": "_", "#": "#", "$": "$", "{": "{", "}": "}", " ": " ", ",": " ",
	";": " ", "@": "", "/": "", "-": "", "ldots": "…", "dots": "…", "textellipsis": "…", "textendash": "–",
	"textemdash": "—", "textbackslash": "\\", "textasciitilde": "~", "textasciicircum": "^",
	"textless": "<", "textgreater": ">", "textbar": "|", "textquotedbl": "\"", "guillemotleft": "«",
	"guillemotright": "»", "ss": "ß", "o": "ø", "O": "Ø", "aa": "å", "AA": "Å", "ae": "æ", "AE": "Æ",
	"oe": "œ", "OE": "Œ", "i": "ı", "l": "ł", "L": "Ł", "copyright": "©", "textregistered": "®",
	"texttrademark": "™", "S": "§", "P": "¶", "dag": "†", "ddag": "‡", "pounds": "£", "euro": "€",
	"textdegree": "°", "LaTeX": "LaTeX", "TeX": "TeX", "quad": " ", "qquad": "  ",
	"enspace": " ", "thinspace": " ", "textquoteleft": "‘", "textquoteright": "’",
	"slash": "/", "textperiodcentered": "·", "textbullet": "•", "cdot": "·",
}

var styleCommands = map[string]byte{
	"textbf": 'b', "bfseries": 'b', "textit": 'i', "emph": 'i', "textsl": 'i', "underline": 'u', "uline": 'u',
	"sout": 's', "st": 's',
}

// ignored commands and how many braced arguments they take.
var ignoredCommands = map[string]int{
	"noindent": 0, "indent": 0, "par": 0, "medskip": 0, "bigskip": 0, "smallskip": 0, "centering": 0,
	"raggedright": 0, "raggedleft": 0, "newpage": 0, "clearpage": 0, "pagebreak": 0, "nopagebreak": 0,
	"linebreak": 0, "nolinebreak": 0, "hfill": 0, "vfill": 0, "maketitle": 0, "tableofcontents": 0,
	"small": 0, "footnotesize": 0, "scriptsize": 0, "tiny": 0, "normalsize": 0, "large": 0, "Large": 0,
	"LARGE": 0, "huge": 0, "Huge": 0, "rm": 0, "sf": 0, "sc": 0, "upshape": 0, "normalfont": 0,
	"vspace": 1, "vspace*": 1, "label": 1, "setlength": 2, "addtolength": 2, "pagestyle": 1,
	"thispagestyle": 1, "usepackage": 1, "documentclass": 1, "cite": 1, "index": 1, "hline": 0,
	"cline": 1, "toprule": 0, "midrule": 0, "bottomrule": 0, "protect": 0, "relax": 0,
	"selectlanguage": 1, "graphicspath": 1, "renewcommand": 2, "newcommand": 2, "def": 0,
	"exmpfile": 2, "createsection": 0, "hrule": 0, "strut": 0, "allowbreak": 0, "mbox": 0, "sloppy": 0,
}

// textCommand handles a command in text: new blocks, inlines, or nothing.
func (p *texParser) textCommand(name string) ([]Block, []Inline, bool) {
	if s, ok := textSymbols[name]; ok {
		if s != " " && name != " " {
			p.skipSpacesAfterWord(name)
		}
		return nil, []Inline{&Text{S: s}}, true
	}
	if acc, ok := textAccents[name]; ok {
		arg := p.raw()
		if l, ok := acc[arg]; ok {
			return nil, []Inline{&Text{S: l}}, true
		}
		return nil, []Inline{&Text{S: arg}}, true
	}
	if st, ok := styleCommands[name]; ok {
		if name == "bfseries" {
			return nil, nil, true
		}
		return nil, []Inline{&Styled{Style: st, Text: p.sub(p.raw())}}, true
	}
	if n, ok := ignoredCommands[name]; ok {
		p.optional()
		for i := 0; i < n; i++ {
			p.raw()
		}
		p.skipSpacesAfterWord(name)
		return nil, nil, true
	}
	switch name {
	case "\\", "newline", "linebreak*", "break":
		p.optional()
		return nil, []Inline{&Break{}}, true
	case "section", "section*", "chapter", "chapter*":
		return []Block{&Heading{Level: 2, Text: p.sub(p.raw())}}, nil, true
	case "subsection", "subsection*":
		return []Block{&Heading{Level: 3, Text: p.sub(p.raw())}}, nil, true
	case "subsubsection", "subsubsection*", "paragraph", "paragraph*":
		return []Block{&Heading{Level: 4, Text: p.sub(p.raw())}}, nil, true
	case "title":
		p.doc.Title = plainText(p.sub(p.raw()))
		return nil, nil, true
	case "author", "date":
		p.raw()
		return nil, nil, true
	case "texttt", "code", "lstinline", "path":
		return nil, []Inline{&CodeSpan{S: plainText(p.sub(p.raw()))}}, true
	case "verb", "verb*":
		if p.eof() {
			return nil, nil, true
		}
		d := p.src[p.pos]
		end := strings.IndexByte(p.src[p.pos+1:], d)
		if end < 0 {
			return nil, nil, true
		}
		s := p.src[p.pos+1 : p.pos+1+end]
		p.pos += end + 2
		return nil, []Inline{&CodeSpan{S: s}}, true
	case "url":
		u := p.raw()
		return nil, []Inline{&Link{URL: u, Text: []Inline{&Text{S: u}}}}, true
	case "href":
		u := p.raw()
		return nil, []Inline{&Link{URL: u, Text: p.sub(p.raw())}}, true
	case "footnote":
		return nil, append(append([]Inline{&Text{S: " ("}}, p.sub(p.raw())...), &Text{S: ")"}), true
	case "textsc", "textrm", "textsf", "textup", "textnormal", "text", "hbox", "makebox", "fbox", "framebox":
		p.optional()
		return nil, p.sub(p.raw()), true
	case "textcolor", "colorbox":
		p.raw()
		return nil, p.sub(p.raw()), true
	case "color":
		p.raw()
		return nil, nil, true
	case "bf", "it", "tt", "em", "sl":
		// Old style switches apply to the rest of their group: the parser
		// sees groups as arguments, so the text that follows keeps its style
		// only there; elsewhere they are ignored.
		p.skipSpaces()
		return nil, nil, true
	case "includegraphics":
		p.optional()
		src := strings.TrimSpace(p.raw())
		return []Block{&Image{Src: src}}, nil, true
	case "caption":
		return nil, p.sub(p.raw()), true
	case "item":
		// outside a list: a bullet
		p.optional()
		return nil, []Inline{&Text{S: "• "}}, true
	case "exmp", "exmpfile":
		in, out := p.raw(), p.raw()
		if name == "exmp" {
			p.doc.Examples = append(p.doc.Examples, Example{Input: exampleText(in), Output: exampleText(out)})
		}
		return nil, nil, true
	case "problem":
		// \problem{...} as a command (rare): title
		p.doc.Title = plainText(p.sub(p.raw()))
		return nil, nil, true
	case "hspace", "hspace*":
		p.raw()
		return nil, []Inline{&Text{S: " "}}, true
	case "ref", "eqref", "pageref":
		return nil, []Inline{&Text{S: p.raw()}}, true
	}
	return nil, nil, false
}

var switches = map[string]byte{
	"bf": 'b', "bfseries": 'b', "it": 'i', "itshape": 'i', "em": 'i', "sl": 'i', "slshape": 'i',
	"tt": 't', "ttfamily": 't', "sc": 0, "scshape": 0, "rm": 0, "sf": 0, "sffamily": 0, "normalfont": 0,
}

// switchGroup reads {\bf ...} (at a '{') as styled text.
func (p *texParser) switchGroup() ([]Inline, bool) {
	j := p.pos + 1
	for j < len(p.src) && (p.src[j] == ' ' || p.src[j] == '\n') {
		j++
	}
	if j >= len(p.src) || p.src[j] != '\\' {
		return nil, false
	}
	k := j + 1
	for k < len(p.src) && (p.src[k] >= 'a' && p.src[k] <= 'z') {
		k++
	}
	st, ok := switches[p.src[j+1:k]]
	if !ok {
		return nil, false
	}
	group := p.raw() // the whole {...}
	inner := strings.TrimLeft(group, " \n")[k-j:]
	in := p.sub(inner)
	switch st {
	case 'b', 'i':
		return []Inline{&Styled{Style: st, Text: in}}, true
	case 't':
		return []Inline{&CodeSpan{S: plainText(in)}}, true
	}
	return in, true
}

// skipSpacesAfterWord: a command word swallows the spaces after it.
func (p *texParser) skipSpacesAfterWord(name string) {
	if name == "" {
		return
	}
	c := name[0]
	if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
		for !p.eof() && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t') {
			p.pos++
		}
		// {} after a command word ("\LaTeX{} is") is empty.
		if strings.HasPrefix(p.src[p.pos:], "{}") {
			p.pos += 2
		}
	}
}

// exampleText cleans an example written in the source (olymp.sty keeps
// it verbatim, one line per line).
func exampleText(s string) string {
	s = strings.Trim(s, "\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.Join(lines, "\n") + "\n"
}

// environment handles \begin{name}: it returns blocks, or inlines for
// the inline environments.
func (p *texParser) environment(name string) ([]Block, []Inline) {
	star := strings.TrimSuffix(name, "*")
	switch star {
	case "itemize", "enumerate", "description":
		return []Block{p.list(name)}, nil
	case "verbatim", "lstlisting", "minted", "Verbatim", "alltt", "code":
		if star == "minted" {
			p.raw()
		}
		p.optional()
		body := p.envBody(name)
		return []Block{&Code{Text: strings.Trim(body, "\n")}}, nil
	case "equation", "displaymath", "math":
		body := p.envBody(name)
		m, w := ParseMath(body, star != "math")
		p.warn = append(p.warn, w...)
		if star == "math" {
			return nil, []Inline{&InlineMath{Math: m}}
		}
		return []Block{&Display{Math: m}}, nil
	case "align", "gather", "multline", "eqnarray", "flalign", "alignat":
		body := p.envBody(name)
		inner := "aligned"
		if star == "gather" || star == "multline" {
			inner = "gathered"
		}
		m, w := ParseMath(`\begin{`+inner+`}`+body+`\end{`+inner+`}`, true)
		p.warn = append(p.warn, w...)
		return []Block{&Display{Math: m}}, nil
	case "center", "flushleft", "flushright", "figure", "minipage", "document", "problem", "wrapfigure", "samepage", "tabularx ":
		// olymp.sty: \begin{problem}{Title}{input}{output}{time}{memory}
		if star == "problem" {
			p.doc.Title = plainText(p.sub(p.raw()))
			for i := 0; i < 4; i++ {
				p.raw()
			}
		}
		if star == "minipage" || star == "wrapfigure" {
			p.optional()
			p.raw()
		}
		if star == "figure" {
			p.optional()
		}
		return p.subBlocks(p.envBody(name)), nil
	case "quote", "quotation", "note", "remark", "verse":
		return []Block{&Quote{Blocks: p.subBlocks(p.envBody(name))}}, nil
	case "tabular", "tabularx", "tabulary", "array", "longtable":
		if star == "tabularx" {
			p.raw()
		}
		spec := p.raw()
		return []Block{p.table(spec, p.envBody(name))}, nil
	case "table":
		p.optional()
		return p.subBlocks(p.envBody(name)), nil
	case "example", "examples", "example*":
		// olymp.sty: \exmp{input}{output} entries
		p.optional()
		body := p.envBody(name)
		q := &texParser{src: body, doc: p.doc}
		q.blocks("")
		return nil, nil
	case "comment":
		p.envBody(name)
		return nil, nil
	}
	p.warnf("unsupported environment " + name)
	return p.subBlocks(p.envBody(name)), nil
}

// list reads \item entries up to \end{name}.
func (p *texParser) list(name string) *List {
	body := p.envBody(name)
	l := &List{Ordered: strings.HasPrefix(name, "enumerate"), Start: 1}
	// Split at top-level \item.
	var items []string
	depth := 0
	last := -1
	for i := 0; i < len(body); i++ {
		switch {
		case strings.HasPrefix(body[i:], `\begin{`):
			depth++
		case strings.HasPrefix(body[i:], `\end{`):
			depth--
		case depth == 0 && strings.HasPrefix(body[i:], `\item`) && !isLetterAt(body, i+5):
			if last >= 0 {
				items = append(items, body[last:i])
			}
			last = i + 5
		}
	}
	if last >= 0 {
		items = append(items, body[last:])
	}
	for _, it := range items {
		label := ""
		t := strings.TrimLeft(it, " \t\n")
		if strings.HasPrefix(t, "[") {
			if end := strings.IndexByte(t, ']'); end > 0 {
				label = t[1:end]
				t = t[end+1:]
			}
		}
		blocks := p.subBlocks(t)
		if label != "" {
			lab := &Styled{Style: 'b', Text: p.sub(label)}
			if len(blocks) > 0 {
				if para, ok := blocks[0].(*Para); ok {
					para.Text = append([]Inline{lab, &Text{S: " "}}, para.Text...)
				} else {
					blocks = append([]Block{&Para{Text: []Inline{lab}}}, blocks...)
				}
			} else {
				blocks = []Block{&Para{Text: []Inline{lab}}}
			}
		}
		l.Items = append(l.Items, blocks)
	}
	return l
}

func isLetterAt(s string, i int) bool {
	if i >= len(s) {
		return false
	}
	c := s[i]
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// table reads a tabular body: & between cells, \\ between rows.
func (p *texParser) table(spec, body string) *Table {
	t := &Table{}
	for _, c := range spec {
		switch c {
		case 'l', 'c', 'r':
			t.Align = append(t.Align, byte(c))
		case 'p', 'X', 'm', 'b':
			t.Align = append(t.Align, 'l')
		}
	}
	body = strings.NewReplacer(`\hline`, "", `\toprule`, "", `\midrule`, "", `\bottomrule`, "").Replace(body)
	for _, r := range splitTopLevel(body, `\\`) {
		if strings.TrimSpace(r) == "" {
			continue
		}
		var cells [][]Inline
		for _, c := range splitTopLevel(r, "&") {
			cells = append(cells, p.sub(strings.TrimSpace(c)))
		}
		t.Rows = append(t.Rows, cells)
	}
	return t
}

// splitTopLevel splits s at sep outside braces and math.
func splitTopLevel(s, sep string) []string {
	var out []string
	depth := 0
	math := false
	last := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			if strings.HasPrefix(s[i:], sep) && depth == 0 && !math {
				out = append(out, s[last:i])
				i += len(sep) - 1
				last = i + 1
				continue
			}
			i++
			continue
		case '{':
			depth++
		case '}':
			depth--
		case '$':
			math = !math
		}
		if depth == 0 && !math && strings.HasPrefix(s[i:], sep) {
			out = append(out, s[last:i])
			i += len(sep) - 1
			last = i + 1
		}
	}
	return append(out, s[last:])
}

// trimInlines removes the spaces at both ends of a paragraph.
func trimInlines(in []Inline) []Inline {
	for len(in) > 0 {
		if t, ok := in[0].(*Text); ok {
			t.S = strings.TrimLeft(t.S, " \n\t")
			if t.S == "" {
				in = in[1:]
				continue
			}
		}
		break
	}
	for len(in) > 0 {
		if t, ok := in[len(in)-1].(*Text); ok {
			t.S = strings.TrimRight(t.S, " \n\t")
			if t.S == "" {
				in = in[:len(in)-1]
				continue
			}
		}
		if _, ok := in[len(in)-1].(*Break); ok {
			in = in[:len(in)-1]
			continue
		}
		break
	}
	// collapse runs of spaces inside text
	for _, i := range in {
		if t, ok := i.(*Text); ok {
			for strings.Contains(t.S, "  ") {
				t.S = strings.ReplaceAll(t.S, "  ", " ")
			}
		}
	}
	return in
}
