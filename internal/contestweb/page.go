package contestweb

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/contest"
	"github.com/D4ND3R/Contest-Management-System/internal/db/sqlc"
	"github.com/D4ND3R/Contest-Management-System/internal/i18n"
)

// langOption is an entry of the UI language selector.
type langOption struct{ Code, Name string }

// page is the data passed to every template. Methods are template helpers
// (translation, formatting) bound to the request's language and time zone.
type page struct {
	Lang        string
	Title       string
	CSRF        string
	Base        string // "/<contest>/"
	Contest     *contestView
	Part        *sqlc.GetParticipationViewRow
	Status      statusView
	Tasks       []*taskView
	Active      string
	EventsURL   string
	ServerTime  int64
	Flash       string
	Error       string
	UILanguages []langOption
	Data        any
	// ViewAs is set when an administrator views the contest as this
	// contestant (read-only).
	ViewAs string
	loc    *time.Location
}

// statusView adds template-friendly accessors to contest.Status.
type statusView struct{ contest.Status }

// Running reports whether the window is open.
func (s statusView) Running() bool { return s.Phase == contest.Running }

// T translates a message.
func (p *page) T(msg string, args ...any) string { return i18n.T(p.Lang, msg, args...) }

// TZ is the time zone name used to display times.
func (p *page) TZ() string { return p.loc.String() }

// Time formats a time of day in the page's time zone.
func (p *page) Time(t time.Time) string { return t.In(p.loc).Format("15:04:05") }

// DateTime formats a full date and time.
func (p *page) DateTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.In(p.loc).Format("2006-01-02 15:04:05")
}

// Dur formats a duration as h:mm:ss.
func (p *page) Dur(d any) string {
	var v time.Duration
	switch x := d.(type) {
	case time.Duration:
		v = x
	case int64:
		v = time.Duration(x) * time.Second
	case *int64:
		if x != nil {
			v = time.Duration(*x) * time.Second
		}
	}
	if v < 0 {
		v = 0
	}
	s := int64(v / time.Second)
	return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
}

// Score formats a score with the task's precision.
func (p *page) Score(v float64, precision int) string {
	if precision < 0 {
		precision = 0
	}
	return strconv.FormatFloat(math.Round(v*math.Pow10(precision))/math.Pow10(precision), 'f', precision, 64)
}

// Seconds formats a duration or a float number of seconds.
func (p *page) Seconds(v any) string {
	var s float64
	switch x := v.(type) {
	case time.Duration:
		s = x.Seconds()
	case float64:
		s = x
	case *float64:
		if x == nil {
			return "—"
		}
		s = *x
	}
	return strconv.FormatFloat(s, 'f', 3, 64) + " s"
}

// Bytes formats a size in MiB/KiB.
func (p *page) Bytes(v any) string {
	var b int64
	switch x := v.(type) {
	case int64:
		b = x
	case *int64:
		if x == nil {
			return "—"
		}
		b = *x
	}
	switch {
	case b >= 1<<20:
		return strconv.FormatFloat(float64(b)/(1<<20), 'f', 1, 64) + " MiB"
	case b >= 1<<10:
		return strconv.FormatInt(b>>10, 10) + " KiB"
	}
	return strconv.FormatInt(b, 10) + " B"
}

// EndMillis is the end of the contestant's window in Unix milliseconds.
func (p *page) EndMillis() int64 { return p.Status.End.UnixMilli() }

// PhaseText describes the contest phase.
func (p *page) PhaseText() string {
	switch p.Status.Phase {
	case contest.NotStarted:
		return p.T("The contest has not started yet.")
	case contest.WaitingStart:
		return p.T("You have not started the contest yet.")
	case contest.Running:
		return p.T("The contest is running.")
	case contest.Analysis:
		return p.T("Analysis mode: submissions are judged but do not count.")
	case contest.Practice:
		return p.T("Practice mode: the contest is over; submissions are judged but do not count.")
	default:
		return p.T("The contest is over.")
	}
}

// uiLanguages lists the selectable UI languages of a contest.
func uiLanguages(allowed []string) []langOption {
	var out []langOption
	for _, l := range i18n.Languages() {
		if len(allowed) == 0 || contains(allowed, l) {
			out = append(out, langOption{Code: l, Name: i18n.Names[l]})
		}
	}
	return out
}

// translateOutcome maps judge messages ("Output is correct", "Execution
// killed by signal 11") to the contestant's language.
func translateOutcome(lang, text string) string {
	if strings.HasPrefix(text, "Execution killed by signal ") {
		return i18n.T(lang, "Execution killed by signal %s", strings.TrimPrefix(text, "Execution killed by signal "))
	}
	if strings.HasPrefix(text, "Evaluation didn't produce file ") {
		return i18n.T(lang, "Evaluation didn't produce file %s", strings.TrimPrefix(text, "Evaluation didn't produce file "))
	}
	return i18n.T(lang, text)
}
