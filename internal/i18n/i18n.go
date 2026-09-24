// Package i18n translates the user interface. Messages are keyed by their
// English text (gettext style), so templates stay readable and a missing
// translation falls back to English. Spanish and English are provided.
package i18n

import (
	"fmt"
	"sort"
	"strings"
)

// Default is the fallback language.
const Default = "en"

var catalogs = map[string]map[string]string{
	"en": {},
	"es": es,
}

// Languages returns the available UI languages.
func Languages() []string {
	out := make([]string, 0, len(catalogs))
	for l := range catalogs {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// Names are the endonyms shown in the language selector.
var Names = map[string]string{"en": "English", "es": "Español"}

// T translates msg into lang, formatting args with fmt when present.
func T(lang, msg string, args ...any) string {
	if tr, ok := catalogs[lang][msg]; ok {
		msg = tr
	}
	if len(args) > 0 {
		return fmt.Sprintf(msg, args...)
	}
	return msg
}

// Has reports whether lang has a translation for msg (tests).
func Has(lang, msg string) bool {
	if lang == Default {
		return true
	}
	_, ok := catalogs[lang][msg]
	return ok
}

// Negotiate picks the UI language: an explicit choice (cookie/session)
// first, then the user's preferences, then Accept-Language. Only languages
// in allowed (all when empty) and available are considered.
func Negotiate(explicit string, preferred []string, acceptLanguage string, allowed []string) string {
	ok := func(l string) bool {
		if _, avail := catalogs[l]; !avail {
			return false
		}
		if len(allowed) == 0 {
			return true
		}
		for _, a := range allowed {
			if a == l {
				return true
			}
		}
		return false
	}
	if l := base(explicit); ok(l) {
		return l
	}
	for _, p := range preferred {
		if l := base(p); ok(l) {
			return l
		}
	}
	for _, part := range strings.Split(acceptLanguage, ",") {
		tag, _, _ := strings.Cut(strings.TrimSpace(part), ";")
		if l := base(tag); ok(l) {
			return l
		}
	}
	if ok(Default) {
		return Default
	}
	if len(allowed) > 0 {
		return allowed[0]
	}
	return Default
}

func base(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if i := strings.IndexAny(tag, "-_"); i > 0 {
		tag = tag[:i]
	}
	return tag
}
