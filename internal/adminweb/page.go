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
	"github.com/D4ND3R/Contest-Management-System/internal/statement"
	"github.com/D4ND3R/Contest-Management-System/internal/version"
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
	// Contest is the contest the page is about, or the one the admin pages
	// default to (see focusContest); Phase is its phase.
	Contest *sqlc.Contest
	Phase   string
	// Path is the request path (the sidebar marks where the admin is).
	Path string
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
	p.Path = r.URL.Path
	if rc != nil {
		p.Contest = s.pageContest(r, rc)
		if p.Contest != nil {
			p.Phase = contestPhase(*p.Contest, p.ServerTime)
		}
	}
	return p
}

// pageContest is the contest of the page: the one the handler loaded or
// named, else the default one.
func (s *Server) pageContest(r *http.Request, rc *reqCtx) *sqlc.Contest {
	if rc.contest != nil {
		return rc.contest
	}
	if rc.contestID != nil {
		if c, err := s.q.GetContest(r.Context(), *rc.contestID); err == nil {
			return &c
		}
	}
	list, err := s.q.ListContests(r.Context())
	if err != nil {
		return nil
	}
	return focusContest(list, s.now())
}

// CID is the page contest's id as a string ("" without one).
func (p *page) CID() string {
	if p.Contest == nil {
		return ""
	}
	return strconv.FormatInt(p.Contest.ID, 10)
}

// On reports whether the page is at path (or below it, with a trailing
// "/..."); OnC does the same under the page contest's address.
func (p *page) On(path string) bool {
	return p.Path == path || strings.HasPrefix(p.Path, path+"/")
}

func (p *page) OnC(sub string) bool {
	if p.Contest == nil {
		return false
	}
	base := "/contests/" + p.CID()
	if sub == "" {
		return p.Path == base
	}
	return p.On(base + "/" + sub)
}

// ContestHeading is the display title of the page contest.
func (p *page) ContestHeading() string {
	if p.Contest == nil {
		return ""
	}
	return contestHeading(*p.Contest)
}

func contestHeading(c sqlc.Contest) string {
	switch {
	case c.Title != "":
		return c.Title
	case c.Description != "":
		return c.Description
	}
	return c.Name
}

// PhaseClass and PhaseLabel describe the page contest's phase in a pill.
func (p *page) PhaseClass() string {
	switch p.Phase {
	case "running":
		return "ok live"
	case "upcoming":
		return "info"
	}
	return ""
}

func (p *page) PhaseLabel() string {
	switch p.Phase {
	case "running":
		return p.T("Contest in progress")
	case "upcoming":
		return p.T("Not started")
	}
	return p.T("Finished")
}

// EndMillis is the end of the page contest in Unix milliseconds.
func (p *page) EndMillis() int64 {
	if p.Contest == nil {
		return 0
	}
	return p.Contest.StopTime.UnixMilli()
}

// Remaining is the time left in the page contest ("h:mm:ss").
func (p *page) Remaining() string {
	if p.Contest == nil {
		return ""
	}
	d := p.Contest.StopTime.Sub(p.ServerTime)
	if d < 0 {
		d = 0
	}
	sec := int64(d / time.Second)
	return fmt.Sprintf("%d:%02d:%02d", sec/3600, sec/60%60, sec%60)
}

// BackURL is the address of the last breadcrumb.
func (p *page) BackURL() string {
	if len(p.Crumbs) == 0 {
		return "/"
	}
	return p.Crumbs[len(p.Crumbs)-1].URL
}

// RoleLabel names the administrator's role.
func (p *page) RoleLabel() string {
	if p.Admin == nil {
		return ""
	}
	switch p.Admin.Role {
	case "all":
		return p.T("Full access")
	case "messaging":
		return p.T("Messaging")
	case "read_only":
		return p.T("Read-only")
	}
	return p.Admin.Role
}

// ServerMillis is the page's time in Unix milliseconds (countdowns).
func (p *page) ServerMillis() int64 { return p.ServerTime.UnixMilli() }

// Version is the CMS version (sidebar footer).
func (p *page) Version() string { return version.Version }

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
		"inc":   func(a int) int { return a + 1 },
		// stformat names a statement's format; stsource tells a source
		// (edited here) from an uploaded PDF.
		"stformat": func(ct string) string {
			switch statement.Extension(ct) {
			case ".md":
				return "Markdown"
			case ".tex":
				return "LaTeX"
			case ".html":
				return "HTML"
			case ".txt":
				return "Text"
			}
			return "PDF"
		},
		"stsource": statement.IsSource,
		"deref":    derefStr,
		"ptr64":    func(v int64) *int64 { return &v },
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
		// signed writes a score change with its sign (+5, -2.5).
		"signed": func(v float64) string {
			if v > 0 {
				return "+" + strconv.FormatFloat(v, 'f', -1, 64)
			}
			return strconv.FormatFloat(v, 'f', -1, 64)
		},
		"printForm": func(p *page, id int64, action, label, class, site string) printForm {
			return printForm{P: p, ID: id, Action: action, Label: label, Class: class, Site: site}
		},
		"percent": func(v float64) string { return strconv.FormatFloat(v, 'f', 0, 64) + "%" },
		"dur":     func(d time.Duration) string { return d.Round(time.Second).String() },
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

// Hours formats a duration as "5h" or "4h 30m".
func (p *page) Hours(d time.Duration) string {
	m := int64(d.Round(time.Minute) / time.Minute)
	switch {
	case m%60 == 0:
		return strconv.FormatInt(m/60, 10) + "h"
	case m < 60:
		return strconv.FormatInt(m, 10) + "m"
	}
	return strconv.FormatInt(m/60, 10) + "h " + strconv.FormatInt(m%60, 10) + "m"
}

// ContestWhen writes a contest's window compactly in its time zone.
func (p *page) ContestWhen(c sqlc.Contest) string {
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		loc = time.UTC
	}
	a, b := c.StartTime.In(loc), c.StopTime.In(loc)
	s := a.Format("2006-01-02 15:04") + " – "
	if a.YearDay() == b.YearDay() && a.Year() == b.Year() {
		s += b.Format("15:04")
	} else {
		s += b.Format("2006-01-02 15:04")
	}
	return s + " (" + loc.String() + ")"
}
