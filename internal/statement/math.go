package statement

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// MKind is the kind of a math node.
type MKind int

// Math node kinds.
const (
	MRow     MKind = iota // Kids in sequence
	MIdent                // a variable or a function name (Text, Variant)
	MNum                  // a number
	MOp                   // an operator, relation, delimiter or punctuation
	MText                 // text inside math (\text{...})
	MFrac                 // Kids[0] over Kids[1]; NoRule for binomials
	MSqrt                 // Kids[0], optional index Kids[1]
	MScripts              // Kids[0] base, Kids[1] subscript, Kids[2] superscript (nil when absent)
	MFenced               // Open Kids[0] Close, delimiters as tall as the body
	MSpace                // horizontal space of Em
	MTable                // Rows of cells (cases, matrices, aligned equations)
	MAccent               // Kids[0] with Text above (or below with Under)
	MError                // something that could not be read (Text)
)

// OpClass decides the spacing around an operator (as TeX does).
type OpClass int

// Operator classes.
const (
	ClassOrd OpClass = iota
	ClassBin
	ClassRel
	ClassOpen
	ClassClose
	ClassPunct
	ClassLarge // sum, product, integral, lim, max...
)

// MNode is a node of a formula.
type MNode struct {
	Kind    MKind
	Text    string
	Variant string // "" (default), "normal", "bold", "bb", "cal", "frak", "tt", "sf"
	Class   OpClass
	Limits  bool    // MScripts: under/over the base (display limits)
	Size    int     // MOp: \big (1) to \Bigg (4); MFenced: stretch
	NoRule  bool    // MFrac: binomial
	Under   bool    // MAccent: below
	Em      float64 // MSpace
	Open    string  // MFenced
	Close   string
	Kids    []*MNode
	Rows    [][]*MNode // MTable
	Align   string     // MTable: one of l, c, r per column
}

// Math is a parsed formula.
type Math struct {
	TeX     string
	Display bool
	Root    *MNode
}

// ParseMath reads a TeX formula. Unknown commands become MError nodes (and
// warnings); the parse never fails.
func ParseMath(tex string, display bool) (*Math, []string) {
	p := &mparser{src: tex}
	root := &MNode{Kind: MRow, Kids: p.list(nil)}
	return &Math{TeX: tex, Display: display, Root: root}, p.warn
}

type mparser struct {
	src  string
	pos  int
	warn []string
}

func (p *mparser) eof() bool { return p.pos >= len(p.src) }

func (p *mparser) peek() rune {
	if p.eof() {
		return 0
	}
	r, _ := utf8.DecodeRuneInString(p.src[p.pos:])
	return r
}

func (p *mparser) next() rune {
	r, n := utf8.DecodeRuneInString(p.src[p.pos:])
	p.pos += n
	return r
}

func (p *mparser) skipSpace() {
	for !p.eof() {
		r := p.peek()
		if r == '%' { // comment to the end of the line
			for !p.eof() && p.peek() != '\n' {
				p.next()
			}
			continue
		}
		if !unicode.IsSpace(r) {
			return
		}
		p.next()
	}
}

// command reads the name after a backslash.
func (p *mparser) command() string {
	if p.eof() {
		return ""
	}
	r := p.next()
	if !isLetter(r) {
		return string(r)
	}
	start := p.pos - utf8.RuneLen(r)
	for !p.eof() && isLetter(p.peek()) {
		p.next()
	}
	return p.src[start:p.pos]
}

func isLetter(r rune) bool { return r < 128 && unicode.IsLetter(r) }

// stop tells list where to end: at a closing brace, at \right, at \end, at
// & or \\ (table cells), or at a given command.
type stop struct {
	brace, right, end, cell bool
}

