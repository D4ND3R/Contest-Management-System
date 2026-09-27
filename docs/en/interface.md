# Interface, languages and accessibility

## The design

The three sites (contest, administration, ranking) share a sober design in
the spirit of Codeforces, QOJ and the original CMS: a header line (contest,
phase, time left, user), a row of links as the menu (two in the
administration: the server and the current contest), tables, and the foot
with the preferences. There are no cards, icons or decorative images:
colour only carries meaning (verdicts and subtask blocks: green accepted,
yellow partial, red rejected, grey pending). Pages weigh a few kilobytes,
so they load fast even with many contestants on the same network.

On narrow screens the menu wraps over several lines and wide tables scroll
inside themselves, never the page.

## Languages of the interface

The contestant and ranking sites speak 18 languages: English, Spanish,
French, Portuguese, German, Italian, Russian, Ukrainian, Polish, Turkish,
Chinese (simplified), Japanese, Korean, Vietnamese, Indonesian, and three
written right to left: Arabic, Persian and Hebrew. The administration site
is in English and Spanish.

Each visitor picks a language **at the foot of any page**; the page reloads
in that language and the choice is remembered in the browser. Without a choice, the browser's preferred languages decide.
A contest can restrict the languages offered (the **Interface** checkboxes of the contest settings, or
`allowed_localizations` in
[contest.yaml](contest-config.md)).

Right-to-left languages mirror the whole layout (the menu starts on the
right). Source code, inputs and outputs always
read left to right. A statement is shown in the direction of its own
language, whatever the interface language, so a Persian statement reads
correctly on an English page and the other way round. Questions, answers,
announcements and names take the direction of what was written.

The shipped translations were written for this project and checked by
machine (every message present, every placeholder in place), not yet by
native speakers. Before an official contest, have a speaker of each
language you offer read the contestant pages, and correct what needs it
as described below.

### Adding or correcting a language

No code is needed: a language is a YAML file with the English text of each
message and its translation.

```sh
cmsctl locale-template -name "Kiswahili" sw > sw.yaml       # every message, Spanish as a hint
cmsctl locale-template -from /etc/cms/locales/fr.yaml fr > fr.yaml   # continue a file
cmsctl locale-check sw.yaml                                  # placeholders and coverage
```

Put the files in a directory and point `locales_dir` (or
`CMS_LOCALES_DIR`) at it:

```yaml
locales_dir: /etc/cms/locales
```

A file named after a shipped language (`fr.yaml`) corrects it: its
translations replace the shipped ones and the rest stay. A new code adds a
language (`dir: rtl` for right-to-left scripts). Messages left empty are
shown in English. Keep every placeholder (`%d`, `%s`) of the English text;
`%[2]s` style indexes reorder them when the grammar needs it. A translation
whose placeholders differ is ignored (English is shown) and logged when the
services start. Restart the contest, admin and ranking services after
changing the directory.

Contributions are welcome: a complete `cmsctl locale-check` and a native
speaker's review are all a new language needs to be shipped.

## Accessibility

The three sites are built to be used with a keyboard only, with a screen
reader, and with large text.

- **Display preferences** (at the foot of every page, applied at once):
  light (default), dark, high contrast (black with yellow accents,
  underlined links) or as the operating system prefers; text size normal, large,
  larger or largest. The base size follows the browser's own font setting,
  and every page reflows at any zoom. They are remembered in the browser,
  apply before logging in, and never change what the organizers see.
- **Keyboard**: every page starts with a "Skip to content" link; every
  control is reachable with Tab and shows where the focus is. The code editor keeps Tab for indentation: press Esc and
  then Tab to leave it (no keyboard trap).
- **Screen readers**: pages declare their language and direction, every
  form field has a label, results appear in a polite live region, notifications are
  announced, and ranking markers (medals, unofficial participants) have
  words.
- **Contrast**: every text colour meets WCAG AA (4.5:1) on its background in
  the dark and light themes; the high contrast theme goes well beyond.
- **Motion**: with the operating system's "reduce motion" setting nothing
  animates.

The test suite checks every contestant, ranking and administration page
against these rules (labels, names, unique ids, landmarks, language and
direction) and drives the contest site in a real browser (the editor's
keys, the right-to-left layout, the themes and text sizes).

## The code editor

Tasks whose submission is one source file show **Or write the code here**
under the file field: a plain text area where Tab indents (Shift+Tab
unindents, also on a selection), Ctrl+Enter submits, and a draft is kept in
the browser until it is replaced. It needs no plug-in and works with the
browser's own undo, find and zoom. A file chosen in the file field takes
precedence over the editor's text. Before sending, the page checks the
file's size and that its extension matches the chosen language; the server
checks everything again.
