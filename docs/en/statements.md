# Statements

A task's statement is written once, in **Markdown** or **LaTeX**, and the CMS
shows it two ways:

- on the task page, as a web page with the formulas laid out by the
  browser (MathML, no JavaScript), in the contestant's language when there
  is one;
- as a **PDF** ("PDF" button next to the statement) with the task's title,
  the limits (time, memory, input and output files) and the **examples**
  typeset in, ready to print.

An uploaded PDF is also accepted and shown as it is (embedded on the page,
with the task's examples under it). HTML statements (Polygon's, for
instance) are converted to the same model: scripts and anything active are
dropped, and their `$...$` formulas are laid out.

## Writing a statement

Task page → **Statements → Write a statement** (or **edit** next to an
existing one). The editor shows the result as you type, lists what it did
not understand, and **PDF preview** opens the PDF without saving. Tick
**Primary** for the official language (several languages may be primary).

Statement languages are codes like `es`, `en`, `pt_BR`. Contestants see the
one for their interface language, or the official one, and can switch.

### Markdown

```markdown
# Title (optional; the task's title is used anyway)

Given $N$ integers $a_1, \ldots, a_N$ ($1 \le N \le 10^5$), compute

$$\sum_{i=1}^{N} \left\lfloor \frac{a_i}{2} \right\rfloor$$

## Input

- The first line has **N**.
- Then `N` numbers.

| Subtask | Points | Constraints |
|:-:|:-:|---|
| 1 | 30 | $N \le 1000$ |

![The pyramid](pyramid.png)

{{examples}}

## Notes

Use `long long`.
```

Headings, paragraphs, **bold**, *italic*, `code`, lists, tables (with
alignment), code blocks, quotes, links and images (attached files, by
name) are supported. A line `{{examples}}` places the examples; without it
they go at the end. A dollar sign followed by a space or a digit (`$5 and
$10`) is not a formula.

### LaTeX

Whole documents or fragments: `\section`, `\subsection`, `\textbf`,
`\emph`, `\texttt`, `\underline`, `itemize`/`enumerate`/`description`,
`tabular`, `verbatim`/`lstlisting`, `quote`, `center`, `\includegraphics`
(attached images), `\url`/`\href`, accents (`\'a`, `\~n`), quotes and
dashes. olymp.sty problems (Polygon and many olympiads) work as they are:
`\begin{problem}{Title}{...}`, `\InputFile`, `\OutputFile`, `\Examples`
with `\exmp{input}{output}`, `\Note`, `\Scoring`, `\Interaction` (the
section titles follow the statement's language).

### Formulas

TeX math as in LaTeX: `$...$` or `\(...\)` inline, `$$...$$` or `\[...\]`
displayed (Polygon's `$$$...$$$` too). Supported: fractions, roots,
sub/superscripts, sums, products and integrals with limits, `\left`/`\right`
and `\big` delimiters, floors and ceilings, `\binom`, `\pmod`/`\bmod`,
`cases`, matrices (`pmatrix`, `bmatrix`...), `aligned`, `\text`,
`\mathbb`/`\mathcal`/`\mathbf`/`\mathrm`, accents (`\hat`, `\bar`,
`\vec`, `\overline`), Greek letters and the usual relations and operators.
Anything else is shown as written (in red) and listed by the editor.

## Examples

Examples belong to the task, not to a dataset, and appear in **every
statement**, on the page and in the PDF (not as downloads):

- task page → **Examples → Add an example**: type the input and output, or
  upload the two files, with an optional explanation (Markdown);
- dataset page → **use as example** next to a testcase;
- in a problem package, `statement/examples/NAME.in`, `NAME.out` and an
  optional `NAME.md` explanation.

## Updates

A new statement, example or limit shows at once: the page and the PDF are
regenerated from what they depend on, and their addresses carry a version,
so no browser keeps an old copy (an old address is revalidated).

## PDF fonts

PDFs use the standard fonts every PDF reader has (Times, Helvetica,
Courier and Symbol for the mathematics), so they are small and embed
nothing. They cover Western European languages; for statements in other
scripts (Cyrillic, Greek text, Chinese...) the web page shows everything,
and for the PDF upload one made elsewhere.
