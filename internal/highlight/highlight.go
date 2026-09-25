// Package highlight marks up source code as HTML for the admin's source
// view: comments, strings, numbers, keywords and preprocessor lines, one
// element per line (numbered with CSS). It is a tokenizer, not a parser:
// good enough to read code, linear in the input, and it always escapes.
// Languages are chosen by file extension, so a new language configured
// with a known extension is highlighted without code.
package highlight

import (
	"html/template"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Syntax describes the lexical conventions of a language family.
type Syntax struct {
	keywords        map[string]bool
	lineComments    []string
	blockComments   [][2]string
	quotes          string // single-line string delimiters
	rawQuotes       string // delimiters of strings that may span lines
	tripleQuotes    bool   // Python """ and '''
	preprocessor    bool   // '#' directives at the start of a line
	caseInsensitive bool
	charQuote       bool // ' starts a character literal only when short (Rust/Haskell lifetimes, primes)
	// starts marks the bytes that may begin a token other than plain text.
	starts [256]bool
}

func words(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

var (
	cLike = "auto break case char const continue default do double else enum extern float for goto if inline int long " +
		"register restrict return short signed sizeof static struct switch typedef union unsigned void volatile while bool true false NULL"
	cpp = cLike + " alignas alignof and asm catch class constexpr consteval constinit co_await co_return co_yield concept decltype delete " +
		"dynamic_cast explicit export friend mutable namespace new noexcept not nullptr operator or private protected public " +
		"reinterpret_cast requires static_assert static_cast template this thread_local throw try typeid typename using virtual xor " +
		"std string vector map set pair size_t int64_t uint64_t"
	java = "abstract assert boolean break byte case catch char class const continue default do double else enum extends final " +
		"finally float for goto if implements import instanceof int interface long native new package private protected public " +
		"return short static strictfp super switch synchronized this throw throws transient try void volatile while var record " +
		"true false null String"
	kotlin = "as break class continue do else false for fun if in interface is null object package return super this throw true " +
		"try typealias typeof val var when while by catch constructor finally get import init internal lateinit open override " +
		"private protected public sealed set data enum companion inline suspend Int Long String Boolean Double"
	csharp = "abstract as base bool break byte case catch char checked class const continue decimal default delegate do double " +
		"else enum event explicit extern false finally fixed float for foreach goto if implicit in int interface internal is lock " +
		"long namespace new null object operator out override params private protected public readonly ref return sbyte sealed " +
		"short sizeof stackalloc static string struct switch this throw true try typeof uint ulong unchecked unsafe ushort using " +
		"var virtual void volatile while"
	golang = "break case chan const continue default defer else fallthrough for func go goto if import interface map package range " +
		"return select struct switch type var true false nil int int64 string byte rune bool float64 error make len append"
	rust = "as async await break const continue crate dyn else enum extern false fn for if impl in let loop match mod move mut pub " +
		"ref return self Self static struct super trait true type unsafe use where while i32 i64 u32 u64 usize f64 bool String Vec"
	python = "False None True and as assert async await break class continue def del elif else except finally for from global if " +
		"import in is lambda nonlocal not or pass raise return try while with yield print range len int str list dict set"
	pascal = "and array begin case const div do downto else end file for function goto if in label mod nil not of or packed " +
		"procedure program record repeat set then to type until var while with uses integer longint int64 boolean char string " +
		"real readln writeln read write true false"
	haskell = "case class data default deriving do else foreign if import in infix infixl infixr instance let module newtype of " +
		"then type where Int Integer String Bool True False IO Maybe"
)

var syntaxes = map[string]*Syntax{}

func register(s *Syntax, exts ...string) {
	for c := 0; c < 256; c++ {
		s.starts[c] = isIdentStart(byte(c)) || isDigit(byte(c)) || c == '.' || c == '#' && s.preprocessor
	}
	for _, q := range s.quotes + s.rawQuotes {
		s.starts[q] = true
	}
	for _, lc := range s.lineComments {
		s.starts[lc[0]] = true
	}
	for _, bc := range s.blockComments {
		s.starts[bc[0][0]] = true
	}
	if s.tripleQuotes || s.charQuote {
		s.starts['"'], s.starts['\''] = true, true
	}
	for _, e := range exts {
		syntaxes[e] = s
	}
}

func init() {
	cBlock := [][2]string{{"/*", "*/"}}
	register(&Syntax{keywords: words(cLike), lineComments: []string{"//"}, blockComments: cBlock, quotes: `"'`, preprocessor: true}, ".c", ".h")
	register(&Syntax{keywords: words(cpp), lineComments: []string{"//"}, blockComments: cBlock, quotes: `"'`, preprocessor: true},
		".cpp", ".cc", ".cxx", ".c++", ".hpp", ".hh", ".hxx")
	register(&Syntax{keywords: words(java), lineComments: []string{"//"}, blockComments: cBlock, quotes: `"'`}, ".java")
	register(&Syntax{keywords: words(kotlin), lineComments: []string{"//"}, blockComments: cBlock, quotes: `"'`}, ".kt", ".kts")
	register(&Syntax{keywords: words(csharp), lineComments: []string{"//"}, blockComments: cBlock, quotes: `"'`, preprocessor: true}, ".cs")
	register(&Syntax{keywords: words(golang), lineComments: []string{"//"}, blockComments: cBlock, quotes: `"'`, rawQuotes: "`"}, ".go")
	register(&Syntax{keywords: words(rust), lineComments: []string{"//"}, blockComments: cBlock, quotes: `"`, charQuote: true}, ".rs")
	register(&Syntax{keywords: words(python), lineComments: []string{"#"}, quotes: `"'`, tripleQuotes: true}, ".py")
	register(&Syntax{keywords: words(pascal), lineComments: []string{"//"}, blockComments: [][2]string{{"{", "}"}, {"(*", "*)"}},
		quotes: "'", caseInsensitive: true}, ".pas", ".pp", ".dpr")
	register(&Syntax{keywords: words(haskell), lineComments: []string{"--"}, blockComments: [][2]string{{"{-", "-}"}}, quotes: `"`, charQuote: true}, ".hs")
	register(&Syntax{keywords: words("function var let const if else for while return new class this null true false undefined"),
		lineComments: []string{"//"}, blockComments: cBlock, quotes: `"'`, rawQuotes: "`"}, ".js", ".mjs")
}

// ForFile returns the syntax of a file name, or nil (plain text).
func ForFile(name string) *Syntax { return syntaxes[strings.ToLower(filepath.Ext(name))] }

// HTML returns src as highlighted HTML lines ("<span class=l>…</span>\n"
// each), to be placed inside <pre class="code hl">. With a nil syntax the
// lines are only escaped.
func HTML(src string, s *Syntax) template.HTML {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	w := &writer{}
	w.b.Grow(len(src)*2 + 64)
	w.b.WriteString(`<span class="l">`)
	if s == nil {
		w.write("", src)
	} else {
		s.tokenize(src, w.write)
	}
	w.b.WriteString("</span>")
	return template.HTML(w.b.String())
}

// HTMLMarked is HTML with the lines in marked (0-based) given the class
// "m" as well (matched code in the plagiarism view).
func HTMLMarked(src string, s *Syntax, marked map[int]bool) template.HTML {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	w := &writer{marked: marked}
	w.b.Grow(len(src)*2 + 64)
	w.open()
	if s == nil {
		w.write("", src)
	} else {
		s.tokenize(src, w.write)
	}
	w.b.WriteString("</span>")
	return template.HTML(w.b.String())
}

// Token kinds reported by Tokens.
const (
	Keyword      = 'k'
	Identifier   = 'i'
	Number       = 'n'
	String       = 's'
	Comment      = 'c'
	Preprocessor = 'p'
	Operator     = 'o'
)

// Tokens calls fn with every token of src in order, whitespace left out:
// its kind, its text (keywords lower-cased in case-insensitive languages)
// and its 0-based line. Every other byte is an operator token of its own
// (a whole rune for non-ASCII text).
func Tokens(src string, s *Syntax, fn func(kind byte, text string, line int)) {
	line := 0
	s.tokenize(src, func(class, text string) {
		if class != "" {
			if class == "k" && s.caseInsensitive {
				fn(Keyword, strings.ToLower(text), line)
			} else {
				fn(class[0], text, line)
			}
			line += strings.Count(text, "\n")
			return
		}
		for i := 0; i < len(text); {
			c := text[i]
			switch {
			case c == '\n':
				line++
				i++
			case c == ' ' || c == '\t' || c == '\r' || c == '\f' || c == '\v':
				i++
			case isIdentStart(c):
				j := i + 1
				for j < len(text) && isIdent(text[j]) {
					j++
				}
				fn(Identifier, text[i:j], line)
				i = j
			default:
				_, n := utf8.DecodeRuneInString(text[i:])
				fn(Operator, text[i:i+n], line)
				i += n
			}
		}
	})
}

// writer emits tokens, closing and reopening their span at line breaks so
// that every line element is well formed.
type writer struct {
	b      strings.Builder
	marked map[int]bool
	line   int
}

func (w *writer) open() {
	if w.marked[w.line] {
		w.b.WriteString(`<span class="l m">`)
	} else {
		w.b.WriteString(`<span class="l">`)
	}
}

func (w *writer) write(class, text string) {
	for {
		nl := strings.IndexByte(text, '\n')
		part := text
		if nl >= 0 {
			part = text[:nl]
		}
		if part != "" {
			if class != "" {
				w.b.WriteString(`<span class="`)
				w.b.WriteString(class)
				w.b.WriteString(`">`)
			}
			escape(&w.b, part)
			if class != "" {
				w.b.WriteString("</span>")
			}
		}
		if nl < 0 {
			return
		}
		w.b.WriteString("</span>\n")
		w.line++
		w.open()
		text = text[nl+1:]
	}
}

// escape writes s HTML-escaped (as html.EscapeString, without allocating).
func escape(b *strings.Builder, s string) {
	last := 0
	for i := 0; i < len(s); i++ {
		var rep string
		switch s[i] {
		case '<':
			rep = "&lt;"
		case '>':
			rep = "&gt;"
		case '&':
			rep = "&amp;"
		case '"':
			rep = "&#34;"
		case '\'':
			rep = "&#39;"
		default:
			continue
		}
		b.WriteString(s[last:i])
		b.WriteString(rep)
		last = i + 1
	}
	b.WriteString(s[last:])
}

func isIdentStart(c byte) bool { return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isIdent(c byte) bool      { return isIdentStart(c) || c >= '0' && c <= '9' }
func isDigit(c byte) bool      { return c >= '0' && c <= '9' }

// tokenize calls out with consecutive pieces of src and their class.
func (s *Syntax) tokenize(src string, out func(class, text string)) {
	plain := 0 // start of the pending plain run
	emit := func(start, end int, class string) {
		if plain < start {
			out("", src[plain:start])
		}
		out(class, src[start:end])
		plain = end
	}
	lineStart := true
	for i := 0; i < len(src); {
		c := src[i]
		if c == '\n' {
			lineStart = true
			i++
			continue
		}
		if c == ' ' || c == '\t' {
			i++
			continue
		}
		if !s.starts[c] {
			lineStart = false
			i++
			continue
		}
		atStart := lineStart
		lineStart = false
		if s.preprocessor && atStart && c == '#' {
			end := indexFrom(src, i, "\n")
			emit(i, end, "p")
			i = end
			continue
		}
		if end, ok := s.comment(src, i); ok {
			emit(i, end, "c")
			i = end
			continue
		}
		if end, ok := s.stringAt(src, i); ok {
			emit(i, end, "s")
			i = end
			continue
		}
		switch {
		case isDigit(c) || c == '.' && i+1 < len(src) && isDigit(src[i+1]):
			j := i + 1
			for j < len(src) && (isIdent(src[j]) || src[j] == '.' || src[j] == '\'' && s.preprocessor && j+1 < len(src) && isDigit(src[j+1])) {
				j++
			}
			emit(i, j, "n")
			i = j
		case isIdentStart(c):
			j := i + 1
			for j < len(src) && isIdent(src[j]) {
				j++
			}
			w := src[i:j]
			if s.caseInsensitive {
				w = strings.ToLower(w)
			}
			if s.keywords[w] {
				emit(i, j, "k")
			}
			i = j
		default:
			i++
		}
	}
	if plain < len(src) {
		out("", src[plain:])
	}
}

// comment returns the end of a comment starting at i.
func (s *Syntax) comment(src string, i int) (int, bool) {
	for _, lc := range s.lineComments {
		if strings.HasPrefix(src[i:], lc) {
			return indexFrom(src, i, "\n"), true
		}
	}
	for _, bc := range s.blockComments {
		if strings.HasPrefix(src[i:], bc[0]) {
			if k := strings.Index(src[i+len(bc[0]):], bc[1]); k >= 0 {
				return i + len(bc[0]) + k + len(bc[1]), true
			}
			return len(src), true
		}
	}
	return 0, false
}

// stringAt returns the end of a string literal starting at i.
func (s *Syntax) stringAt(src string, i int) (int, bool) {
	c := src[i]
	if s.tripleQuotes && (strings.HasPrefix(src[i:], `"""`) || strings.HasPrefix(src[i:], `'''`)) {
		q := src[i : i+3]
		if k := strings.Index(src[i+3:], q); k >= 0 {
			return i + 3 + k + 3, true
		}
		return len(src), true
	}
	if strings.IndexByte(s.rawQuotes, c) >= 0 {
		if k := strings.IndexByte(src[i+1:], c); k >= 0 {
			return i + 1 + k + 1, true
		}
		return len(src), true
	}
	if s.charQuote && c == '\'' {
		// 'a', '\n', '\u{1F600}': a character; otherwise (lifetimes,
		// primes) not a string.
		if i+1 < len(src) && src[i+1] == '\\' {
			if k := strings.IndexByte(src[i+2:min(len(src), i+14)], '\''); k >= 0 {
				return i + 2 + k + 1, true
			}
			return 0, false
		}
		if _, n := utf8.DecodeRuneInString(src[i+1:]); n > 0 && i+1+n < len(src) && src[i+1+n] == '\'' && src[i+1] != '\n' {
			return i + 1 + n + 1, true
		}
		return 0, false
	}
	if strings.IndexByte(s.quotes, c) < 0 {
		return 0, false
	}
	for j := i + 1; j < len(src); j++ {
		switch src[j] {
		case '\\':
			if !s.caseInsensitive { // Pascal has no escapes
				j++
			}
		case c:
			return j + 1, true
		case '\n':
			return j, true
		}
	}
	return len(src), true
}

func indexFrom(s string, i int, sub string) int {
	if k := strings.Index(s[i:], sub); k >= 0 {
		return i + k
	}
	return len(s)
}
