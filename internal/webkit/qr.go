package webkit

import (
	"fmt"
	"html/template"
	"strings"

	"rsc.io/qr"
)

// QRSVG renders text as an inline SVG QR code (no image files, no data:
// URLs, compatible with the strict CSP).
func QRSVG(text string, px int) (template.HTML, error) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", err
	}
	n := code.Size
	var b strings.Builder
	quiet := 4
	total := n + 2*quiet
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d" shape-rendering="crispEdges"><rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`,
		total, total, px, px, total, total)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if code.Black(x, y) {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+quiet, y+quiet)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return template.HTML(b.String()), nil
}
