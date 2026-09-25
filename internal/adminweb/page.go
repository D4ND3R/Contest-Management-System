package adminweb

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/i18n"
	"github.com/D4ND3R/Contest-Management-System/internal/webkit"
)

// page is the data of every admin page.
type page struct {
	Lang       string
	Title      string
	Admin      *sqlc.Admin
	CSRF       string
	Active     string
	Flash      string
	Error      string
	Crumbs     []crumb
	CanWrite   bool
	CanMessage bool
	// Pending is the number of unanswered questions (menu counter).
	Pending    int64
	Data       any
	ServerTime time.Time
}

type crumb struct{ Name, URL string }

func (s *Server) newPage(w http.ResponseWriter, r *http.Request, rc *reqCtx, title, active string, data any) *page {
	lang := adminLang(r)
	p := &page{Lang: lang, Title: i18n.T(lang, title), Active: active, Data: data, ServerTime: s.now()}
	if rc != nil {
		p.Admin = &rc.admin
		p.CSRF = s.csrf.Token(rc.sess.ID)
		p.CanWrite = roleAllows(rc.admin.Role, permAll)
		p.CanMessage = roleAllows(rc.admin.Role, permMessaging)
		if n, err := s.q.CountPendingQuestions(r.Context()); err == nil {
			p.Pending = n
		}
	}
	p.Flash = s.takeFlash(w, r)
	return p
}

func (p *page) crumb(name, url string) *page {
	p.Crumbs = append(p.Crumbs, crumb{i18n.T(p.Lang, name), url})
	return p
}

// T translates a message into the page's language.
func (p *page) T(msg string, args ...any) string { return i18n.T(p.Lang, msg, args...) }

// Languages lists the UI languages for the selector.
func (p *page) Languages() []localization {
	var out []localization
	for _, code := range i18n.Languages() {
		out = append(out, localization{code, i18n.Names[code]})
	}
	return out
}

// adminLang negotiates the interface language: the language cookie (shared
// with the contest web server), then the browser's preferences.
func adminLang(r *http.Request) string {
	explicit := ""
	if c, err := r.Cookie("cms_lang"); err == nil {
		explicit = c.Value
	}
	return i18n.Negotiate(explicit, nil, r.Header.Get("Accept-Language"), nil)
}

// adminTr returns a translator into the administrator's language, for
// messages built outside a page (validation, previews).
func adminTr(r *http.Request) func(string, ...any) string {
	lang := adminLang(r)
	return func(msg string, args ...any) string { return i18n.T(lang, msg, args...) }
}

// wrap gives partial templates their data together with the page (for
// translations and the CSRF token).
// printForm is an action button of the print queue.
type printForm struct {
	P                          *page
	ID                         int64
	Action, Label, Class, Site string
}

type wrap struct {
	P *page
	V any
}

func (w wrap) T(msg string, args ...any) string { return w.P.T(msg, args...) }

func (s *Server) render(w http.ResponseWriter, name string, status int, p *page) {
	t := s.pages[name]
	if t == nil {
		s.log.Error("unknown page template", "name", name)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	webkit.Render(w, s.log, t, "layout", status, p)
}

func (s *Server) renderPartial(w http.ResponseWriter, name string, data any) {
	webkit.Render(w, s.log, s.pages["partials"], name, http.StatusOK, data)
}

func (s *Server) errorPage(w http.ResponseWriter, r *http.Request, rc *reqCtx, status int, msg string) {
	if rc != nil && rc.audit != nil {
		rc.audit.skip = true
	}
	if webkit.IsHTMX(r) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(status)
		w.Write([]byte(i18n.TDetail(adminLang(r), msg)))
		return
	}
	p := s.newPage(w, r, rc, http.StatusText(status), "", nil)
	p.Error = i18n.TDetail(p.Lang, msg)
	s.render(w, "error", status, p)
}

// formError re-renders a form page with an error (HTTP 422, not audited).
func (s *Server) formError(w http.ResponseWriter, r *http.Request, rc *reqCtx, name string, p *page, msg string) {
	if rc.audit != nil {
		rc.audit.skip = true
	}
	p.Error = i18n.TDetail(p.Lang, msg)
	s.render(w, name, http.StatusUnprocessableEntity, p)
}

// ---------------------------------------------------------------- flash

// setFlash stores a one-shot message shown by the next page (signed so it
// cannot be forged from outside).
func (s *Server) setFlash(w http.ResponseWriter, msg string) {
	http.SetCookie(w, &http.Cookie{Name: "cms_flash", Value: s.flash.Sign([]byte(msg)), Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.cfg.CookieSecure, MaxAge: 60})
}

func (s *Server) takeFlash(w http.ResponseWriter, r *http.Request) string {
	c, err := r.Cookie("cms_flash")
	if err != nil {
		return ""
	}
	http.SetCookie(w, &http.Cookie{Name: "cms_flash", Value: "", Path: "/", MaxAge: -1})
	b, ok := s.flash.Verify(c.Value)
	if !ok {
		return ""
	}
	return string(b)
}

// done redirects after a successful POST with a flash message (translated
// into the administrator's language, formatted with args).
func (s *Server) done(w http.ResponseWriter, r *http.Request, to, msg string, args ...any) {
	if msg != "" {
		s.setFlash(w, i18n.T(adminLang(r), msg, args...))
	}
	webkit.Redirect(w, r, to)
}

// ---------------------------------------------------------------- template helpers

