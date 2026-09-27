# Glossary

The words the system and this documentation use, in alphabetical order.

- **Announcement**: a message from the organizers to every contestant of a
  contest.
- **Appeal**: after the contest, a contestant asks for a task or a
  submission to be reviewed; the organizers answer (accepted or rejected).
- **Attachment**: a task file contestants download (a sample grader, a
  header, data).
- **Checker**: decides whether a program's output is right. The built-in
  ones compare ignoring whitespace, exactly, or as real numbers with a
  tolerance; an **own** checker is a program (CMS protocol or testlib).
- **Clarification**: a contestant's question with its answer; when the
  answer is public, everybody sees it.
- **Dataset**: the judging configuration of a task (type, limits,
  testcases, checker, managers, scoring). The **live dataset** scores the
  submissions; others are for trying changes.
- **Dispatcher**: the service that hands work to the judges and stores the
  results.
- **Freezing the scoreboard**: near the end, the public scoreboard stops
  changing (later submissions show as `?`) until the organizers unfreeze
  it.
- **Generator**: a program that writes testcase inputs from parameters.
- **Grader**: organizers' code compiled together with the contestant's
  (tasks where a function is implemented).
- **Interactor**: a program that talks with the contestant's in
  interactive tasks and decides the verdict.
- **Judge** (worker): the service that compiles and runs programs in a
  sandbox (`isolate`). There can be several, on several machines.
- **Manager**: any helper file of a dataset (checker, grader, stub,
  header, interactor, communication manager).
- **Participation**: a user's registration in a contest (with its
  settings: extra time, IPs, hidden...).
- **Sandbox**: the isolated environment where programs run, with limits on
  time, memory and processes and a system-call filter.
- **Score mode**: how a contestant's submissions to a task combine: best
  per subtask (IOI), best submission, the last one...
- **Server time zone**: the zone every site shows times in (**Server** in
  the administration).
- **Short-circuit**: stop judging a subtask as soon as a testcase scores 0.
- **Site**: a place with its own start time (contests in several cities).
- **Submission**: the code a contestant sends for a task. **Official** when
  it counts; **unofficial** in practice or when invalidated.
- **problem.yaml**: the options of a task (type, limits, checker, scoring,
  submission rules) in the format of problem packages; the Options form of
  the Configuration window edits it.
- **Subtask**: a group of testcases with their own points.
- **Task library**: every task of the server, in a contest or not.
- **Test submission**: a solution an organizer sends from the Testcases
  window of a task; it is judged on every dataset and never counts. A
  correct one is a **reference solution**.
- **Testcase**: an input and its expected output. **Public**: the
  contestant sees its result one by one.
- **Token**: shows a submission's full result during a contest that hides
  results.
- **Verdict**: the result of a submission or a testcase: AC, PA, WA, TLE,
  MLE, RE, OLE, SV, CE, SK (see the [contestant guide](contestant-guide.md#verdicts)).
- **Wait between submissions**: the minimum time between two submissions
  of a contestant (the Submit button counts down).