// list parses nodes until the end of input or a stop token (not consumed,
// except the closing brace).
func (p *mparser) list(st *stop) []*MNode {
	var out []*MNode
	for {
		p.skipSpace()
		if p.eof() {
			return out
		}
		r := p.peek()
		if st != nil {
			if r == '}' && st.brace {
				p.next()
				return out
			}
			if r == '&' && st.cell {
				return out
			}
			if r == '\\' {
				save := p.pos
				p.next()
				name := p.command()
				p.pos = save
				if (name == "right" && st.right) || (name == "end" && st.end) || (name == "\\" && st.cell) || (name == "cr" && st.cell) {
					return out
				}
			}
		}
		if r == '}' { // unbalanced: skip
			p.next()
			continue
		}
		if r == '^' || r == '_' {
			p.next()
			arg := p.argument()
			var base *MNode
			if n := len(out); n > 0 {
				base = out[n-1]
				out = out[:n-1]
			} else {
				base = &MNode{Kind: MRow}
			}
			out = append(out, attach(base, r == '^', arg))
			continue
		}
		if r == '\'' {
			p.next()
			primes := "′"
			for p.peek() == '\'' {
				p.next()
				primes += "′"
			}
			prime := &MNode{Kind: MOp, Text: primes, Class: ClassOrd}
			var base *MNode
			if n := len(out); n > 0 {
				base = out[n-1]
				out = out[:n-1]
			} else {
				base = &MNode{Kind: MRow}
			}
			out = append(out, attach(base, true, prime))
			continue
		}
		if n := p.atom(st); n != nil {
			out = append(out, n)
		}
	}
}

// attach adds a subscript or superscript to base.
func attach(base *MNode, sup bool, arg *MNode) *MNode {
	if base.Kind != MScripts {
		base = &MNode{Kind: MScripts, Kids: []*MNode{base, nil, nil}, Limits: base.Kind == MOp && base.Class == ClassLarge && base.Limits}
	}
	i := 1
	if sup {
		i = 2
	}
	if base.Kids[i] != nil { // x^a^b: TeX refuses; keep the first
		return base
	}
	base.Kids[i] = arg
	return base
}

// argument reads one argument: a braced group or a single token.
func (p *mparser) argument() *MNode {
	p.skipSpace()
	if p.eof() {
		return &MNode{Kind: MRow}
	}
	if p.peek() == '{' {
		p.next()
		return row(p.list(&stop{brace: true}))
	}
	n := p.atom(nil)
	if n == nil {
		return &MNode{Kind: MRow}
	}
	return n
}

// rawArgument reads a braced group as text (for \text, \begin).
func (p *mparser) rawArgument() string {
	p.skipSpace()
	if p.peek() != '{' {
		if p.eof() {
			return ""
		}
		return string(p.next())
	}
	p.next()
	depth := 1
	start := p.pos
	for !p.eof() {
		switch p.next() {
		case '\\':
			if !p.eof() {
				p.next()
			}
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return p.src[start : p.pos-1]
			}
		}
	}
	return p.src[start:]
}

// optional reads [..] if present.
func (p *mparser) optional() (*MNode, bool) {
	p.skipSpace()
	if p.peek() != '[' {
		return nil, false
	}
	p.next()
	start := p.pos
	depth := 0
	for !p.eof() {
		r := p.next()
		if r == '{' {
			depth++
		} else if r == '}' {
			depth--
		} else if r == ']' && depth == 0 {
			sub := &mparser{src: p.src[start : p.pos-1]}
			n := row(sub.list(nil))
			p.warn = append(p.warn, sub.warn...)
			return n, true
		}
	}
	return nil, false
}

func row(kids []*MNode) *MNode {
	if len(kids) == 1 {
		return kids[0]
	}
	return &MNode{Kind: MRow, Kids: kids}
}

