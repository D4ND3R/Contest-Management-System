package pdf

import (
	"bytes"
	"compress/zlib"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestDocumentStructure(t *testing.T) {
	d := New()
	d.Title = "Credenciales (OMI)"
	for i := 0; i < 3; i++ {
		p := d.AddPage()
		p.Text(50, 800, 12, false, "Lucía Gómez — ¿Año?")
		p.TextCentered(A4Width/2, 700, 20, true, "Contraseña: (x)\\y")
		p.Rect(40, 40, 100, 50, 1)
		p.DashedLine(0, 421, A4Width, 421, 0.5)
		p.FillRect(10, 10, 5, 5, 0.9)
	}
	b := d.Bytes()
	if !bytes.HasPrefix(b, []byte("%PDF-1.4")) || !bytes.HasSuffix(b, []byte("%%EOF\n")) {
		t.Fatal("not a PDF")
	}
	// Every xref offset points at the start of its object.
	m := regexp.MustCompile(`startxref\n(\d+)`).FindSubmatch(b)
	if m == nil {
		t.Fatal("no startxref")
	}
	xref, _ := strconv.Atoi(string(m[1]))
	lines := strings.Split(string(b[xref:]), "\n")
	count, _ := strconv.Atoi(strings.Fields(lines[1])[1])
	for i := 1; i < count; i++ {
		off, _ := strconv.Atoi(strings.Fields(lines[2+i])[0])
		if want := strconv.Itoa(i) + " 0 obj"; !bytes.HasPrefix(b[off:], []byte(want)) {
			t.Fatalf("object %d: offset %d points at %q", i, off, b[off:off+10])
		}
	}
	if !bytes.Contains(b, []byte("/Count 3")) {
		t.Fatal("page count")
	}
	// WinAnsi: í is 0xED, — is 0x97; parentheses and backslashes escaped.
	if !bytes.Contains(b, []byte("Luc\xeda G\xf3mez \x97 \xbfA\xf1o?")) || !bytes.Contains(b, []byte(`\(x\)\\y`)) {
		t.Fatal("text encoding")
	}
}

func TestWidthAndFit(t *testing.T) {
	if w := Width("MMMM", 10, false); w != 33.32 {
		t.Fatalf("width %v", w)
	}
	if Width("Ñandú", 10, true) != Width("Nandu", 10, true) {
		t.Fatal("accented letters must use the base letter width")
	}
	s := Fit("A very long institution name that does not fit", 100, 10, false)
	if Width(s, 10, false) > 100 || !strings.HasSuffix(s, "…") {
		t.Fatalf("fit %q", s)
	}
}

func TestCountPages(t *testing.T) {
	d := New()
	for i := 0; i < 3; i++ {
		d.AddPage().Mono(40, 800, 9, "int main() { return 0; }")
	}
	if n, err := CountPages(d.Bytes()); n != 3 || err != nil {
		t.Fatalf("own document: %d %v", n, err)
	}
	// A page tree inside a compressed object stream, an outline whose
	// /Count is larger, a nested dictionary and a string with ">>" in it.
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	zw.Write([]byte("2 0 3 60 << /Type /Pages /Kids [4 0 R 5 0 R] /Resources << /Font << >> >> /Count 7 >>\n<< /Type /Outlines /First 9 0 R /Count 40 >>"))
	zw.Close()
	doc := "%PDF-1.5\n%\xe2\xe3\xcf\xd3\n1 0 obj << /Type /Catalog /Pages 2 0 R /Title (a >> b) >> endobj\n" +
		"6 0 obj << /Type /ObjStm /N 2 /First 10 /Filter /FlateDecode /Length " + strconv.Itoa(z.Len()) + " >>\nstream\n" +
		z.String() + "\nendstream\nendobj\n%%EOF\n"
	if n, err := CountPages([]byte(doc)); n != 7 || err != nil {
		t.Fatalf("object stream: %d %v", n, err)
	}
	for _, bad := range []string{"hello", "%PDF-1.4\n1 0 obj << /Type /Catalog >> endobj", "%PDF-1.4\n<< /Type /Pages /Count 3 >>"} {
		if _, err := CountPages([]byte(bad)); err == nil {
			t.Errorf("%q: counted", bad)
		}
	}
}
