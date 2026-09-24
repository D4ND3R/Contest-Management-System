package i18n

import "testing"

func TestTranslate(t *testing.T) {
	if T("es", "Submit") != "Enviar" || T("en", "Submit") != "Submit" || T("fr", "Submit") != "Submit" {
		t.Fatal("basic translation")
	}
	if got := T("es", "%d of %d testcases", 3, 10); got != "3 de 10 casos" {
		t.Fatalf("formatted: %q", got)
	}
}

func TestNegotiate(t *testing.T) {
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
		{"", nil, "fr-FR, de", nil, "en"},
		{"", nil, "es", []string{"en"}, "en"},
		{"es", nil, "", []string{"en"}, "en"},
		{"", nil, "", []string{"es"}, "es"},
	}
	for _, c := range cases {
		if got := Negotiate(c.explicit, c.pref, c.accept, c.allowed); got != c.want {
			t.Errorf("Negotiate(%q, %v, %q, %v) = %q, want %q", c.explicit, c.pref, c.accept, c.allowed, got, c.want)
		}
	}
}
