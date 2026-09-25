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
	"github.com/D4ND3R/Contest-Management-System/internal/webkit"
)

// page is the data of every admin page.
type page struct {
	Title      string
	Admin      *sqlc.Admin
	CSRF       string
	Active     string
	Flash      string
	Error      string
	Crumbs     []crumb
	CanWrite   bool
	CanMessage bool
	Data       any
	ServerTime time.Time
}

type crumb struct{ Name, URL string }

func (s *Server) newPage(w http.ResponseWriter, r *http.Request, rc *reqCtx, title, active string, data any) *page {
	p := &page{Title: title, Active: active, Data: data, ServerTime: s.now()}
	if rc != nil {
		p.Admin = &rc.admin
		p.CSRF = s.csrf.Token(rc.sess.ID)
		p.CanWrite = roleAllows(rc.admin.Role, permAll)
		p.CanMessage = roleAllows(rc.admin.Role, permMessaging)
	}
	p.Flash = s.takeFlash(w, r)
	return p
}

func (p *page) crumb(name, url string) *page {
	p.Crumbs = append(p.Crumbs, crumb{name, url})
	return p
}

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
		w.Write([]byte(msg))
		return
	}
	p := s.newPage(w, r, rc, http.StatusText(status), "", nil)
	p.Error = msg
	s.render(w, "error", status, p)
}

// formError re-renders a form page with an error (HTTP 422, not audited).
func (s *Server) formError(w http.ResponseWriter, r *http.Request, rc *reqCtx, name string, p *page, msg string) {
	if rc.audit != nil {
		rc.audit.skip = true
	}
	p.Error = msg
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

// done redirects after a successful POST with a flash message.
func (s *Server) done(w http.ResponseWriter, r *http.Request, to, msg string) {
	if msg != "" {
		s.setFlash(w, msg)
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
		"join":  func(v []string, sep string) string { return strings.Join(v, sep) },
		"has":   contains,
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
		"reeval": func(csrf, field string, id int64, back string) reevalForm {
			return reevalForm{CSRF: csrf, Field: field, ID: id, Back: back}
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
	CSRF, Field, Back string
	ID                int64
}
