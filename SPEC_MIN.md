# SPEC_MIN — minimal redesign and contest-day fixes (fourth specification)

Skipping skills: immersive-web-design, master skill (and every other skill:
the owner asked that no skill be used).

## Request (translated from Spanish)

1. Redesign the whole site again: a mix of Codeforces, the original CMS and
   qoj.ac, extremely simplified ("minimalism"): in a contest what matters
   most is saving time. Codeforces/QOJ-like UI, no cards. The admin may
   take after Polygon/Codeforces or CubeCoders AMP.
2. The theme and language selector looks odd: move it to the very bottom
   and make it apply immediately.
3. A clarifications section, like the questions one: questions asked and
   answered there, answers to one person or to everybody, and general
   announcements.
4. The site administrator can set a timezone at any moment and the whole
   server follows it.
5. Pages do not update in real time; they must (clarifications, statement
   changes, submission results).
6. Each problem gets two tabs, Statement and Submissions, like qoj.ac.
7. After submitting, a notification says the submission was made and that
   its status should be watched.
8. Contestants do not see their verdicts (the page says only public
   subtasks are shown, which is wrong). A contestant sees the execution of
   the first (public) testcase only, but sees the score of every subtask as
   blocks coloured by result (red WA, yellow PA, green AC, ...).
9. There are no partial results (PA).
10. A cooldown between submissions, against spam.
11. The admin scoreboard settings are disorganised; make them simple.
12. Upgrading from the previous version to the current one failed with a
    database error (the owner deleted everything and reinstalled).
13. Setting up a problem: create it, then add the files one by one (the
    statement some time later, then the testcases, ...), not only a zip.
14. A manual testcase generator in the problems section.
15. Remove images, drawings and symbols, and the cards: they look AI-made.
    Everything in its minimal expression, but looking good.
16. Check that the other problem types work.
17. Update the docs in great detail, so anyone can follow them.
18. A field review that everything works.
19. Then merge the branch into main, commit and report.

The project rules still hold (SPEC.md §2): server-rendered HTML + htmx +
plain CSS, no inline JavaScript, strict CSP, light pages.