// atom reads one element (not a script).
func (p *mparser) atom(st *stop) *MNode {
	r := p.next()
	switch {
	case r == '{':
		return row(p.list(&stop{brace: true}))
	case r == '\\':
		return p.commandNode(p.command(), st)
	case r >= '0' && r <= '9' || r == '.' && p.peek() >= '0' && p.peek() <= '9':
		start := p.pos - 1
		for !p.eof() {
			c := p.peek()
			if c >= '0' && c <= '9' {
				p.next()
				continue
			}
			// A decimal point followed by a digit belongs to the number.
			if c == '.' && p.pos+1 < len(p.src) && p.src[p.pos+1] >= '0' && p.src[p.pos+1] <= '9' {
				p.next()
				continue
			}
			break
		}
		return &MNode{Kind: MNum, Text: p.src[start:p.pos]}
	case unicode.IsLetter(r):
		if s, ok := greekFromRune(r); ok {
			return &MNode{Kind: MIdent, Text: s}
		}
		return &MNode{Kind: MIdent, Text: string(r)}
	case r == '~':
		return &MNode{Kind: MSpace, Em: 0.33}
	case r == '&' || r == '#':
		return nil
	}
	if cl, ok := asciiOps[r]; ok {
		t := string(r)
		if r == '-' {
			t = "−"
		} else if r == '*' {
			t = "∗"
		}
		return &MNode{Kind: MOp, Text: t, Class: cl}
	}
	if cl, ok := unicodeClass(r); ok {
		return &MNode{Kind: MOp, Text: string(r), Class: cl}
	}
	return &MNode{Kind: MIdent, Text: string(r), Variant: "normal"}
}

var asciiOps = map[rune]OpClass{
	'+': ClassBin, '-': ClassBin, '*': ClassBin, '/': ClassOrd, '=': ClassRel, '<': ClassRel, '>': ClassRel,
	',': ClassPunct, ';': ClassPunct, ':': ClassRel, '!': ClassClose, '?': ClassClose,
	'(': ClassOpen, '[': ClassOpen, ')': ClassClose, ']': ClassClose, '|': ClassOrd,
}

// unicodeClass classifies operators typed directly (≤, ×, →...).
func unicodeClass(r rune) (OpClass, bool) {
	for _, s := range symbols {
		if s.text == string(r) {
			return s.class, true
		}
	}
	return 0, false
}

func greekFromRune(r rune) (string, bool) {
	if (r >= 'α' && r <= 'ω') || (r >= 'Α' && r <= 'Ω') {
		return string(r), true
	}
	return "", false
}

type symbol struct {
	text  string
	class OpClass
}

