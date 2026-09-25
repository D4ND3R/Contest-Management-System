// Package certificate renders participation certificates from a contest's
// template: one A4 landscape page per contestant with a title, paragraphs
// with placeholders ({name}, {rank}, {award}, …), an optional logo,
// signature lines and a footer. Awards (medals, mentions) are given by
// rank.
package certificate

import (
	"strconv"
	"strings"

	"github.com/D4ND3R/Contest-Management-System/internal/pdf"
	"github.com/D4ND3R/Contest-Management-System/internal/ranking"
)

// Signature is one signature line.
type Signature struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

// Award is given to the contestants ranked up to UpToRank (after the
// previous awards).
type Award struct {
	Name     string `json:"name"`
	UpToRank int    `json:"up_to_rank"`
}

// Template is a contest's certificate design.
type Template struct {
	Title      string
	Body       string
	Footer     string
	Signatures []Signature
	Awards     []Award
	// MinScore, when set, leaves out contestants below it.
	MinScore *float64
	// OnlyAwarded leaves out contestants without an award.
	OnlyAwarded bool
}

// Recipient is one certificate.
type Recipient struct {
	ParticipationID int64
	Vars            map[string]string
}

// Placeholders lists the variables a template may use.
var Placeholders = []string{"name", "first_name", "last_name", "username", "institution", "team", "site",
	"contest", "rank", "participants", "score", "max_score", "award", "date"}

// ParseSignatures reads "Name | Role" lines.
func ParseSignatures(s string) []Signature {
	var out []Signature
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, role, _ := strings.Cut(line, "|")
		out = append(out, Signature{Name: strings.TrimSpace(name), Role: strings.TrimSpace(role)})
	}
	return out
}

// FormatSignatures is the inverse of ParseSignatures.
func FormatSignatures(sigs []Signature) string {
	var b strings.Builder
	for _, s := range sigs {
		b.WriteString(s.Name)
		if s.Role != "" {
			b.WriteString(" | " + s.Role)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// ParseAwards reads "Name: last rank" lines; the ranks must grow.
func ParseAwards(s string) ([]Award, error) {
	var out []Award
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		i := strings.LastIndex(line, ":")
		if i < 0 {
			return nil, &AwardLineError{Line: line}
		}
		n, err := strconv.Atoi(strings.TrimSpace(line[i+1:]))
		name := strings.TrimSpace(line[:i])
		if err != nil || n <= 0 || name == "" || len(out) > 0 && n <= out[len(out)-1].UpToRank {
			return nil, &AwardLineError{Line: line}
		}
		out = append(out, Award{Name: name, UpToRank: n})
	}
	return out, nil
}

// AwardLineError is an award line that is not "Name: last rank" with a
// rank above the previous line's.
type AwardLineError struct{ Line string }

func (e *AwardLineError) Error() string {
	return "invalid award line " + strconv.Quote(e.Line) + ": use \"Name: last rank\", ranks growing"
}

// FormatAwards is the inverse of ParseAwards.
func FormatAwards(as []Award) string {
	var b strings.Builder
	for _, a := range as {
		b.WriteString(a.Name + ": " + strconv.Itoa(a.UpToRank) + "\n")
	}
	return b.String()
}

// AwardFor returns the award of a rank ("" for none).
func (t *Template) AwardFor(rank int) string {
	for _, a := range t.Awards {
		if rank <= a.UpToRank {
			return a.Name
		}
	}
	return ""
}

// Recipients lists the certificates of a ranking (hidden contestants are
// never in it) in rank order.
func (t *Template) Recipients(rk *ranking.Ranking, contest, date string) []Recipient {
	var maxScore float64
	for _, task := range rk.Tasks {
		maxScore += task.MaxScore
	}
	participants := 0
	for _, row := range rk.Rows {
		if !row.Hidden {
			participants++
		}
	}
	var out []Recipient
	for _, row := range rk.Rows {
		if row.Hidden {
			continue
		}
		award := t.AwardFor(row.Rank)
		if t.MinScore != nil && row.Total < *t.MinScore || t.OnlyAwarded && award == "" {
			continue
		}
		name := strings.TrimSpace(row.FirstName + " " + row.LastName)
		if name == "" {
			name = row.Username
		}
		inst := row.Institution
		if inst == "" {
			inst = row.TeamInstitution
		}
		score := ranking.FormatScore(row.Total, rk.Precision)
		if rk.ICPC {
			score = strconv.Itoa(row.Solved)
		}
		out = append(out, Recipient{ParticipationID: row.ParticipationID, Vars: map[string]string{
			"name": name, "first_name": row.FirstName, "last_name": row.LastName, "username": row.Username,
			"institution": inst, "team": row.TeamName, "site": row.Site, "contest": contest,
			"rank": strconv.Itoa(row.Rank), "participants": strconv.Itoa(participants), "score": score,
			"max_score": ranking.FormatScore(maxScore, rk.Precision), "award": award, "date": date,
		}})
	}
	return out
}

// Fill replaces the placeholders of s.
func Fill(s string, vars map[string]string) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '{')
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		j := strings.IndexByte(s[i:], '}')
		if j < 0 {
			b.WriteString(s)
			return b.String()
		}
		key := s[i+1 : i+j]
		if v, ok := vars[key]; ok {
			b.WriteString(s[:i])
			b.WriteString(v)
		} else {
			b.WriteString(s[:i+j+1])
		}
		s = s[i+j+1:]
	}
}

