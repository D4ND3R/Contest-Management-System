package ranking

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/D4ND3R/Contest-Management-System/internal/pdf"
)

// PDFLabels are the words of the printable ranking (translated by the
// caller).
type PDFLabels struct {
	Title, Rank, Contestant, Team, Total, Solved, Penalty, Page string
}

// WritePDF writes the ranking as a printable A4 landscape table: rank,
// contestant, team or institution, one column per task, total (solved and
// penalty in ICPC mode), with the header repeated on every page.
func (r *Ranking) WritePDF(w io.Writer, l PDFLabels) error {
	const (
		margin = 30.0
		size   = 8.5
		rowH   = 13.0
	)
	d := pdf.New()
	d.Title = l.Title
	type col struct {
		title string
		w     float64
		right bool
	}
	cols := []col{{l.Rank, 28, true}, {l.Contestant, 170, false}, {l.Team, 110, false}}
	tail := []col{{l.Total, 46, true}}
	if r.ICPC {
		tail = []col{{l.Solved, 58, true}, {l.Penalty, 70, true}}
	}
	width := pdf.A4Height - 2*margin
	fixed := 0.0
	for _, c := range append(append([]col(nil), cols...), tail...) {
		fixed += c.w
	}
	taskW := 34.0
	if n := len(r.Tasks); n > 0 {
		taskW = min(60, max(24, (width-fixed)/float64(n)))
	}
	for _, t := range r.Tasks {
		cols = append(cols, col{t.Name, taskW, true})
	}
	cols = append(cols, tail...)

	usable := pdf.A4Width - 2*margin - 50 // landscape height minus the title
	perPage := int(usable / rowH)
	// The column that ranks: the total, or the problems solved.
	bold := len(cols) - 1
	if r.ICPC {
		bold = len(cols) - 2
	}
	pages := max(1, (len(r.Rows)+perPage-1)/perPage)
	for p := 0; p < pages; p++ {
		pg := d.AddLandscapePage()
		pw, ph := pg.Size()
		y := ph - margin
		pg.Text(margin, y, 13, true, pdf.Fit(l.Title, pw-2*margin-120, 13, true))
		pg.TextRight(pw-margin, y, size, false, fmt.Sprintf("%s %d / %d", l.Page, p+1, pages))
		y -= 12
		pg.Text(margin, y, size, false, r.Generated.UTC().Format("2006-01-02 15:04 UTC"))
		y -= 18
		// Header.
		x := margin
		for _, c := range cols {
			text := pdf.Fit(c.title, c.w-4, size, true)
			if c.right {
				pg.TextRight(x+c.w-2, y, size, true, text)
			} else {
				pg.Text(x+2, y, size, true, text)
			}
			x += c.w
		}
		pg.Line(margin, y-4, x, y-4, 0.8)
		y -= rowH + 2
		for i := p * perPage; i < min((p+1)*perPage, len(r.Rows)); i++ {
			row := r.Rows[i]
			if i%2 == 1 {
				pg.FillRect(margin, y-3.5, x-margin, rowH, 0.93)
			}
			cells := []string{strconv.Itoa(row.Rank), contestantName(row), teamOf(row)}
			for k, cell := range row.Cells {
				cells = append(cells, pdfCell(cell, r.Tasks[k], r.ICPC))
			}
			if r.ICPC {
				cells = append(cells, strconv.Itoa(row.Solved), strconv.Itoa(row.Penalty))
			} else {
				cells = append(cells, formatScore(row.Total, r.Precision))
			}
			cx := margin
			for k, c := range cols {
				text := pdf.Fit(cells[k], c.w-4, size, k == bold)
				if c.right {
					pg.TextRight(cx+c.w-2, y, size, k == bold, text)
				} else {
					pg.Text(cx+2, y, size, false, text)
				}
				cx += c.w
			}
			y -= rowH
		}
	}
	return d.Write(w)
}

func contestantName(row Row) string {
	name := strings.TrimSpace(row.FirstName + " " + row.LastName)
	if name == "" {
		return row.Username
	}
	return name + " (" + row.Username + ")"
}

func teamOf(row Row) string {
	switch {
	case row.TeamName != "":
		return row.TeamName
	case row.TeamCode != "":
		return row.TeamCode
	case row.Institution != "":
		return row.Institution
	}
	return row.TeamInstitution
}

// pdfCell is a task cell: the score, or in ICPC mode "+" with the rejected
// attempts and the minute ("+2 45'") or "-n" for rejected attempts only.
func pdfCell(c Cell, t Task, icpc bool) string {
	if !c.Submitted {
		return ""
	}
	if !icpc {
		return formatScore(c.Score, t.Precision)
	}
	if c.Solved {
		s := "+"
		if c.Attempts > 0 {
			s += strconv.Itoa(c.Attempts)
		}
		return s + " " + strconv.Itoa(c.SolvedMinute) + "'"
	}
	if c.Attempts > 0 {
		return "-" + strconv.Itoa(c.Attempts)
	}
	return ""
}
