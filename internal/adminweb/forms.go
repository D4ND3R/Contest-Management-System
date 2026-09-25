package adminweb

import (
	"errors"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/i18n"
)

// form reads and validates submitted values, collecting the first error.
type form struct {
	r    *http.Request
	err  error
	lang string
}

func newForm(r *http.Request) *form { return &form{r: r, lang: adminLang(r)} }

// fail records the first error, translated into the administrator's
// language. Messages starting with "%s" name the field first: its label is
// translated too.
func (f *form) fail(format string, args ...any) {
	if f.err != nil {
		return
	}
	if len(args) > 0 && strings.HasPrefix(format, "%s") {
		if label, ok := args[0].(string); ok {
			args = append([]any{i18n.T(f.lang, label)}, args[1:]...)
		}
	}
	f.err = errors.New(i18n.T(f.lang, format, args...))
}

func (f *form) str(name string) string { return strings.TrimSpace(f.r.FormValue(name)) }

func (f *form) required(name, label string) string {
	v := f.str(name)
	if v == "" {
		f.fail("%s is required", label)
	}
	return v
}

func (f *form) optStr(name string) *string {
	v := f.str(name)
	if v == "" {
		return nil
	}
	return &v
}

func (f *form) check(name string) bool {
	switch f.r.FormValue(name) {
	case "on", "1", "true", "yes":
		return true
	}
	return false
}

func (f *form) int64(name, label string, def int64) int64 {
	v := f.str(name)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		f.fail("%s must be an integer", label)
	}
	return n
}

func (f *form) nonNeg(name, label string, def int64) int64 {
	n := f.int64(name, label, def)
	if n < 0 {
		f.fail("%s must not be negative", label)
	}
	return n
}

func (f *form) int32(name, label string, def int32) int32 {
	n := f.int64(name, label, int64(def))
	if n < -1<<31 || n > 1<<31-1 {
		f.fail("%s is out of range", label)
	}
	return int32(n)
}

func (f *form) optInt64(name, label string) *int64 {
	if f.str(name) == "" {
		return nil
	}
	n := f.nonNeg(name, label, 0)
	return &n
}

func (f *form) optInt32(name, label string) *int32 {
	if f.str(name) == "" {
		return nil
	}
	n := f.int32(name, label, 0)
	if n < 0 {
		f.fail("%s must not be negative", label)
	}
	return &n
}

// optPositive64 is an optional value that must be > 0 when present.
func (f *form) optPositive64(name, label string) *int64 {
	v := f.optInt64(name, label)
	if v != nil && *v == 0 {
		f.fail("%s must be positive", label)
	}
	return v
}

func (f *form) float(name, label string) (float64, bool) {
	v := f.str(name)
	if v == "" {
		return 0, false
	}
	x, err := strconv.ParseFloat(v, 64)
	if err != nil {
		f.fail("%s must be a number", label)
		return 0, false
	}
	return x, true
}

// secondsToMs parses seconds (decimal) into milliseconds.
func (f *form) secondsToMs(name, label string) *int32 {
	x, ok := f.float(name, label)
	if !ok {
		return nil
	}
	if x <= 0 || x > 3600 {
		f.fail("%s must be between 0 and 3600 seconds", label)
		return nil
	}
	ms := int32(x*1000 + 0.5)
	return &ms
}

// mib parses a size in MiB into bytes.
func (f *form) mib(name, label string) *int64 {
	x, ok := f.float(name, label)
	if !ok {
		return nil
	}
	if x <= 0 {
		f.fail("%s must be positive", label)
		return nil
	}
	b := int64(x * (1 << 20))
	return &b
}

// kib parses a size in KiB into bytes.
func (f *form) kib(name, label string) *int64 {
	x, ok := f.float(name, label)
	if !ok {
		return nil
	}
	if x <= 0 {
		f.fail("%s must be positive", label)
		return nil
	}
	b := int64(x * (1 << 10))
	return &b
}

var timeLayouts = []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02 15:04"}

func (f *form) optTime(name, label string, loc *time.Location) *time.Time {
	v := f.str(name)
	if v == "" {
		return nil
	}
	for _, l := range timeLayouts {
		if t, err := time.ParseInLocation(l, v, loc); err == nil {
			t = t.UTC()
			return &t
		}
	}
	f.fail("%s: invalid date/time %q (YYYY-MM-DD HH:MM)", label, v)
	return nil
}

func (f *form) time(name, label string, loc *time.Location) time.Time {
	t := f.optTime(name, label, loc)
	if t == nil {
		f.fail("%s is required", label)
		return time.Time{}
	}
	return *t
}

// list splits a comma/space separated field.
func (f *form) list(name string) []string {
	return splitList(f.r.FormValue(name))
}

func splitList(v string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, x := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\r' || r == '\t' }) {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// multi returns the checked values of a multi-valued field.
func (f *form) multi(name string) []string {
	if err := f.r.ParseForm(); err != nil {
		f.fail("invalid form")
	}
	out := []string{}
	var vals []string
	if f.r.MultipartForm != nil {
		vals = f.r.MultipartForm.Value[name]
	} else {
		vals = f.r.PostForm[name]
	}
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// prefixes parses "10.0.0.1, 192.168.0.0/24".
func (f *form) prefixes(name, label string) []netip.Prefix {
	out := []netip.Prefix{}
	for _, v := range f.list(name) {
		p, err := parsePrefix(v)
		if err != nil {
			f.fail("%s: invalid address %q", label, v)
			continue
		}
		out = append(out, p)
	}
	return out
}

func parsePrefix(v string) (netip.Prefix, error) {
	if strings.Contains(v, "/") {
		p, err := netip.ParsePrefix(v)
		return p.Masked(), err
	}
	a, err := netip.ParseAddr(v)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(a, a.BitLen()), nil
}

func (f *form) oneOf(name, label string, allowed ...string) string {
	v := f.str(name)
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	f.fail("%s must be one of %s", label, strings.Join(allowed, ", "))
	return allowed[0]
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func (f *form) identifier(name, label string) string {
	v := f.required(name, label)
	if v != "" && !nameRe.MatchString(v) {
		f.fail("%s may only contain letters, digits, '_', '.' and '-'", label)
	}
	return v
}

func (f *form) timezone(name string) string {
	v := f.str(name)
	if v == "" {
		return "UTC"
	}
	if _, err := time.LoadLocation(v); err != nil {
		f.fail("unknown timezone %q", v)
	}
	return v
}
