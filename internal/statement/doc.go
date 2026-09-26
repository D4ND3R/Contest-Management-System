// Package statement reads task statements written in Markdown, LaTeX or
// HTML (with TeX formulas) into one document model, and renders it as
// HTML with MathML formulas (the task page) and as PDF (with the examples
// and limits typeset in). No JavaScript is involved anywhere: formulas are
// laid out on the server.
package statement

// Doc is a statement.
type Doc struct {
	Title  string // when the source names one (\begin{problem}{Title}...)
	Blocks []Block
	// Examples written inside the source (olymp.sty \exmp, Polygon); the
	// task's own examples are added by the caller.
	Examples []Example
	Warnings []string
}

// Block is one of the block types below.
type Block interface{ isBlock() }

// Heading is a section title (level 1 to 4).
type Heading struct {
	Level int
	Text  []Inline
}

// Para is a paragraph.
type Para struct{ Text []Inline }

// List is a bulleted or numbered list.
type List struct {
	Ordered bool
	Start   int
	Items   [][]Block
}

// Code is preformatted text (source code, input formats).
type Code struct{ Text string }

// Display is a displayed formula.
type Display struct{ Math *Math }

// Table is a table; the first row is the header when Header is set.
type Table struct {
	Header bool
	Align  []byte // 'l', 'c' or 'r' per column
	Rows   [][][]Inline
}

// Quote is an indented quotation or note.
type Quote struct{ Blocks []Block }

// Rule is a horizontal line.
type Rule struct{}

// Image is a picture from the task's attachments.
type Image struct{ Src, Alt string }

// ExamplesHere marks where the examples go (olymp.sty's \Examples, a
// "{{examples}}" line in Markdown); without it they follow the text.
type ExamplesHere struct{}

func (*Heading) isBlock() {}
func (*Para) isBlock()    {}
func (*List) isBlock()    {}
func (*Code) isBlock()    {}
func (*Display) isBlock() {}
func (*Table) isBlock()   {}
func (*Quote) isBlock()   {}
func (*Rule) isBlock()    {}
func (*Image) isBlock()   {}

func (*ExamplesHere) isBlock() {}

// Inline is one of the inline types below.
type Inline interface{ isInline() }

// Text is plain text.
type Text struct{ S string }

// Styled is text in a style: 'i' italic, 'b' bold, 'u' underline, 's'
// struck out.
type Styled struct {
	Style byte
	Text  []Inline
}

// CodeSpan is inline code (monospace).
type CodeSpan struct{ S string }

// InlineMath is a formula inside a line.
type InlineMath struct{ Math *Math }

// Link is a hyperlink.
type Link struct {
	URL  string
	Text []Inline
}

// Break is a forced line break.
type Break struct{}

func (*Text) isInline()       {}
func (*Styled) isInline()     {}
func (*CodeSpan) isInline()   {}
func (*InlineMath) isInline() {}
func (*Link) isInline()       {}
func (*Break) isInline()      {}

// Example is a sample input with its expected output.
type Example struct {
	Input, Output string
	Note          []Block // an explanation, when given
}
