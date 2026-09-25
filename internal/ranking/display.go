package ranking

import (
	"strconv"
	"strings"
)

// CellDisplay is how a board cell looks (language-neutral).
type CellDisplay struct {
	Class    string
	Text     string
	Minute   string
	Subtasks string
	Pending  bool
}

// Display returns how cell c of task i looks on board b.
func Display(b *Board, i int, c BoardCell) CellDisplay {
	t := b.Tasks[i]
	var d CellDisplay
	d.Pending = c.Pending > 0
	if b.ICPC {
		switch {
		case c.Solved:
			d.Class, d.Text = "ok", "+"
			if c.Attempts > 0 {
				d.Text += strconv.Itoa(c.Attempts)
			}
			d.Minute = strconv.Itoa(c.Minute) + "'"
		case c.Attempts > 0:
			d.Class, d.Text = "bad", "−"+strconv.Itoa(c.Attempts)
		case c.Submitted:
			d.Class, d.Text = "muted", "·"
		default:
			d.Class, d.Text = "muted", ""
		}
		return d
	}
	switch {
	case !c.Submitted:
		d.Class, d.Text = "muted", "–"
	case t.MaxScore > 0 && c.Score >= t.MaxScore-1e-9:
		d.Class, d.Text = "ok", FormatScore(c.Score, t.Precision)
	case c.Score > 0:
		d.Class, d.Text = "warn", FormatScore(c.Score, t.Precision)
	default:
		d.Class, d.Text = "bad", FormatScore(c.Score, t.Precision)
	}
	if len(c.Subtasks) > 0 {
		parts := make([]string, len(c.Subtasks))
		for k, v := range c.Subtasks {
			parts[k] = FormatScore(v, t.Precision)
		}
		d.Subtasks = strings.Join(parts, " · ")
	}
	return d
}

// FormatScore formats a score with the given decimals.
func FormatScore(v float64, precision int) string {
	return strconv.FormatFloat(v, 'f', precision, 64)
}
