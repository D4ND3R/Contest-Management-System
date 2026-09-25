// Package printing turns contestants' print requests into PDF documents
// (plain text is typeset here; PDFs are printed as they are) and runs the
// print service that sends them to CUPS with a banner page.
package printing

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/D4ND3R/Contest-Management-System/internal/pdf"
)

// MsgCancelled is the status text of a job the staff cancelled.
const MsgCancelled = "Cancelled by the organizers."

// Errors of Prepare, shown to the contestant.
var (
	ErrEmpty    = errors.New("The file is empty.")
	ErrNotText  = errors.New("Only PDF files and plain text (UTF-8) can be printed.")
	ErrBadPDF   = errors.New("The PDF could not be read; export it again or print it as text.")
	ErrTooLarge = errors.New("The document has too many pages.")
)

// Layout of typeset text: Courier 9 pt on A4 with line numbers.
const (
	margin     = 36.0
	fontSize   = 9.0
	leading    = 10.5
	headerSize = 9.0
	gutter     = 6 // "1234 " line numbers
	tabWidth   = 4
)

var (
	columns      = floor((pdf.A4Width-2*margin)/(fontSize*pdf.MonoWidth)) - gutter
	linesPerPage = floor((pdf.A4Height - 2*margin - 22) / leading)
)

func floor(v float64) int { return int(v) }

// Prepare returns the printable PDF of an uploaded file and its pages: a
// PDF as it is, text typeset with a header (title, page i of n) on every
// page. Documents with more than maxPages pages are refused (maxPages <= 0:
// no limit) before any typesetting.
func Prepare(title string, data []byte, maxPages int) (doc []byte, pages int, err error) {
	if len(data) == 0 {
		return nil, 0, ErrEmpty
	}
	if bytes.HasPrefix(data, []byte("%PDF-")) {
		n, err := pdf.CountPages(data)
		if err != nil {
			return nil, 0, ErrBadPDF
		}
		if maxPages > 0 && n > maxPages {
			return nil, n, ErrTooLarge
		}
		return data, n, nil
	}
	lines, err := layout(data)
	if err != nil {
		return nil, 0, err
	}
	pages = (len(lines) + linesPerPage - 1) / linesPerPage
	if maxPages > 0 && pages > maxPages {
		return nil, pages, ErrTooLarge
	}
	d := pdf.New()
	d.Title = title
	for p := 0; p < pages; p++ {
		pg := d.AddPage()
		top := pdf.A4Height - margin
		right := pdf.A4Width - margin
		num := fmt.Sprintf("%d / %d", p+1, pages)
		pg.Text(margin, top, headerSize, true, pdf.Fit(title, right-margin-pdf.Width(num, headerSize, false)-20, headerSize, true))
		pg.TextRight(right, top, headerSize, false, num)
		pg.Line(margin, top-5, right, top-5, 0.5)
		y := top - 22
		for _, l := range lines[p*linesPerPage : min((p+1)*linesPerPage, len(lines))] {
			pg.Mono(margin, y, fontSize, l)
			y -= leading
		}
	}
	return d.Bytes(), pages, nil
}

// layout splits text into printed lines: tabs expanded, long lines
// wrapped, each source line numbered on its first printed line.
func layout(data []byte) ([]string, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return nil, ErrNotText
	}
	text := strings.ReplaceAll(strings.ReplaceAll(string(data), "\r\n", "\n"), "\r", "\n")
	text = strings.TrimRight(text, "\n")
	if strings.TrimSpace(text) == "" {
		return nil, ErrEmpty
	}
	var out []string
	for i, src := range strings.Split(text, "\n") {
		runes := []rune(expandTabs(src))
		prefix := fmt.Sprintf("%*d ", gutter-1, i+1)
		for first := true; first || len(runes) > 0; first = false {
			n := min(columns, len(runes))
			out = append(out, prefix+string(runes[:n]))
			runes = runes[n:]
			prefix = strings.Repeat(" ", gutter)
		}
	}
	return out, nil
}

func expandTabs(s string) string {
	if !strings.Contains(s, "\t") {
		return s
	}
	var b strings.Builder
	col := 0
	for _, r := range s {
		if r == '\t' {
			n := tabWidth - col%tabWidth
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		}
		b.WriteRune(r)
		col++
	}
	return b.String()
}

// BannerInfo is what the banner page says about a job.
type BannerInfo struct {
	JobID               int64
	Username, Name      string
	Team, Site, Contest string
	Filename            string
	Pages               int
	Submitted           time.Time
}

// Banner is the cover page printed before each job, so that the staff know
// whom to give the pages to.
func Banner(b BannerInfo) []byte {
	d := pdf.New()
	d.Title = fmt.Sprintf("Print job %d", b.JobID)
	p := d.AddPage()
	w := pdf.A4Width
	y := pdf.A4Height - 140
	p.TextCentered(w/2, y, 20, false, fmt.Sprintf("#%d", b.JobID))
	y -= 70
	p.TextCentered(w/2, y, 40, true, pdf.Fit(b.Username, w-2*margin, 40, true))
	y -= 40
	if b.Name != "" {
		p.TextCentered(w/2, y, 20, false, pdf.Fit(b.Name, w-2*margin, 20, false))
		y -= 40
	}
	for _, kv := range [][2]string{{"Team", b.Team}, {"Site", b.Site}, {"Contest", b.Contest}, {"File", b.Filename},
		{"Pages", fmt.Sprint(b.Pages)}, {"Sent", b.Submitted.UTC().Format("2006-01-02 15:04 UTC")}} {
		if kv[1] == "" {
			continue
		}
		p.TextRight(w/2-10, y, 16, false, kv[0])
		p.Text(w/2+10, y, 16, true, pdf.Fit(kv[1], w/2-10-margin, 16, true))
		y -= 28
	}
	p.DashedLine(margin, 120, w-margin, 120, 1)
	return d.Bytes()
}
