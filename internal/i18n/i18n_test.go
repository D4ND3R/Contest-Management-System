package i18n

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestTranslate(t *testing.T) {
	if T("es", "Submit") != "Enviar" || T("en", "Submit") != "Submit" || T("zz", "Submit") != "Submit" {
		t.Fatal("basic translation")
	}
	if got := T("es", "%d of %d testcases", 3, 10); got != "3 de 10 casos" {
		t.Fatalf("formatted: %q", got)
	}
	// Translations that reorder the arguments (%[2]s).
	for _, c := range []struct{ lang, want string }{
		{"tr", "40 puandan itibaren 3"},
		{"ja", "40 点以上：3 人"},
		{"fr", "3 à partir de 40 points"},
	} {
		if got := T(c.lang, "%d from %s points", 3, "40"); got != c.want {
			t.Errorf("%s: %q", c.lang, got)
		}
	}
	if got := T("ko", "Your position: %d of %d.", 2, 10); got != "내 순위: 10명 중 2위." {
		t.Errorf("ko: %q", got)
	}
}

func TestNegotiate(t *testing.T) {
	catalogs["pt-br"] = map[string]string{}
	defer delete(catalogs, "pt-br")
	cases := []struct {
		explicit string
		pref     []string
		accept   string
		allowed  []string
		want     string
	}{
		{"", nil, "es-MX,es;q=0.9,en;q=0.8", nil, "es"},
		{"en", nil, "es-MX", nil, "en"},
		{"", []string{"es"}, "en-US", nil, "es"},
		{"", nil, "zz-ZZ, qq", nil, "en"},
		{"", nil, "es", []string{"en"}, "en"},
		{"es", nil, "", []string{"en"}, "en"},
		{"", nil, "", []string{"es"}, "es"},
		{"", nil, "pt-BR,pt;q=0.9", nil, "pt-br"}, // a regional language when it exists
		{"", nil, "es_AR", nil, "es"},             // else the base language
	}
	for _, c := range cases {
		if got := Negotiate(c.explicit, c.pref, c.accept, c.allowed); got != c.want {
			t.Errorf("Negotiate(%q, %v, %q, %v) = %q, want %q", c.explicit, c.pref, c.accept, c.allowed, got, c.want)
		}
	}
}

func TestSameVerbs(t *testing.T) {
	for _, c := range []struct {
		a, b string
		ok   bool
	}{
		{"Pages: %d of %d used.", "Páginas: %d de %d.", true},
		{"%d from %s points", "%[2]s puntos: %[1]d", true}, // reordered with indexes
		{"%d from %s points", "%s de %d puntos", false},    // swapped types
		{"Example %d", "Ejemplo", false},                   // an argument dropped
		{"100%% done", "100%% listo", true},
		{"Tokens: %d.", "Fichas: %d. %s", false},
		{"no verbs", "sin verbos", true},
		{"%d", "%[3]d", false},
	} {
		if got := SameVerbs(c.a, c.b); got != c.ok {
			t.Errorf("SameVerbs(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestRegisterAndLoadDir(t *testing.T) {
	defer func() {
		delete(catalogs, "qx")
		delete(Names, "qx")
		delete(dirs, "qx")
	}()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "qx.yaml"), []byte(`name: Qxish
dir: rtl
messages:
  "Submit": "Qsubmit"
  "Example %d": "Qexample"
  "Log in": ""
`), 0o644)
	warnings, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "Example %d") {
		t.Fatalf("warnings %v", warnings)
	}
	if T("qx", "Submit") != "Qsubmit" || T("qx", "Example %d", 2) != "Example 2" || T("qx", "Log in") != "Log in" {
		t.Fatal("loaded catalog")
	}
	if Dir("qx") != "rtl" || Names["qx"] != "Qxish" || !slices.Contains(Languages(), "qx") || slices.Contains(AdminLanguages(), "qx") {
		t.Fatal("metadata")
	}
	if tr, total := Coverage("qx"); tr != 1 || total != len(ContestantMessages()) {
		t.Fatalf("coverage %d/%d", tr, total)
	}
	// A file can correct a shipped language without losing the rest.
	os.WriteFile(filepath.Join(dir, "es.yaml"), []byte("name: Español\nmessages:\n  \"Submit\": \"Mandar\"\n"), 0o644)
	defer func() { es["Submit"] = "Enviar" }()
	if _, err := LoadDir(dir); err != nil {
		t.Fatal(err)
	}
	if T("es", "Submit") != "Mandar" || T("es", "Log in") != "Iniciar sesión" {
		t.Fatalf("override: %q %q", T("es", "Submit"), T("es", "Log in"))
	}
	for _, bad := range []string{"name: \"\"\n", "name: X\ndir: up\n", "name: X\ncolour: red\n"} {
		if _, err := Register("qy", []byte(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if _, err := Register("../x", []byte("name: X\n")); err == nil {
		t.Error("accepted a path as a code")
	}
}

func TestDir(t *testing.T) {
	for lang, want := range map[string]string{"ar": "rtl", "fa_IR": "rtl", "he-IL": "rtl", "en": "ltr", "es": "ltr", "ja": "ltr", "ur": "rtl"} {
		if got := Dir(lang); got != want {
			t.Errorf("Dir(%q) = %s", lang, got)
		}
	}
}
