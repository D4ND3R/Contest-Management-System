package statement

import (
	"path"
	"strings"
	"unicode/utf8"
)

// Content types of statement sources.
const (
	TypePDF      = "application/pdf"
	TypeMarkdown = "text/markdown; charset=utf-8"
	TypeLaTeX    = "text/x-tex; charset=utf-8"
	TypeHTML     = "text/html; charset=utf-8"
	TypeText     = "text/plain; charset=utf-8"
)

// TypeForName is the content type of a statement file by its extension
// ("" when it is not a statement format).
func TypeForName(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".pdf":
		return TypePDF
	case ".md", ".markdown":
		return TypeMarkdown
	case ".tex":
		return TypeLaTeX
	case ".html", ".htm":
		return TypeHTML
	case ".txt":
		return TypeText
	}
	return ""
}

// Extension is the file extension of a content type.
func Extension(contentType string) string {
	switch {
	case strings.HasPrefix(contentType, "text/markdown"):
		return ".md"
	case strings.HasPrefix(contentType, "text/x-tex"):
		return ".tex"
	case strings.HasPrefix(contentType, "text/html"):
		return ".html"
	case strings.HasPrefix(contentType, "text/plain"):
		return ".txt"
	}
	return ".pdf"
}

// IsSource reports whether a statement is a source the CMS renders (not
// an uploaded PDF).
func IsSource(contentType string) bool { return !strings.HasPrefix(contentType, TypePDF) }

// Parse reads a statement source by its content type. PDF is not a
// source: ok is false.
func Parse(contentType string, data []byte) (doc *Doc, ok bool) {
	return ParseIn(contentType, data, "")
}

// ParseIn is Parse for a statement in language lang (section titles the
// renderer writes itself follow it).
func ParseIn(contentType string, data []byte, lang string) (doc *Doc, ok bool) {
	if !IsSource(contentType) {
		return nil, false
	}
	s := string(data)
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	s = strings.TrimPrefix(s, "\uFEFF")
	switch {
	case strings.HasPrefix(contentType, "text/x-tex"):
		return ParseLaTeXIn(s, lang), true
	case strings.HasPrefix(contentType, "text/html"):
		return ParseHTML(s), true
	}
	return ParseMarkdown(s), true
}