// symbols are the commands that stand for one character.
var symbols = map[string]symbol{
	// relations
	"le": {"≤", ClassRel}, "leq": {"≤", ClassRel}, "leqslant": {"≤", ClassRel}, "ge": {"≥", ClassRel},
	"geq": {"≥", ClassRel}, "geqslant": {"≥", ClassRel}, "ne": {"≠", ClassRel}, "neq": {"≠", ClassRel},
	"approx": {"≈", ClassRel}, "equiv": {"≡", ClassRel}, "sim": {"∼", ClassRel}, "simeq": {"≃", ClassRel},
	"cong": {"≅", ClassRel}, "propto": {"∝", ClassRel}, "in": {"∈", ClassRel}, "notin": {"∉", ClassRel},
	"ni": {"∋", ClassRel}, "subset": {"⊂", ClassRel}, "subseteq": {"⊆", ClassRel}, "supset": {"⊃", ClassRel},
	"supseteq": {"⊇", ClassRel}, "ll": {"≪", ClassRel}, "gg": {"≫", ClassRel}, "mid": {"∣", ClassRel},
	"parallel": {"∥", ClassRel}, "perp": {"⊥", ClassRel}, "to": {"→", ClassRel}, "rightarrow": {"→", ClassRel},
	"leftarrow": {"←", ClassRel}, "gets": {"←", ClassRel}, "leftrightarrow": {"↔", ClassRel},
	"Rightarrow": {"⇒", ClassRel}, "Leftarrow": {"⇐", ClassRel}, "Leftrightarrow": {"⇔", ClassRel},
	"implies": {"⇒", ClassRel}, "iff": {"⇔", ClassRel}, "mapsto": {"↦", ClassRel}, "uparrow": {"↑", ClassRel},
	"downarrow": {"↓", ClassRel}, "longrightarrow": {"⟶", ClassRel}, "prec": {"≺", ClassRel}, "succ": {"≻", ClassRel},
	"preceq": {"⪯", ClassRel}, "succeq": {"⪰", ClassRel}, "nleq": {"≰", ClassRel}, "ngeq": {"≱", ClassRel},
	"lessdot": {"⋖", ClassRel}, "coloneqq": {"≔", ClassRel}, "vdash": {"⊢", ClassRel}, "models": {"⊨", ClassRel},
	// binary operators
	"cdot": {"⋅", ClassBin}, "times": {"×", ClassBin}, "div": {"÷", ClassBin}, "pm": {"±", ClassBin},
	"mp": {"∓", ClassBin}, "cup": {"∪", ClassBin}, "cap": {"∩", ClassBin}, "setminus": {"∖", ClassBin},
	"wedge": {"∧", ClassBin}, "land": {"∧", ClassBin}, "vee": {"∨", ClassBin}, "lor": {"∨", ClassBin},
	"oplus": {"⊕", ClassBin}, "otimes": {"⊗", ClassBin}, "circ": {"∘", ClassBin}, "bullet": {"∙", ClassBin},
	"ast": {"∗", ClassBin}, "star": {"⋆", ClassBin}, "bmod": {"mod", ClassBin}, "oslash": {"⊘", ClassBin},
	"sqcup": {"⊔", ClassBin}, "sqcap": {"⊓", ClassBin}, "odot": {"⊙", ClassBin}, "triangleleft": {"◁", ClassBin},
	// ordinary symbols
	"infty": {"∞", ClassOrd}, "emptyset": {"∅", ClassOrd}, "varnothing": {"∅", ClassOrd}, "forall": {"∀", ClassOrd},
	"exists": {"∃", ClassOrd}, "nexists": {"∄", ClassOrd}, "neg": {"¬", ClassOrd}, "lnot": {"¬", ClassOrd},
	"partial": {"∂", ClassOrd}, "nabla": {"∇", ClassOrd}, "angle": {"∠", ClassOrd}, "triangle": {"△", ClassOrd},
	"prime": {"′", ClassOrd}, "ldots": {"…", ClassOrd}, "dots": {"…", ClassOrd}, "dotsc": {"…", ClassOrd},
	"dotsb": {"⋯", ClassOrd}, "cdots": {"⋯", ClassOrd}, "vdots": {"⋮", ClassOrd}, "ddots": {"⋱", ClassOrd},
	"aleph": {"ℵ", ClassOrd}, "ell": {"ℓ", ClassOrd}, "hbar": {"ℏ", ClassOrd}, "Re": {"ℜ", ClassOrd},
	"Im": {"ℑ", ClassOrd}, "wp": {"℘", ClassOrd}, "top": {"⊤", ClassOrd}, "bot": {"⊥", ClassOrd},
	"clubsuit": {"♣", ClassOrd}, "diamondsuit": {"♦", ClassOrd}, "heartsuit": {"♥", ClassOrd},
	"spadesuit": {"♠", ClassOrd}, "checkmark": {"✓", ClassOrd}, "degree": {"°", ClassOrd},
	"#": {"#", ClassOrd}, "%": {"%", ClassOrd}, "&": {"&", ClassOrd}, "$": {"$", ClassOrd}, "_": {"_", ClassOrd},
	"backslash": {"\\", ClassOrd}, "vert": {"|", ClassOrd}, "|": {"‖", ClassOrd}, "Vert": {"‖", ClassOrd},
	"lvert": {"|", ClassOpen}, "rvert": {"|", ClassClose}, "lVert": {"‖", ClassOpen}, "rVert": {"‖", ClassClose},
	// delimiters
	"{": {"{", ClassOpen}, "}": {"}", ClassClose}, "lbrace": {"{", ClassOpen}, "rbrace": {"}", ClassClose},
	"langle": {"⟨", ClassOpen}, "rangle": {"⟩", ClassClose}, "lfloor": {"⌊", ClassOpen}, "rfloor": {"⌋", ClassClose},
	"lceil": {"⌈", ClassOpen}, "rceil": {"⌉", ClassClose}, "lbrack": {"[", ClassOpen}, "rbrack": {"]", ClassClose},
	// punctuation
	"colon": {":", ClassPunct},
}

