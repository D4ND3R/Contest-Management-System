// Package i18n translates the user interface. Messages are keyed by their
// English text (gettext style), so templates stay readable and a missing
// translation falls back to English. English and Spanish cover every site;
// more languages for the contestant and ranking sites are locale files
// (locales/*.yaml, embedded, plus an optional directory of the
// installation: adding a language needs no code).
package i18n

import (
	"bufio"
	"bytes"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Default is the fallback language.
const Default = "en"

// catalogs, Names, dirs and admin are filled at start-up (init and
// LoadDir, before any request is served) and only read afterwards.
var catalogs = map[string]map[string]string{
	"en": {},
	"es": es,
}

// Names are the endonyms shown in the language selector.
var Names = map[string]string{"en": "English", "es": "Español"}

// dirs holds the writing direction of the languages that declare one.
var dirs = map[string]string{}

// admin lists the languages that translate the administration site too.
var admin = map[string]bool{"en": true, "es": true}

// rtl are right-to-left languages (also for statements in a language the
// interface does not have).
var rtl = map[string]bool{"ar": true, "arc": true, "ckb": true, "dv": true, "fa": true, "he": true, "ku-arab": true,
	"ps": true, "sd": true, "ug": true, "ur": true, "yi": true}

//go:embed locales/*.yaml
var localeFS embed.FS

//go:generate go run ./gen ../..
//go:embed contestant.txt
var contestantTxt []byte

// contestant are the messages of the contestant and ranking sites.
var contestant = func() []string {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(contestantTxt))
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		if s, err := strconv.Unquote(line); err == nil {
			out = append(out, s)
		}
	}
	return out
}()

// ContestantMessages lists the messages a language for the contestant and
// ranking sites translates.
func ContestantMessages() []string { return contestant }

func init() {
	entries, err := localeFS.ReadDir("locales")
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		data, err := localeFS.ReadFile("locales/" + e.Name())
		if err != nil {
			panic(err)
		}
		if _, err := Register(strings.TrimSuffix(e.Name(), ".yaml"), data); err != nil {
			panic(fmt.Sprintf("locales/%s: %v", e.Name(), err))
		}
	}
}

// Locale is a locale file: a language of the contestant and ranking sites.
type Locale struct {
	// Name is the language's own name (Français, العربية).
	Name string `yaml:"name"`
	// Dir is ltr or rtl.
	Dir string `yaml:"dir"`
	// Admin: the file translates the administration site too.
	Admin bool `yaml:"admin,omitempty"`
	// Messages maps the English text to the translation; empty values
	// fall back to English.
	Messages map[string]string `yaml:"messages"`
}

// ParseLocale reads a locale file and checks it: the translations whose
// format verbs (%d, %s...) differ from the English text's are dropped and
// reported, since they would print garbage.
func ParseLocale(data []byte) (*Locale, []string, error) {
	var l Locale
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&l); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(l.Name) == "" {
		return nil, nil, fmt.Errorf("name is required")
	}
	switch l.Dir {
	case "":
		l.Dir = "ltr"
	case "ltr", "rtl":
	default:
		return nil, nil, fmt.Errorf("dir must be ltr or rtl, not %q", l.Dir)
	}
	var warnings []string
	for k, v := range l.Messages {
		if v == "" {
			delete(l.Messages, k)
			continue
		}
		if !SameVerbs(k, v) {
			warnings = append(warnings, fmt.Sprintf("%q: the translation %q does not use the same %%-verbs; English is shown", k, v))
			delete(l.Messages, k)
		}
	}
	sort.Strings(warnings)
	return &l, warnings, nil
}

// Register adds (or completes) a language from a locale file. A code
// already present keeps the messages the file does not translate.
func Register(code string, data []byte) ([]string, error) {
	code = strings.ToLower(code)
	if !validCode(code) {
		return nil, fmt.Errorf("%q is not a language code (e.g. fr, pt-br)", code)
	}
	l, warnings, err := ParseLocale(data)
	if err != nil {
		return nil, err
	}
	cat := catalogs[code]
	if cat == nil {
		cat = map[string]string{}
		catalogs[code] = cat
	}
	for k, v := range l.Messages {
		cat[k] = v
	}
	Names[code] = l.Name
	dirs[code] = l.Dir
	if l.Admin {
		admin[code] = true
	}
	return warnings, nil
}