func (s *Server) funcs() template.FuncMap {
	return template.FuncMap{
		"static": s.static.URL,
		"dt": func(t any) string {
			switch v := t.(type) {
			case time.Time:
				return v.UTC().Format("2006-01-02 15:04:05")
			case *time.Time:
				if v == nil {
					return ""
				}
				return v.UTC().Format("2006-01-02 15:04:05")
			}
			return ""
		},
		// local formats a time for a datetime-local input in loc.
		"local": func(t any, tz string) string {
			loc, err := time.LoadLocation(tz)
			if err != nil {
				loc = time.UTC
			}
			switch v := t.(type) {
			case time.Time:
				return v.In(loc).Format("2006-01-02T15:04:05")
			case *time.Time:
				if v != nil {
					return v.In(loc).Format("2006-01-02T15:04:05")
				}
			}
			return ""
		},
		"inTZ": func(t time.Time, tz string) string {
			loc, err := time.LoadLocation(tz)
			if err != nil {
				loc = time.UTC
			}
			return t.In(loc).Format("2006-01-02 15:04:05 MST")
		},
		"opt": func(v any) string {
			switch x := v.(type) {
			case *int32:
				if x != nil {
					return strconv.Itoa(int(*x))
				}
			case *int64:
				if x != nil {
					return strconv.FormatInt(*x, 10)
				}
			case *string:
				if x != nil {
					return *x
				}
			case *float64:
				if x != nil {
					return strconv.FormatFloat(*x, 'f', -1, 64)
				}
			}
			return ""
		},
		"score": func(v any, precision any) string {
			p := toInt(precision)
			switch x := v.(type) {
			case float64:
				return strconv.FormatFloat(x, 'f', p, 64)
			case *float64:
				if x != nil {
					return strconv.FormatFloat(*x, 'f', p, 64)
				}
			}
			return ""
		},
		"secs": func(v any) string {
			switch x := v.(type) {
			case float64:
				return strconv.FormatFloat(x, 'f', 3, 64) + " s"
			case *float64:
				if x != nil {
					return strconv.FormatFloat(*x, 'f', 3, 64) + " s"
				}
			}
			return ""
		},
		"bytes": func(v any) string {
			var n int64
			switch x := v.(type) {
			case int64:
				n = x
			case *int64:
				if x == nil {
					return ""
				}
				n = *x
			case int:
				n = int64(x)
			default:
				return ""
			}
			switch {
			case n >= 1<<30 && n%(1<<30) == 0:
				return fmt.Sprintf("%d GiB", n>>30)
			case n >= 1<<20:
				return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
			case n >= 1<<10:
				return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
			}
			return fmt.Sprintf("%d B", n)
		},
		"mib": func(v *int64) string {
			if v == nil {
				return ""
			}
			return strconv.FormatInt(*v>>20, 10)
		},
		"kib": func(v *int64) string {
			if v == nil {
				return ""
			}
			return strconv.FormatInt(*v>>10, 10)
		},
		"msToS": func(v *int32) string {
			if v == nil {
				return ""
			}
			return strconv.FormatFloat(float64(*v)/1000, 'f', -1, 64)
		},
		"json": func(v any) string {
			switch x := v.(type) {
			case json.RawMessage:
				var buf strings.Builder
				var anyv any
				if json.Unmarshal(x, &anyv) == nil {
					b, _ := json.MarshalIndent(anyv, "", "  ")
					buf.Write(b)
					return buf.String()
				}
				return string(x)
			}
			b, _ := json.Marshal(v)
			return string(b)
		},
		"join":     func(v []string, sep string) string { return strings.Join(v, sep) },
		"has":      contains,
		"joinHead": joinHead,
		"num":      fmtNum,
		"card": func(q sqlc.AdminListQuestionsRow, d *questionsPage) questionCard {
			return questionCard{Q: q, Quick: d.Quick, CanAnswer: d.CanAnswer}
		},
		"cidrs": formatPrefixes,
		"add":   func(a, b int) int { return a + b },
		"deref": derefStr,
		"ptr64": func(v int64) *int64 { return &v },
		"deref32": func(v *int32) int32 {
			if v == nil {
				return 0
			}
			return *v
		},
		"deref64": func(v *int64) int64 {
			if v == nil {
				return 0
			}
			return *v
		},
		"urlquery": url.QueryEscape,
		"pct": func(a, b int64) string {
			if b == 0 {
				return "0%"
			}
			return strconv.FormatFloat(100*float64(a)/float64(b), 'f', 1, 64) + "%"
		},
		"cmds":  langCommands,
		"split": strings.Fields,
		"args":  func(v []string) string { return strings.Join(v, " ") },
		"div60": func(v *int64) string {
			if v == nil {
				return ""
			}
			return strconv.FormatInt(*v/60, 10)
		},
		// reeval builds the data of a reevaluation form for one scope.
		"reeval": func(p *page, field string, id int64, back string) reevalForm {
			return reevalForm{P: p, CSRF: p.CSRF, Field: field, ID: id, Back: back}
		},
		"part": func(p *page, v any) wrap { return wrap{P: p, V: v} },
		"printForm": func(p *page, id int64, action, label, class, site string) printForm {
			return printForm{P: p, ID: id, Action: action, Label: label, Class: class, Site: site}
		},
		"since": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return time.Since(t).Round(time.Second).String()
		},
	}
}

func toInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int32:
		return int(x)
	case int64:
		return int(x)
	}
	return 0
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func derefStr(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func formatPrefixes(ps []netip.Prefix) string {
	out := make([]string, len(ps))
	for i, p := range ps {
		if p.IsSingleIP() {
			out[i] = p.Addr().String()
		} else {
			out[i] = p.String()
		}
	}
	return strings.Join(out, ", ")
}

func urlQueryEscape(s string) string { return url.QueryEscape(s) }

// reevalForm is the data of the "reevaluate" partial.
type reevalForm struct {
	P                 *page
	CSRF, Field, Back string
	ID                int64
}

func (r reevalForm) T(msg string, args ...any) string { return r.P.T(msg, args...) }
