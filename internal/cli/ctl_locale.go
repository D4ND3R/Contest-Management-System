package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/D4ND3R/Contest-Management-System/internal/i18n"
	"gopkg.in/yaml.v3"
)

func init() {
	ctlCommands["locale-template"] = ctlCommand{"write a locale file to translate (every message of the contestant and ranking sites)", cmdLocaleTemplate}
	ctlCommands["locale-check"] = ctlCommand{"check a locale file: format, placeholders and how much it translates", cmdLocaleCheck}
}

const templateHelp = `# (the comment above each is the Spanish translation, as a hint). Keep every placeholder
# (%d, %s...); indexes such as %[2]s reorder them. Check with: cms ctl locale-check FILE
`

// cmdLocaleTemplate prints a locale file for a language: every message
// of the contestant and ranking sites, with the translation it has (from
// the shipped languages or -from), the Spanish one as a hint for the rest.
func cmdLocaleTemplate(args []string, stdout, stderr io.Writer) error {
	fs, _ := newFlags("locale-template", stderr)
	from := fs.String("from", "", "a locale file whose translations are kept")
	name := fs.String("name", "", "the language's own name (e.g. Français)")
	rtl := fs.Bool("rtl", false, "the language is written right to left")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: cms ctl locale-template [-name N] [-rtl] [-from file.yaml] CODE > CODE.yaml")
	}
	code := strings.ToLower(fs.Arg(0))
	have := map[string]string{}
	for _, m := range i18n.ContestantMessages() {
		if i18n.Has(code, m) && code != i18n.Default {
			have[m] = i18n.T(code, m)
		}
	}
	if *from != "" {
		data, err := os.ReadFile(*from)
		if err != nil {
			return err
		}
		l, _, err := i18n.ParseLocale(data)
		if err != nil {
			return fmt.Errorf("%s: %w", *from, err)
		}
		for k, v := range l.Messages {
			have[k] = v
		}
		if *name == "" {
			*name = l.Name
		}
		if l.Dir == "rtl" {
			*rtl = true
		}
	}
	if *name == "" {
		*name = i18n.Names[code]
	}
	dir := "ltr"
	if *rtl || i18n.Dir(code) == "rtl" {
		dir = "rtl"
	}
	q := func(s string) string {
		b, _ := yaml.Marshal(s)
		return strings.TrimSuffix(string(b), "\n")
	}
	fmt.Fprintf(stdout, "# Interface of the contestant and ranking sites in %q. Translate the empty values\n", code)
	io.WriteString(stdout, templateHelp)
	fmt.Fprintf(stdout, "name: %s\ndir: %s\nmessages:\n", q(*name), dir)
	for _, m := range i18n.ContestantMessages() {
		if _, ok := have[m]; !ok {
			fmt.Fprintf(stdout, "  # %s\n", strings.ReplaceAll(i18n.T("es", m), "\n", " "))
		}
		fmt.Fprintf(stdout, "  %s: %s\n", q(m), q(have[m]))
	}
	return nil
}

func cmdLocaleCheck(args []string, stdout, stderr io.Writer) error {
	fs, _ := newFlags("locale-check", stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: cms ctl locale-check FILE.yaml")
	}
	data, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	l, warnings, err := i18n.ParseLocale(data)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, m := range i18n.ContestantMessages() {
		known[m] = true
	}
	for k := range l.Messages {
		if !known[k] {
			warnings = append(warnings, fmt.Sprintf("%q is not a message of this version (ignored)", k))
		}
	}
	for _, w := range warnings {
		fmt.Fprintln(stdout, "warning:", w)
	}
	translated := 0
	for _, m := range i18n.ContestantMessages() {
		if l.Messages[m] != "" {
			translated++
		}
	}
	total := len(i18n.ContestantMessages())
	fmt.Fprintf(stdout, "%s (%s): %d of %d messages translated (%d%%); the rest is shown in English\n",
		l.Name, l.Dir, translated, total, translated*100/total)
	return nil
}
