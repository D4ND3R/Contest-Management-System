package webtest

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// A11y checks a rendered page against the accessibility rules every page
// of the three sites follows (SPEC_IOI §8: keyboard, screen readers):
//
//   - <html> declares lang and dir, and the page has a title, one main
//     landmark and a first-level heading;
//   - every image has alt text (empty for decoration);
//   - every form control has a name (a label, aria-label, aria-labelledby
//     or title), and so does every button and link (text or a label);
//   - ids are unique, aria-labelledby / aria-describedby / label for point
//     at existing ids, and nothing has a positive tabindex (which breaks
//     the reading order).
//
// Fragments (htmx partials) are checked with page=false: no document-level
// rules.
func A11y(t testing.TB, name, body string, page bool) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var problems []string
	fail := func(format string, args ...any) {
		problems = append(problems, sprintf(format, args...))
	}
	ids := map[string]int{}
	labelFor := map[string]bool{}
	var refs [][2]string // attribute, id
	var mains, h1s int
	var htmlNode *html.Node
	var title string

	var walk func(n *html.Node, inLabel bool)
	walk = func(n *html.Node, inLabel bool) {
		if n.Type == html.ElementNode {
			if id := attr(n, "id"); id != "" {
				ids[id]++
			}
			if tab := attr(n, "tabindex"); tab != "" && tab != "0" && tab != "-1" {
				fail("<%s> has tabindex=%s", n.Data, tab)
			}
			for _, a := range []string{"aria-labelledby", "aria-describedby"} {
				for _, id := range strings.Fields(attr(n, a)) {
					refs = append(refs, [2]string{a, id})
				}
			}
			switch n.Data {
			case "html":
				htmlNode = n
			case "title":
				title = strings.TrimSpace(text(n))
			case "main":
				mains++
			case "h1":
				h1s++
			case "label":
				if f := attr(n, "for"); f != "" {
					labelFor[f] = true
					refs = append(refs, [2]string{"label for", f})
				}
				inLabel = true
			case "img":
				if !has(n, "alt") {
					fail("<img src=%q> has no alt", attr(n, "src"))
				}
			case "input", "select", "textarea":
				typ := attr(n, "type")
				if n.Data == "input" && (typ == "hidden" || typ == "submit" || typ == "button" || typ == "reset" || typ == "image") {
					if (typ == "submit" || typ == "button") && attr(n, "value") == "" && !named(n) {
						fail("<input type=%s> has no value", typ)
					}
					break
				}
				if !inLabel && !named(n) && !labelFor[attr(n, "id")] {
					// A label may come after the control; checked at the end.
					refs = append(refs, [2]string{"control " + n.Data + " " + attr(n, "name"), "\x00" + attr(n, "id")})
				}
			case "button":
				if strings.TrimSpace(text(n)) == "" && !named(n) {
					fail("a button has no text or label")
				}
			case "a":
				if has(n, "href") && strings.TrimSpace(text(n)) == "" && !named(n) && !hasAltImage(n) {
					fail("the link to %q has no text or label", attr(n, "href"))
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inLabel)
		}
	}
	walk(doc, false)

	for id, n := range ids {
		if n > 1 {
			fail("id %q is used %d times", id, n)
		}
	}
	for _, r := range refs {
		if strings.HasPrefix(r[1], "\x00") {
			if id := r[1][1:]; id == "" || !labelFor[id] {
				fail("%s has no label", strings.TrimSpace(r[0]))
			}
			continue
		}
		if ids[r[1]] == 0 {
			fail("%s points at the missing id %q", r[0], r[1])
		}
	}
	if page {
		if htmlNode == nil || attr(htmlNode, "lang") == "" || attr(htmlNode, "dir") == "" {
			fail("<html> must declare lang and dir")
		}
		if title == "" {
			fail("the page has no title")
		}
		if mains != 1 {
			fail("the page has %d main landmarks", mains)
		}
		if h1s == 0 {
			fail("the page has no h1")
		}
	}
	for _, p := range problems {
		t.Errorf("%s: %s", name, p)
	}
}

func named(n *html.Node) bool {
	return strings.TrimSpace(attr(n, "aria-label")) != "" || attr(n, "aria-labelledby") != "" || strings.TrimSpace(attr(n, "title")) != ""
}

func hasAltImage(n *html.Node) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && (c.Data == "img" && attr(c, "alt") != "" || named(c) || hasAltImage(c)) {
			return true
		}
	}
	return false
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func has(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key {
			return true
		}
	}
	return false
}

// text is an element's text, leaving out hidden parts (aria-hidden).
func text(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		if n.Type == html.ElementNode && attr(n, "aria-hidden") == "true" {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }
