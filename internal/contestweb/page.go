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
	"github.com/D4ND3R/Contest-Management-System/internal/version"
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
	// Unread is the number of unread announcements, messages and answers.
	Unread int64
	// Ranking is set when the contestant may see the ranking.
	Ranking bool
	// RegisterOpen: the login page offers self-registration.
	RegisterOpen bool
	// OOB marks a fragment swapped out of band (htmx).
	OOB bool
	loc *time.Location
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
	if p.Contest != nil && p.Contest.Status == "archived" {
		return p.T("This contest is archived: you can look at it but not submit.")
	}
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

// Heading is the contest's display title: its title, else its description,
// else its name.
func (c *contestView) Heading() string {
	switch {
	case c.Title != "":
		return c.Title
	case c.Description != "":
		return c.Description
	}
	return c.Name
}

// Subheading is the line under the heading (the description, unless it is
// the heading already).
func (c *contestView) Subheading() string {
	if c.Title != "" {
		return c.Description
	}
	return ""
}

// BannerURL is the versioned address of the banner image ("" = none).
func (p *page) BannerURL() string {
	if p.Contest == nil || p.Contest.BannerDigest == nil || len(*p.Contest.BannerDigest) < 20 {
		return ""
	}
	return "/" + p.Contest.Name + "/banner?v=" + (*p.Contest.BannerDigest)[:20]
}

// PhaseClass and PhaseLabel describe the contest phase in a pill.
func (p *page) PhaseClass() string {
	switch p.Status.Phase {
	case contest.Running:
		return "ok live"
	case contest.NotStarted, contest.WaitingStart:
		return "info"
	case contest.Analysis, contest.Practice:
		return "warn"
	}
	return ""
}

func (p *page) PhaseLabel() string {
	if p.Contest != nil && p.Contest.Status == "archived" {
		return p.T("Archived")
	}
	switch p.Status.Phase {
	case contest.Running:
		return p.T("Contest in progress")
	case contest.NotStarted:
		return p.T("Not started")
	case contest.WaitingStart:
		return p.T("Ready to start")
	case contest.Analysis:
		return p.T("Analysis mode")
	case contest.Practice:
		return p.T("Practice mode")
	}
	return p.T("Finished")
}

// Version is the CMS version (sidebar footer).
func (p *page) Version() string { return version.Version }

// FullName is the contestant's name for the user menu.
func (p *page) FullName() string {
	if p.Part == nil {
		return ""
	}
	if n := strings.TrimSpace(p.Part.FirstName + " " + p.Part.LastName); n != "" {
		return n
	}
	return p.Part.Username
}

// DateRange formats the contest window compactly ("May 12, 09:00 – 14:00"
// or across days).
func (p *page) DateRange(a, b time.Time) string {
	if a.IsZero() {
		return ""
	}
	a, b = a.In(p.loc), b.In(p.loc)
	if a.Year() == b.Year() && a.YearDay() == b.YearDay() {
		return a.Format("2006-01-02 15:04") + " – " + b.Format("15:04")
	}
	return a.Format("2006-01-02 15:04") + " – " + b.Format("2006-01-02 15:04")
}

// Hours formats a duration as "5h" or "4h 30m".
func (p *page) Hours(d time.Duration) string {
	m := int64(d.Round(time.Minute) / time.Minute)
	if m%60 == 0 {
		return strconv.FormatInt(m/60, 10) + "h"
	}
	if m < 60 {
		return strconv.FormatInt(m, 10) + "m"
	}
	return strconv.FormatInt(m/60, 10) + "h " + strconv.FormatInt(m%60, 10) + "m"
}

// TaskIndex is the position of a task in the contest (its letter).
func (p *page) TaskIndex(name string) int {
	for i, t := range p.Tasks {
		if t.Name == name {
			return i
		}
	}
	return 0
}

// Approx writes a number of seconds roughly ("12 s", "3 min").
func (p *page) Approx(sec int64) string {
	if sec < 90 {
		return strconv.FormatInt(max(sec, 1), 10) + " s"
	}
	return strconv.FormatInt((sec+30)/60, 10) + " min"
}