// largeOps are the operators with limits (∑, ∏, ∫...).
var largeOps = map[string]string{
	"sum": "∑", "prod": "∏", "coprod": "∐", "int": "∫", "iint": "∬", "oint": "∮", "bigcup": "⋃",
	"bigcap": "⋂", "bigoplus": "⨁", "bigotimes": "⨂", "bigvee": "⋁", "bigwedge": "⋀", "bigsqcup": "⨆",
}

// functions are named operators written upright; the ones with limits
// take their scripts below and above in display formulas.
var functions = map[string]bool{
	"log": false, "ln": false, "lg": false, "exp": false, "sin": false, "cos": false, "tan": false, "cot": false,
	"sec": false, "csc": false, "arcsin": false, "arccos": false, "arctan": false, "sinh": false, "cosh": false,
	"tanh": false, "deg": false, "dim": false, "ker": false, "arg": false, "hom": false, "det": true, "gcd": true,
	"lim": true, "limsup": true, "liminf": true, "max": true, "min": true, "sup": true, "inf": true, "Pr": true,
	"argmax": true, "argmin": true, "lcm": false,
}

var greek = map[string]string{
	"alpha": "α", "beta": "β", "gamma": "γ", "delta": "δ", "epsilon": "ϵ", "varepsilon": "ε", "zeta": "ζ",
	"eta": "η", "theta": "θ", "vartheta": "ϑ", "iota": "ι", "kappa": "κ", "lambda": "λ", "mu": "μ", "nu": "ν",
	"xi": "ξ", "omicron": "ο", "pi": "π", "varpi": "ϖ", "rho": "ρ", "varrho": "ϱ", "sigma": "σ", "varsigma": "ς",
	"tau": "τ", "upsilon": "υ", "phi": "ϕ", "varphi": "φ", "chi": "χ", "psi": "ψ", "omega": "ω",
	"Gamma": "Γ", "Delta": "Δ", "Theta": "Θ", "Lambda": "Λ", "Xi": "Ξ", "Pi": "Π", "Sigma": "Σ",
	"Upsilon": "Υ", "Phi": "Φ", "Psi": "Ψ", "Omega": "Ω",
}

var accents = map[string]struct {
	char  string
	under bool
}{
	"bar": {"¯", false}, "overline": {"¯", false}, "hat": {"^", false}, "widehat": {"^", false},
	"tilde": {"~", false}, "widetilde": {"~", false}, "vec": {"→", false}, "overrightarrow": {"→", false},
	"dot": {"˙", false}, "ddot": {"¨", false}, "underline": {"_", true}, "check": {"ˇ", false},
	"breve": {"˘", false}, "acute": {"´", false}, "grave": {"`", false},
}

var variants = map[string]string{
	"mathrm": "normal", "mathbf": "bold", "boldsymbol": "bold", "bm": "bold", "mathit": "", "mathsf": "sf",
	"mathtt": "tt", "mathcal": "cal", "mathscr": "cal", "mathbb": "bb", "mathfrak": "frak", "mathnormal": "",
}

var spaces = map[string]float64{
	",": 3.0 / 18, "thinspace": 3.0 / 18, ":": 4.0 / 18, ">": 4.0 / 18, "medspace": 4.0 / 18, ";": 5.0 / 18,
	"thickspace": 5.0 / 18, "!": -3.0 / 18, "negthinspace": -3.0 / 18, " ": 0.33, "quad": 1, "qquad": 2, "enspace": 0.5,
}

var bigSizes = map[string]int{
	"big": 1, "bigl": 1, "bigr": 1, "bigm": 1, "Big": 2, "Bigl": 2, "Bigr": 2, "Bigm": 2,
	"bigg": 3, "biggl": 3, "biggr": 3, "biggm": 3, "Bigg": 4, "Biggl": 4, "Biggr": 4, "Biggm": 4,
}

