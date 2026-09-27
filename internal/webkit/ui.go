package webkit

import (
	"html/template"
	"math"
	"strconv"
	"strings"

	"github.com/D4ND3R/Contest-Management-System/internal/i18n"
)

// UIFuncs are the template helpers shared by the sites (every site adds
// them to its own).
func UIFuncs() template.FuncMap {
	return template.FuncMap{
		"letter": Letter,
		"share":  Percent,
		// dir and bcp47 mark text in another language than the page's
		// (a statement in Persian under an English interface).
		"dir":   i18n.Dir,
		"bcp47": func(lang string) string { return strings.ReplaceAll(lang, "_", "-") },
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
