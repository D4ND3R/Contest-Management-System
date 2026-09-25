package adminweb

import (
	"fmt"
	"github.com/D4ND3R/Contest-Management-System/internal/backup"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/i18n"
	"github.com/D4ND3R/Contest-Management-System/internal/problempkg"
	"github.com/D4ND3R/Contest-Management-System/internal/webtest"
	"github.com/D4ND3R/Contest-Management-System/web"
)

// messageArgs maps the functions that show a message to the administrator
// to the position of that message among their arguments.
var messageArgs = map[string]int{
	"done": 3, "errorPage": 4, "formError": 5, "newPage": 3, "crumb": 0, "fail": 0, "tr": 0,
	// Form fields: the label.
	"required": 1, "int64": 1, "nonNeg": 1, "int32": 1, "optInt64": 1, "optInt32": 1, "optPositive64": 1,
	"float": 1, "secondsToMs": 1, "mib": 1, "kib": 1, "optTime": 1, "time": 1, "prefixes": 1, "oneOf": 1, "identifier": 1,
}

// TestMessagesTranslated checks that every literal message the admin server
// shows (flash notices, error pages, form errors and field labels, page
// titles) has a Spanish translation.
func TestMessagesTranslated(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	missing := map[string]string{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			var fn string
			switch x := call.Fun.(type) {
			case *ast.SelectorExpr:
				fn = x.Sel.Name
			case *ast.Ident:
				fn = x.Name
			}
			i, ok := messageArgs[fn]
			if !ok || i >= len(call.Args) {
				return true
			}
			if key := messageKey(call.Args[i]); key != "" && key != "%v" && key != "%s" && !i18n.Has("es", key) {
				missing[key] = fset.Position(call.Pos()).String()
			}
			return true
		})
	}
	keys := make([]string, 0, len(missing))
	for k := range missing {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Errorf("%s: %q has no Spanish translation", missing[k], k)
	}
}

// messageKey returns the catalog key of a message argument: a string
// literal, or the "Key:" part of `"Key: " + detail`.
func messageKey(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind == token.STRING {
			s, _ := strconv.Unquote(x.Value)
			return s
		}
	case *ast.BinaryExpr:
		if x.Op == token.ADD {
			if s := messageKey(x.X); strings.HasSuffix(s, ": ") {
				return strings.TrimSuffix(s, " ")
			}
		}
	}
	return ""
}

// TestDynamicKeysTranslated covers values the templates translate
// indirectly ({{$.T .Status}} and similar).
func TestDynamicKeysTranslated(t *testing.T) {
	keys := []string{"compile", "evaluate", "usertest", "background", // queue priorities
		"upcoming", "running", "finished", // contest phases
		"create", "update", // import actions
		"system error", "compiling", "compilation failed", "scored", "binary file", // statuses and notes
		"the points must be a non-negative number", "the number of testcases must be a positive integer", // score editor
		"choose at least one testcase", "write a regular expression", "the threshold must be a number", "GroupThreshold needs a threshold",
		"public", "contestants", "admins", "hidden", // ranking visibility
		backup.KindScheduled, backup.KindManual, backup.KindCLI, "external", "Kind", "done", "failed", // backups
		"Bad Request", "Unauthorized", "Forbidden", "Not Found", "Method Not Allowed", "Conflict",
		"Request Entity Too Large", "Unprocessable Entity", "Too Many Requests", "Internal Server Error"}
	for _, v := range problempkg.Verdicts {
		keys = append(keys, v)
	}
	for _, st := range statusFilters {
		keys = append(keys, st)
	}
	for _, k := range keys {
		if !i18n.Has("es", k) {
			t.Errorf("%q has no Spanish translation", k)
		}
	}
	// And the templates themselves (the contest web server test walks them
	// too; this keeps the admin package self-checking).
	re := regexp.MustCompile(`\.T "([^"]+)"`)
	entries, err := fs.ReadDir(web.Templates, "aws")
	if err != nil || len(entries) == 0 {
		t.Fatalf("admin templates: %v", err)
	}
	for _, e := range entries {
		b, err := fs.ReadFile(web.Templates, "aws/"+e.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			if !i18n.Has("es", m[1]) {
				t.Errorf("%s: %q has no Spanish translation", e.Name(), m[1])
			}
		}
	}
}

// TestAdminInSpanish switches the interface language and checks pages,
// form errors and flash notices.
func TestAdminInSpanish(t *testing.T) {
	f := newFixture(t)
	b := f.login("all")
	code, body := b.Post("/lang", url.Values{"lang": {"es"}})
	webtest.MustOK(t, "lang", code, body)
	id := func(v int64) string { return fmt.Sprint(v) }
	for path, want := range map[string][]string{
		"/":                             {`<html lang="es">`, "Resumen", "Cerrar sesión"},
		"/contests/" + id(f.contest.ID): {"Guardar", "Reevaluar todo el concurso"},
		"/tasks/" + id(f.task.ID):       {"Enunciados", "Probador de problemas", "Límites de envíos"},
		"/datasets/" + id(f.ds.ID):      {"Límite de tiempo (s)", "Casos de prueba", "Puntaje máximo"},
		"/submissions/" + id(f.subs[0]): {"Resultados", "Oficial"},
		"/users/" + id(f.user.ID):       {"Participaciones", "Sesiones"},
		"/system":                       {"Workers y colas", "workers activos"},
		"/contests/" + id(f.contest.ID) + "/stats": {"puntaje completo"},
		"/backups": {"Respaldar ahora", "Rotación", "Todavía no hay respaldos."},
	} {
		code, body := b.Get(path)
		webtest.MustOK(t, path, code, body)
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Errorf("%s lacks %q", path, w)
			}
		}
	}
	// Form errors translate the message and the field label.
	code, body = b.Post("/datasets/"+id(f.ds.ID), url.Values{"description": {"x"}, "time_limit": {"abc"}})
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "Límite de tiempo debe ser un número") {
		t.Fatalf("form error = %d\n%s", code, body)
	}
	// Flash notices.
	code, body = b.PostMultipart("/teams/"+id(f.team.ID), map[string]string{"code": f.team.Code, "name": "Otro"})
	if code != 200 || !strings.Contains(body, "Equipo guardado.") {
		t.Fatalf("flash = %d\n%s", code, body)
	}
	// An unknown language is ignored.
	b.Post("/lang", url.Values{"lang": {"xx"}})
	if _, body := b.Get("/"); !strings.Contains(body, "Resumen") {
		t.Fatal("unknown language replaced the choice")
	}
	b.Post("/lang", url.Values{"lang": {"en"}})
	if _, body := b.Get("/"); !strings.Contains(body, "Overview") {
		t.Fatal("back to English")
	}
}