// commandNode reads what follows a command.
func (p *mparser) commandNode(name string, st *stop) *MNode {
	if name == "" {
		return nil
	}
	if s, ok := symbols[name]; ok {
		if name == "bmod" {
			return &MNode{Kind: MOp, Text: "mod", Class: ClassBin, Variant: "word"}
		}
		return &MNode{Kind: MOp, Text: s.text, Class: s.class}
	}
	if g, ok := greek[name]; ok {
		return &MNode{Kind: MIdent, Text: g}
	}
	if op, ok := largeOps[name]; ok {
		return &MNode{Kind: MOp, Text: op, Class: ClassLarge, Limits: !strings.Contains(name, "int")}
	}
	if lim, ok := functions[name]; ok {
		t := name
		switch name {
		case "argmax":
			t = "arg max"
		case "argmin":
			t = "arg min"
		case "limsup":
			t = "lim sup"
		case "liminf":
			t = "lim inf"
		}
		return &MNode{Kind: MOp, Text: t, Class: ClassLarge, Limits: lim, Variant: "word"}
	}
	if em, ok := spaces[name]; ok {
		return &MNode{Kind: MSpace, Em: em}
	}
	if a, ok := accents[name]; ok {
		return &MNode{Kind: MAccent, Text: a.char, Under: a.under, Kids: []*MNode{p.argument()}}
	}
	if v, ok := variants[name]; ok {
		body := p.argument()
		setVariant(body, v)
		return body
	}
	if size, ok := bigSizes[name]; ok {
		p.skipSpace()
		d := p.delimiter()
		if d == "" {
			return nil
		}
		cl := ClassOrd
		switch {
		case strings.HasSuffix(name, "l"):
			cl = ClassOpen
		case strings.HasSuffix(name, "r"):
			cl = ClassClose
		case strings.HasSuffix(name, "m"):
			cl = ClassRel
		}
		return &MNode{Kind: MOp, Text: d, Class: cl, Size: size}
	}
	switch name {
	case "frac", "dfrac", "tfrac", "cfrac":
		num := p.argument()
		return &MNode{Kind: MFrac, Kids: []*MNode{num, p.argument()}}
	case "binom", "dbinom", "tbinom":
		n := p.argument()
		k := p.argument()
		return &MNode{Kind: MFenced, Open: "(", Close: ")", Kids: []*MNode{{Kind: MFrac, NoRule: true, Kids: []*MNode{n, k}}}}
	case "sqrt":
		idx, ok := p.optional()
		body := p.argument()
		if ok {
			return &MNode{Kind: MSqrt, Kids: []*MNode{body, idx}}
		}
		return &MNode{Kind: MSqrt, Kids: []*MNode{body}}
	case "text", "textrm", "mbox", "textnormal", "hbox", "textup":
		return &MNode{Kind: MText, Text: textArg(p.rawArgument())}
	case "textbf":
		return &MNode{Kind: MText, Text: textArg(p.rawArgument()), Variant: "bold"}
	case "textit", "emph", "textsl":
		return &MNode{Kind: MText, Text: textArg(p.rawArgument()), Variant: "italic"}
	case "texttt":
		return &MNode{Kind: MText, Text: textArg(p.rawArgument()), Variant: "tt"}
	case "operatorname", "operatorname*":
		return &MNode{Kind: MOp, Text: textArg(p.rawArgument()), Class: ClassLarge, Variant: "word", Limits: name == "operatorname*"}
	case "pmod":
		arg := p.argument()
		return row([]*MNode{{Kind: MSpace, Em: 0.5}, {Kind: MFenced, Open: "(", Close: ")",
			Kids: []*MNode{row([]*MNode{{Kind: MOp, Text: "mod", Variant: "word", Class: ClassOrd}, {Kind: MSpace, Em: 0.33}, arg})}}})
	case "mod":
		return row([]*MNode{{Kind: MSpace, Em: 0.5}, {Kind: MOp, Text: "mod", Variant: "word", Class: ClassOrd}, {Kind: MSpace, Em: 0.33}})
	case "left":
		open := p.delimiter()
		body := p.list(&stop{right: true})
		close := ""
		if !p.eof() { // at \right
			p.next()
			p.command()
			close = p.delimiter()
		}
		return &MNode{Kind: MFenced, Open: open, Close: close, Kids: []*MNode{row(body)}}
	case "right":
		p.delimiter()
		return nil
	case "middle":
		d := p.delimiter()
		return &MNode{Kind: MOp, Text: d, Class: ClassRel, Size: 2}
	case "not":
		n := p.atom(st)
		if n != nil && n.Kind == MOp {
			switch n.Text {
			case "=":
				n.Text = "≠"
			case "∈":
				n.Text = "∉"
			case "≤":
				n.Text = "≰"
			case "≥":
				n.Text = "≱"
			case "⊂":
				n.Text = "⊄"
			default:
				n.Text += "̸"
			}
		}
		return n
	case "limits", "nolimits", "displaystyle", "textstyle", "scriptstyle", "scriptscriptstyle", "nonumber",
		"notag", "label", "tag", "centering", "small", "large", "Large":
		if name == "label" || name == "tag" {
			p.rawArgument()
		}
		return nil
	case "overset", "stackrel":
		over := p.argument()
		base := p.argument()
		return &MNode{Kind: MScripts, Limits: true, Kids: []*MNode{base, nil, over}}
	case "underset":
		under := p.argument()
		base := p.argument()
		return &MNode{Kind: MScripts, Limits: true, Kids: []*MNode{base, under, nil}}
	case "overbrace", "underbrace":
		body := p.argument()
		return &MNode{Kind: MAccent, Text: map[string]string{"overbrace": "⏞", "underbrace": "⏟"}[name], Under: name == "underbrace", Kids: []*MNode{body}}
	case "begin":
		return p.environment(p.rawArgument())
	case "end":
		p.rawArgument()
		return nil
	case "\\", "cr", "newline":
		return nil
	case "phantom", "hphantom", "vphantom":
		p.argument()
		return &MNode{Kind: MSpace, Em: 0.5}
	case "color", "textcolor":
		p.rawArgument()
		if name == "textcolor" {
			return p.argument()
		}
		return nil
	case "boxed", "fbox":
		return p.argument()
	case "hspace", "hspace*", "kern", "mkern":
		arg := p.rawArgument()
		return &MNode{Kind: MSpace, Em: lengthEm(arg)}
	}
	p.warn = append(p.warn, "unsupported math command \\"+name)
	return &MNode{Kind: MError, Text: "\\" + name}
}

