package certificate

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/D4ND3R/Contest-Management-System/internal/pdf"
	"github.com/D4ND3R/Contest-Management-System/internal/ranking"
)

func ranked() *ranking.Ranking {
	return &ranking.Ranking{Contest: "oni", Precision: 0, Tasks: []ranking.Task{{MaxScore: 100}, {MaxScore: 100}},
		Rows: []ranking.Row{
			{Rank: 1, ParticipationID: 11, Username: "ana", FirstName: "Ana", LastName: "Pérez", Institution: "Escuela 1", Total: 200},
			{Rank: 2, ParticipationID: 12, Username: "beto", Total: 150, TeamName: "México"},
			{Rank: 3, ParticipationID: 13, Username: "caro", Total: 150, Hidden: true},
			{Rank: 3, ParticipationID: 14, Username: "dani", Total: 40, TeamInstitution: "Liceo"},
			{Rank: 5, ParticipationID: 15, Username: "eli", Total: 0},
		}}
}

func TestRecipientsAndAwards(t *testing.T) {
	awards, err := ParseAwards("Medalla de oro: 1\nMedalla de plata: 3\n\nMención: 4\n")
	if err != nil {
		t.Fatal(err)
	}
	tp := &Template{Awards: awards}
	rs := tp.Recipients(ranked(), "ONI 2026", "25/09/2026")
	if len(rs) != 4 {
		t.Fatalf("%d recipients (hidden excluded): %+v", len(rs), rs)
	}
	got := []string{}
	for _, r := range rs {
		got = append(got, r.Vars["name"]+"="+r.Vars["award"]+"@"+r.Vars["rank"]+"/"+r.Vars["score"])
	}
	if strings.Join(got, " ") != "Ana Pérez=Medalla de oro@1/200 beto=Medalla de plata@2/150 dani=Medalla de plata@3/40 eli=@5/0" {
		t.Fatalf("got %v", got)
	}
	if rs[0].Vars["participants"] != "4" || rs[0].Vars["max_score"] != "200" || rs[2].Vars["institution"] != "Liceo" || rs[1].Vars["team"] != "México" {
		t.Fatalf("vars %+v", rs)
	}
	min := 100.0
	tp.MinScore = &min
	if n := len(tp.Recipients(ranked(), "", "")); n != 2 {
		t.Fatalf("min score: %d", n)
	}
	tp.MinScore, tp.OnlyAwarded = nil, true
	if n := len(tp.Recipients(ranked(), "", "")); n != 3 {
		t.Fatalf("only awarded: %d", n)
	}
	for _, bad := range []string{"Oro", "Oro: x", "Oro: 3\nPlata: 2", ": 3", "Oro: 0"} {
		if _, err := ParseAwards(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if s := FormatAwards(awards); s != "Medalla de oro: 1\nMedalla de plata: 3\nMención: 4\n" {
		t.Fatalf("format %q", s)
	}
	sigs := ParseSignatures("Dra. Ruiz | Presidenta\n\nIng. Soto\n")
	if len(sigs) != 2 || sigs[0].Role != "Presidenta" || sigs[1].Role != "" || FormatSignatures(sigs) != "Dra. Ruiz | Presidenta\nIng. Soto\n" {
		t.Fatalf("signatures %+v", sigs)
	}
}

func TestFill(t *testing.T) {
	vars := map[string]string{"name": "Ana", "rank": "1"}
	if got := Fill("{name} ({rank}) {unknown} {name", vars); got != "Ana (1) {unknown} {name" {
		t.Fatalf("%q", got)
	}
}

func TestRender(t *testing.T) {
	tp := &Template{
		Title:      "Certificado",
		Body:       "Se otorga a\n\n# {name}\n\npor su participación en {contest}, puesto {rank} de {participants}, con una frase larga que obliga a partir el párrafo en varias líneas para que quepa dentro del margen del certificado.\n\n## {award}",
		Footer:     "{date}",
		Signatures: []Signature{{Name: "Dra. Ruiz", Role: "Presidenta"}, {Name: "Ing. Soto"}},
	}
	awards, _ := ParseAwards("Medalla de oro: 1")
	tp.Awards = awards
	img := image.NewNRGBA(image.Rect(0, 0, 40, 20))
	for x := 0; x < 40; x++ {
		img.Set(x, x/2, color.NRGBA{200, 0, 0, 255})
	}
	var pngData bytes.Buffer
	png.Encode(&pngData, img)
	doc := pdf.New()
	logo, err := doc.AddImage(pngData.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range tp.Recipients(ranked(), "la ONI", "25 de septiembre de 2026") {
		tp.Render(doc, r, logo)
	}
	b := doc.Bytes()
	if n, err := pdf.CountPages(b); err != nil || n != 4 {
		t.Fatalf("pages = %d, %v", n, err)
	}
	s := string(b)
	for _, want := range []string{"(Certificado)", "(Ana P\xe9rez)", "(Medalla de oro)", "(Dra. Ruiz)", "(Presidenta)", "(25 de septiembre de 2026)", "/Im1 Do", "/Subtype /Image"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
	// "## {award}" disappears for contestants without an award.
	if strings.Count(s, "(Medalla de oro)") != 1 {
		t.Fatalf("award lines: %d", strings.Count(s, "(Medalla de oro)"))
	}
}