// wrap splits s into lines at most width points wide.
func wrap(s string, size float64, bold bool, width float64) []string {
	var lines []string
	var cur string
	for _, w := range strings.Fields(s) {
		next := w
		if cur != "" {
			next = cur + " " + w
		}
		if cur != "" && pdf.Width(next, size, bold) > width {
			lines = append(lines, cur)
			next = w
		}
		cur = next
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// Render adds the certificate of r to doc; logo may be nil.
func (t *Template) Render(doc *pdf.Doc, r Recipient, logo *pdf.Image) {
	p := doc.AddLandscapePage()
	w, h := p.Size()
	p.Rect(20, 20, w-40, h-40, 2)
	p.Rect(28, 28, w-56, h-56, 0.5)
	cx := w / 2
	y := h - 70
	if logo != nil {
		lh := 70.0
		lw := lh * float64(logo.Width) / float64(logo.Height)
		if lw > 220 {
			lw, lh = 220, 220*float64(logo.Height)/float64(logo.Width)
		}
		p.Image(logo, cx-lw/2, y-lh, lw, lh)
		y -= lh + 20
	} else {
		y -= 30
	}
	title := Fill(t.Title, r.Vars)
	for _, line := range wrap(title, 34, true, w-160) {
		y -= 34
		p.TextCentered(cx, y, 34, true, line)
	}
	y -= 26
	for _, para := range strings.Split(strings.ReplaceAll(t.Body, "\r\n", "\n"), "\n\n") {
		para = strings.TrimSpace(para)
		size, bold := 15.0, false
		switch {
		case strings.HasPrefix(para, "# "):
			para, size, bold = para[2:], 26, true
		case strings.HasPrefix(para, "## "):
			para, size, bold = para[3:], 19, true
		}
		text := strings.Join(strings.Fields(Fill(para, r.Vars)), " ")
		if text == "" {
			continue
		}
		for _, line := range wrap(text, size, bold, w-200) {
			y -= size * 1.3
			p.TextCentered(cx, y, size, bold, line)
		}
		y -= size * 0.6
	}
	if n := len(t.Signatures); n > 0 {
		col := (w - 120) / float64(n)
		for i, s := range t.Signatures {
			x := 60 + col*(float64(i)+0.5)
			p.Line(x-80, 112, x+80, 112, 0.7)
			p.TextCentered(x, 96, 12, true, Fill(s.Name, r.Vars))
			if s.Role != "" {
				p.TextCentered(x, 82, 10, false, Fill(s.Role, r.Vars))
			}
		}
	}
	if f := strings.TrimSpace(Fill(t.Footer, r.Vars)); f != "" {
		p.TextCentered(cx, 46, 9, false, f)
	}
}