// delimiter reads the delimiter after \left, \right or \big.
func (p *mparser) delimiter() string {
	p.skipSpace()
	if p.eof() {
		return ""
	}
	r := p.next()
	switch r {
	case '.':
		return ""
	case '\\':
		name := p.command()
		if s, ok := symbols[name]; ok {
			return s.text
		}
		switch name {
		case "{", "lbrace":
			return "{"
		case "}", "rbrace":
			return "}"
		case "|":
			return "‖"
		}
		p.warn = append(p.warn, "unsupported delimiter \\"+name)
		return ""
	case '<':
		return "⟨"
	case '>':
		return "⟩"
	}
	return string(r)
}

// environment reads \begin{name} ... \end{name}.
func (p *mparser) environment(name string) *MNode {
	star := strings.TrimSuffix(name, "*")
	var spec string
	if star == "array" || star == "alignat" {
		spec = p.rawArgument()
	}
	rows := p.table()
	t := &MNode{Kind: MTable, Rows: rows}
	switch star {
	case "cases", "dcases":
		t.Align = "ll"
		return &MNode{Kind: MFenced, Open: "{", Close: "", Kids: []*MNode{t}}
	case "rcases":
		t.Align = "ll"
		return &MNode{Kind: MFenced, Open: "", Close: "}", Kids: []*MNode{t}}
	case "matrix", "smallmatrix":
		t.Align = "c"
		return t
	case "pmatrix":
		t.Align = "c"
		return &MNode{Kind: MFenced, Open: "(", Close: ")", Kids: []*MNode{t}}
	case "bmatrix":
		t.Align = "c"
		return &MNode{Kind: MFenced, Open: "[", Close: "]", Kids: []*MNode{t}}
	case "Bmatrix":
		t.Align = "c"
		return &MNode{Kind: MFenced, Open: "{", Close: "}", Kids: []*MNode{t}}
	case "vmatrix":
		t.Align = "c"
		return &MNode{Kind: MFenced, Open: "|", Close: "|", Kids: []*MNode{t}}
	case "Vmatrix":
		t.Align = "c"
		return &MNode{Kind: MFenced, Open: "‖", Close: "‖", Kids: []*MNode{t}}
	case "array":
		for _, c := range spec {
			if c == 'l' || c == 'c' || c == 'r' {
				t.Align += string(c)
			}
		}
		return t
	case "aligned", "align", "alignat", "split", "eqnarray", "flalign":
		t.Align = "rl"
		return t
	case "gathered", "gather", "equation", "multline":
		t.Align = "c"
		return t
	}
	p.warn = append(p.warn, "unsupported math environment "+name)
	t.Align = "c"
	return t
}

