package webkit

import (
	"net/http"
	"slices"
	"strings"
)

// Display is a visitor's display preferences. They live in a cookie (not
// the session) so they apply before logging in, on every site of the
// host, and cost nothing on the server.
type Display struct {
	// Theme: "" is the light design, or dark, contrast (high contrast),
	// system (light or dark as the operating system prefers).
	Theme string
	// Size: "" is the normal text size, or l, xl, xxl.
	Size string
}

// Themes and Sizes are the accepted values, in the order the selectors
// show them.
var (
	Themes = []string{"", "dark", "contrast", "system"}
	Sizes  = []string{"", "l", "xl", "xxl"}
)

// DisplayCookie is the cookie's name.
const DisplayCookie = "cms_display"

// ReadDisplay returns the request's display preferences.
func ReadDisplay(r *http.Request) Display {
	c, err := r.Cookie(DisplayCookie)
	if err != nil {
		return Display{}
	}
	theme, size, _ := strings.Cut(c.Value, ".")
	d := Display{Theme: legacyTheme(theme), Size: size}
	if !slices.Contains(Themes, d.Theme) {
		d.Theme = ""
	}
	if !slices.Contains(Sizes, d.Size) {
		d.Size = ""
	}
	return d
}

// DisplayFromForm reads the theme and size fields of a preferences form;
// unknown values become the defaults.
func DisplayFromForm(r *http.Request) Display {
	d := Display{Theme: legacyTheme(r.FormValue("theme")), Size: r.FormValue("size")}
	if !slices.Contains(Themes, d.Theme) {
		d.Theme = ""
	}
	if !slices.Contains(Sizes, d.Size) {
		d.Size = ""
	}
	return d
}

// legacyTheme maps the values of the earlier dark-first design ("light"
// was a choice then) to the current ones.
func legacyTheme(t string) string {
	if t == "light" {
		return ""
	}
	return t
}

// WriteDisplay stores the preferences for a year.
func WriteDisplay(w http.ResponseWriter, d Display, secure bool) {
	http.SetCookie(w, &http.Cookie{Name: DisplayCookie, Value: d.Theme + "." + d.Size, Path: "/", MaxAge: 365 * 24 * 3600,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode})
}

// Option is an entry of a preferences selector.
type Option struct{ Value, Label string }

// ThemeOptions and SizeOptions are the selector entries, labelled by tr
// (the page's translation function).
func ThemeOptions(tr func(string, ...any) string) []Option {
	return []Option{{"", tr("Light")}, {"dark", tr("Dark")}, {"contrast", tr("High contrast")}, {"system", tr("As the system")}}
}

func SizeOptions(tr func(string, ...any) string) []Option {
	return []Option{{"", tr("Normal")}, {"l", tr("Large")}, {"xl", tr("Larger")}, {"xxl", tr("Largest")}}
}