func validCode(code string) bool {
	if len(code) < 2 || len(code) > 12 {
		return false
	}
	for i, r := range code {
		if !(r >= 'a' && r <= 'z' || r == '-' && i > 1) {
			return false
		}
	}
	return true
}

// LoadDir registers the locale files (*.yaml, named after the language
// code) of an installation's directory. Call it before serving requests.
func LoadDir(dir string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	var warnings []string
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return warnings, err
		}
		w, err := Register(strings.TrimSuffix(filepath.Base(f), ".yaml"), data)
		if err != nil {
			return warnings, fmt.Errorf("%s: %w", f, err)
		}
		for _, x := range w {
			warnings = append(warnings, filepath.Base(f)+": "+x)
		}
	}
	return warnings, nil
}

// Languages returns the available UI languages of the contestant and
// ranking sites.
func Languages() []string {
	out := make([]string, 0, len(catalogs))
	for l := range catalogs {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// AdminLanguages returns the languages that translate the administration
// site.
func AdminLanguages() []string {
	var out []string
	for l := range catalogs {
		if admin[l] {
			out = append(out, l)
		}
	}
	sort.Strings(out)
	return out
}

// Dir is the writing direction of a language (or of a statement language
// such as "fa_IR"): "rtl" or "ltr".
func Dir(lang string) string {
	l := strings.ToLower(strings.ReplaceAll(lang, "_", "-"))
	if d, ok := dirs[l]; ok {
		return d
	}
	if rtl[l] || rtl[base(l)] {
		return "rtl"
	}
	if d, ok := dirs[base(l)]; ok {
		return d
	}
	return "ltr"
}

// Coverage counts the contestant messages lang translates.
func Coverage(lang string) (translated, total int) {
	for _, m := range contestant {
		if lang == Default || catalogs[lang][m] != "" {
			translated++
		}
	}
	return translated, len(contestant)
}

// SameVerbs reports whether two format strings take the same arguments:
// the same verbs for the same argument positions (explicit indexes such as
// %[2]s let a translation reorder them).
func SameVerbs(a, b string) bool {
	va, ok1 := verbs(a)
	vb, ok2 := verbs(b)
	if !ok1 || !ok2 || len(va) != len(vb) {
		return false
	}
	for i, v := range va {
		if vb[i] != v {
			return false
		}
	}
	return true
}

// verbs maps each argument position of a format string to its verb.
func verbs(f string) (map[int]byte, bool) {
	out := map[int]byte{}
	arg := 1
	for i := 0; i < len(f); i++ {
		if f[i] != '%' {
			continue
		}
		i++
		for i < len(f) && strings.IndexByte("+-# 0123456789.", f[i]) >= 0 {
			i++
		}
		if i < len(f) && f[i] == '[' {
			end := strings.IndexByte(f[i:], ']')
			if end < 0 {
				return nil, false
			}
			n, err := strconv.Atoi(f[i+1 : i+end])
			if err != nil || n < 1 {
				return nil, false
			}
			arg = n
			i += end + 1
		}
		if i >= len(f) {
			return nil, false
		}
		if f[i] == '%' {
			continue
		}
		if prev, dup := out[arg]; dup && prev != f[i] {
			return nil, false
		}
		out[arg] = f[i]
		arg++
	}
	return out, true
}

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
// TDetail translates messages of the form "Key: detail", where only the
// key (including the colon) is in the catalog.
func TDetail(lang, msg string) string {
	if Has(lang, msg) {
		return T(lang, msg)
	}
	if i := strings.Index(msg, ": "); i > 0 && Has(lang, msg[:i+1]) {
		return T(lang, msg[:i+1]) + msg[i+1:]
	}
	return msg
}

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
	// A regional code ("pt-br") when that language exists, else its base.
	pick := func(tag string) string {
		full := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(tag), "_", "-"))
		if ok(full) {
			return full
		}
		if ok(base(tag)) {
			return base(tag)
		}
		return ""
	}
	if l := pick(explicit); l != "" {
		return l
	}
	for _, p := range preferred {
		if l := pick(p); l != "" {
			return l
		}
	}
	for _, part := range strings.Split(acceptLanguage, ",") {
		tag, _, _ := strings.Cut(strings.TrimSpace(part), ";")
		if l := pick(tag); l != "" {
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
