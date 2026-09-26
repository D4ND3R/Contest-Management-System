package statement

import (
	"strconv"
	"strings"
	"time"

	"github.com/D4ND3R/Contest-Management-System/internal/i18n"
)

// TaskInfo is what the box under a statement's title says about the task.
type TaskInfo struct {
	TimeLimit             time.Duration
	MemoryLimit           int64
	TaskType              string
	InputFile, OutputFile string // Batch file I/O ("" = standard streams)
}

// LabelsFor are the labels in an interface language (English otherwise).
func LabelsFor(lang string) Labels {
	return Labels{
		Examples: i18n.T(lang, "Examples"), Example: i18n.T(lang, "Example %d"),
		Input: i18n.T(lang, "Input"), Output: i18n.T(lang, "Output"), Note: i18n.T(lang, "Explanation"),
	}
}

// UILang maps a statement language ("es_MX") to an interface language.
func UILang(stLang string) string {
	l := strings.ToLower(strings.SplitN(strings.SplitN(stLang, "_", 2)[0], "-", 2)[0])
	for _, a := range i18n.Languages() {
		if a == l {
			return l
		}
	}
	return i18n.Default
}

// InfoRows are the limits and files, labelled in lang.
func InfoRows(lang string, t TaskInfo) [][2]string {
	var out [][2]string
	if t.TimeLimit > 0 {
		out = append(out, [2]string{i18n.T(lang, "Time limit"), FormatSeconds(t.TimeLimit)})
	}
	if t.MemoryLimit > 0 {
		out = append(out, [2]string{i18n.T(lang, "Memory limit"), FormatBytes(t.MemoryLimit)})
	}
	switch t.TaskType {
	case "Batch", "TwoSteps", "":
		in, out2 := i18n.T(lang, "standard input"), i18n.T(lang, "standard output")
		if t.InputFile != "" {
			in = t.InputFile
		}
		if t.OutputFile != "" {
			out2 = t.OutputFile
		}
		out = append(out, [2]string{i18n.T(lang, "Input"), in}, [2]string{i18n.T(lang, "Output"), out2})
	case "Communication", "Interactive":
		out = append(out, [2]string{i18n.T(lang, "Task type"), i18n.T(lang, "interactive")})
	case "OutputOnly":
		out = append(out, [2]string{i18n.T(lang, "Task type"), i18n.T(lang, "output only")})
	}
	return out
}

// FormatSeconds writes a duration in seconds ("1 s", "0.5 s").
func FormatSeconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', -1, 64) + " s"
}

// FormatBytes writes a size in the largest binary unit that divides it
// well ("256 MiB", "1.5 GiB", "64 KiB").
func FormatBytes(b int64) string {
	units := []string{"B", "KiB", "MiB", "GiB"}
	v := float64(b)
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	return strconv.FormatFloat(v, 'f', -1, 64) + " " + units[i]
}
