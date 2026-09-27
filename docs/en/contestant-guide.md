# Contestant guide

Everything a contestant sees and does on the contest site
(`https://<domain>/<contest>/`). Organizers can print it or link to it
before the contest.

## The page

- **Top**: the contest's name, its phase (*Contest in progress*,
  *Finished*...), the **time remaining**, your username and **Log out**.
- **Menu**: *Problems*, one link per problem (`A. sum`, `B. paths`...),
  *Standings* (if the organizers show them), *Clarifications* (with a red
  number when something new is unread), *Testing*, *Printing* and
  *Appeals* (when enabled) and *Documentation* (languages, compilers and
  libraries).
- **Foot**: language, theme (light, dark, high contrast or the system's)
  and text size. They apply at once and are remembered.

## Problems

The first page lists the problems with your score (`40 / 100`, green when
complete, yellow when partial) and how many submissions you made; below,
the organizers' announcements and your latest submissions.

## A problem: Statement and Submissions

Each problem has two tabs:

- **Statement**: the text, the limits (time, memory), the examples, the
  PDF and the attachments to download. If the organizers fix it during the
  contest, a notice appears and the statement reloads by itself.
- **Submissions**: the form to submit and the list of your submissions to
  the problem.

## Submitting

1. In **Submissions**, choose the file (`sum.*`: the extension follows the
   language) and the **language**; or open **Or write the code here** and
   type it (Tab indents; Esc then Tab leaves the editor; Ctrl+Enter
   submits; a draft is kept in the browser).
2. **Submit**. A **Submission sent** notice appears and the submission
   joins the list.
3. The button shows **Wait 0:20**: there is a minimum time between
   submissions (set by the organizers) so the judge is not flooded.

Before sending, the page checks that the file is not too large and that
its extension matches the language.

## Following the result

No need to reload: the submission's row changes by itself.

1. *Compiling…*
2. *Evaluating* with a progress bar (testcases done out of the total) and,
   while waiting, how many submissions are ahead of yours.
3. The **verdict** and the **score**; a notice appears too ("Submission 4
   judged"), even on another page of the contest. When the browser allows
   it, also a system notification while the tab is hidden (**Enable
   desktop notifications**).

### Verdicts

| Code | Meaning |
|------|---------|
| **AC** | Accepted: all correct. |
| **PA** | Partially correct: some points. |
| **WA** | Wrong answer. |
| **TLE** | Time limit exceeded. |
| **MLE** | Memory limit exceeded. |
| **RE** | Runtime error (the program ended with an error). |
| **OLE** | Output limit exceeded. |
| **SV** | Security violation (the program tried something forbidden). |
| **CE** | Compilation failed: open the details for the compiler's message. |
| **SK** | Skipped: not judged because the subtask had already failed. |

### A submission's details

**details** opens the submission's page:

- its data (time, language, verdict, score, files);
- **Subtasks**: one block per subtask with its points (`40 / 40`) and its
  verdict; green = accepted, yellow = partial, red = failed;
- **Public testcases**: the result of each public testcase (verdict,
  message, time and memory). Secret testcases are not listed one by one,
  but they count in the blocks;
- **Compilation**: the result and the compiler's messages (warnings too).

If the contest uses **tokens**, you see the score of the public testcases
("public") until you play a token on that submission (**use a token**),
which shows its full result. The rules (how many you have, how often one
comes) are on the Submissions tab.

## Clarifications

**Clarifications** gathers in one table, newest first:

- the organizers' **announcements** to everyone;
- your **questions** and their answers (*Your question*; *Your question,
  answered for everyone* when the answer helps everybody);
- **clarifications for everyone**: other people's questions the organizers
  answered publicly;
- **private messages** the organizers send you.

To ask: choose **About** (a problem or *General*), write the **Question**
and **Send question**. When an answer or an announcement arrives, a notice
appears on any page of the contest and the red number of the
**Clarifications** menu goes up. **Sound** turns on a short sound for each
notice.

## Testing

**Testing** runs your program on an input of yours (it does not count):
choose the problem, the file or the code, type the input and send; the row
shows the output, the time and the memory.

## If the page does not update

The page gets news through a connection kept open with the server; if it
is cut (network, proxy), the page switches by itself to asking every few
seconds, so results keep arriving. Reloading never hurts.
