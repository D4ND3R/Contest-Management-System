package pdf

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
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

func TestImages(t *testing.T) {
	d := New()
	// PNG with transparency: composed over white.
	nrgba := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	nrgba.Set(0, 0, color.NRGBA{255, 0, 0, 255})
	nrgba.Set(1, 0, color.NRGBA{0, 0, 0, 0})
	var pngData bytes.Buffer
	png.Encode(&pngData, nrgba)
	imPNG, err := d.AddImage(pngData.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	var jpg, gray bytes.Buffer
	jpeg.Encode(&jpg, image.NewRGBA(image.Rect(0, 0, 4, 4)), nil)
	jpeg.Encode(&gray, image.NewGray(image.Rect(0, 0, 5, 2)), nil)
	imJPG, err := d.AddImage(jpg.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	imGray, err := d.AddImage(gray.Bytes())
	if err != nil || imGray.Width != 5 || imGray.Height != 2 {
		t.Fatalf("gray: %+v %v", imGray, err)
	}
	for i := 0; i < 2; i++ {
		p := d.AddLandscapePage()
		p.Image(imPNG, 10, 10, 30, 20)
		p.Image(imJPG, 50, 10, 40, 40)
		p.Image(imGray, 100, 10, 50, 20)
	}
	b := d.Bytes()
	checkXref(t, b)
	s := string(b)
	if strings.Count(s, "/Subtype /Image") != 3 || strings.Count(s, "/Im1 Do") != 2 || !strings.Contains(s, "/Im3 Do") ||
		strings.Count(s, "/Filter /DCTDecode") != 2 || !strings.Contains(s, "/ColorSpace /DeviceGray") {
		t.Fatalf("image objects:\n%s", regexp.MustCompile(`[^\x20-\x7e\n]`).ReplaceAllString(s, "."))
	}
	if !bytes.Contains(b, jpg.Bytes()) {
		t.Fatal("the JPEG is not embedded as is")
	}
	// The PNG pixels: red, then transparent → white.
	m := regexp.MustCompile(`(?s)/FlateDecode /Length (\d+) >>\nstream\n`).FindSubmatchIndex(b)
	n, _ := strconv.Atoi(string(b[m[2]:m[3]]))
	zr, err := zlib.NewReader(bytes.NewReader(b[m[1] : m[1]+n]))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(zr)
	if len(raw) != 3*2*3 || !bytes.Equal(raw[:6], []byte{255, 0, 0, 255, 255, 255}) {
		t.Fatalf("pixels % x", raw)
	}
	if _, err := d.AddImage([]byte("not an image")); err == nil {
		t.Fatal("garbage accepted")
	}
	// A header announcing 5000×5000 pixels is refused before decoding.
	var hdr bytes.Buffer
	hdr.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := []byte("IHDR\x00\x00\x13\x88\x00\x00\x13\x88\x08\x02\x00\x00\x00")
	binary.Write(&hdr, binary.BigEndian, uint32(len(ihdr)-4))
	hdr.Write(ihdr)
	binary.Write(&hdr, binary.BigEndian, crc32.ChecksumIEEE(ihdr))
	if _, err := d.AddImage(hdr.Bytes()); err != ErrImageTooLarge {
		t.Fatalf("huge image: %v", err)
	}
}

// checkXref checks that every xref offset points at its object.
func checkXref(t *testing.T, b []byte) {
	t.Helper()
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
}