// table reads cells separated by & and rows by \\ up to \end{...}.
func (p *mparser) table() [][]*MNode {
	var rows [][]*MNode
	var cur []*MNode
	for {
		cell := row(p.list(&stop{end: true, cell: true}))
		cur = append(cur, cell)
		p.skipSpace()
		if p.eof() {
			break
		}
		if p.peek() == '&' {
			p.next()
			continue
		}
		// \\, \cr or \end
		p.next()
		name := p.command()
		if name == "end" {
			p.rawArgument()
			break
		}
		p.optional() // \\[2pt]
		rows = append(rows, cur)
		cur = nil
	}
	if len(cur) > 1 || (len(cur) == 1 && !emptyNode(cur[0])) {
		rows = append(rows, cur)
	}
	return rows
}

func emptyNode(n *MNode) bool { return n == nil || (n.Kind == MRow && len(n.Kids) == 0) }

// setVariant applies a font style to identifiers and numbers.
func setVariant(n *MNode, v string) {
	if n == nil {
		return
	}
	switch n.Kind {
	case MIdent, MNum:
		n.Variant = v
		if v == "" {
			n.Variant = "italic"
		}
		// \mathrm{abc} is one word, not three variables.
	case MRow:
		if v == "normal" && allLetters(n) {
			var sb strings.Builder
			for _, k := range n.Kids {
				sb.WriteString(k.Text)
			}
			*n = MNode{Kind: MIdent, Text: sb.String(), Variant: "normal"}
			return
		}
	}
	for _, k := range n.Kids {
		setVariant(k, v)
	}
}

func allLetters(n *MNode) bool {
	if len(n.Kids) < 2 {
		return false
	}
	for _, k := range n.Kids {
		if k == nil || (k.Kind != MIdent && k.Kind != MNum) {
			return false
		}
	}
	return true
}

// textArg turns the argument of \text into plain text.
func textArg(s string) string {
	s = strings.NewReplacer("\\ ", " ", "~", " ", "\\,", " ", "{", "", "}", "", "\\%", "%", "\\&", "&",
		"\\_", "_", "\\#", "#", "\\$", "$").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// lengthEm reads a TeX length ("1em", "3mu", "2pt") in em.
func lengthEm(s string) float64 {
	s = strings.TrimSpace(s)
	var v float64
	var unit string
	for i, r := range s {
		if !(r >= '0' && r <= '9' || r == '.' || r == '-') {
			v = parseFloat(s[:i])
			unit = strings.TrimSpace(s[i:])
			break
		}
	}
	switch unit {
	case "em":
		return v
	case "mu":
		return v / 18
	case "pt":
		return v / 10
	case "ex":
		return v * 0.43
	}
	return 0.33
}

func parseFloat(s string) float64 {
	var v, frac float64
	neg := false
	dot := false
	scale := 1.0
	for _, r := range s {
		switch {
		case r == '-':
			neg = true
		case r == '.':
			dot = true
		case r >= '0' && r <= '9':
			if dot {
				scale /= 10
				frac += float64(r-'0') * scale
			} else {
				v = v*10 + float64(r-'0')
			}
		}
	}
	if neg {
		return -(v + frac)
	}
	return v + frac
}
