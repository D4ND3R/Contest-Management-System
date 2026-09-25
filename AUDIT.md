# Compatibility and completeness audit (SPEC_AUDIT.md)

Status: **complete** / **partial** / **missing** / **hw** (pending
verification on real hardware). Every requirement lists the files that
implement it and the automated test(s) that cover it. This file is updated
section by section; the "initial" column records the state found by the
first audit (after F6), the "status" column the current state.

## §2 Problem types

| ID | Requirement | Initial | Status | Files | Tests |
|----|-------------|---------|--------|-------|-------|
| T1 | Normal I/O (stdin/stdout) with checkers: exact, whitespace, float tolerance, custom CMS and testlib | complete | complete | tasktypes/batch.go, checkers/ | worker.TestBatchVariants, worker.TestSampleSolutions, e2e.TestEveryTaskTypeFromAdminUI |
| T2a | Batch with file I/O and configurable names | complete | complete | tasktypes/batch.go | worker.TestBatchVariants/file_io* |
| T2b | Grader + headers/stubs per language (C, C++, Java, Python) | partial (no C test) | complete | tasktypes/program.go, worker/testdata/tasks/grader.* | worker.TestProblemTypeSamples/batch_grader (C, C++, Java, Python × AC/WA/TLE/MLE/RE) |
| T2c | Grader + stdio and grader + files | partial (files untested) | complete | tasktypes/batch.go | worker.TestProblemTypeSamples/batch_grader_files, e2e.TestEveryTaskTypeFromAdminUI |
| T3 | Interactive: interactor ↔ one process over crossed pipes, own box, interactor verdict, no-flush TLE, interactor/contestant exits first, invalid output WA, interactor time not charged | missing | complete | tasktypes/interactive.go, sandbox/sandbox.go (inherited pipes) | worker.TestProblemTypeSamples/interactive (AC C/Python, WA, TLE, no-flush TLE, MLE, RE, contestant/interactor ends first, invalid output, slow interactor not charged) |
| T4 | Communication: manager + N processes, FIFOs, separate boxes, per-process or summed limits, stubs, manager score+message | partial (no summed limits) | complete | tasktypes/communication.go | worker.TestCommunication, worker.TestProblemTypeSamples/communication (AC/WA/TLE/MLE/RE, stubs C/C++/Python, per_process vs total) |
| T5 | Output only: one file per testcase or a zip, name validation, partial submissions, merge with best previous per testcase (optional) | partial (no zip, no merge) | complete | tasktypes/outputonly.go, contestweb/handlers.go, db/queries/cws.sql | contestweb.TestOutputOnlySubmissions, worker.TestProblemTypeSamples/output_only |
| T6 | TwoSteps | complete | complete | tasktypes/twosteps.go | worker.TestTwoSteps |
| T7 | Every type creatable and fully configurable from the admin panel | partial (JSON parameters) | complete | adminweb/datasets.go, adminweb/typeparams.go, web/templates/aws/dataset.html | e2e.TestEveryTaskTypeFromAdminUI, adminweb.TestTaskAndDatasetManagement |
| T8 | Every type importable | missing (F9) | missing (§6/F9) | — | — |
| T9 | Sample solutions AC/WA/TLE/MLE/RE per type in tests | partial (Batch stdio only) | complete (OutputOnly: AC/WA/partial, nothing runs) | worker/types_test.go | worker.TestProblemTypeSamples, worker.TestSampleSolutions |
| T10 | For all types: custom checkers, subtasks, score types, feedback, public testcases, several datasets, reevaluation | complete | complete | scoring/, dispatcher/ | dispatcher.*, scoring.* |
| T11 | Task tester in the admin (reference solution, per-testcase verdicts, not a submission) | missing | complete | adminweb/tester.go, db/migrations/0003_task_types.sql, dispatcher/judging.go | e2e.TestEveryTaskTypeFromAdminUI |

## §3 User management

| ID | Requirement | Initial | Status | Files | Tests |
|----|-------------|---------|--------|-------|-------|
| U1 | User fields: username, names, email, institution, country, region, photo, preferred language, timezone | partial (no institution/country/region/photo) | partial | adminweb/users.go | adminweb.TestUserImportAndParticipations |
| U2 | CSV import with preview and per-row error report | partial (no preview) | partial | adminweb/users.go | adminweb.TestUserImportAndParticipations |
| U3 | CSV export | missing | missing | — | — |
| U4 | Secure password generation, password reset | partial (import only) | partial | adminweb/users.go | adminweb.TestUserImportAndParticipations |
| U5 | Printable credentials sheet (PDF, one or several per page) | missing | missing | — | — |
| U6 | Edit, disable/enable, delete with confirmation, force logout, active sessions and IPs | partial (no disable/sessions) | partial | adminweb/users.go, adminweb/participations.go | adminweb.TestUserImportAndParticipations |
| U7 | View as contestant (read-only, audited) | missing | missing | — | — |
| U8 | Participations one by one or in bulk with overrides (password, IPs, extra/delay time, hidden, unrestricted) | complete | complete | adminweb/participations.go, adminweb/users.go | adminweb.TestUserImportAndParticipations |
| U9 | Teams: CRUD, members, name, flag/logo, institution | partial (no institution, members via participations) | partial | adminweb/users.go | adminweb.TestEveryPageRenders |
| U10 | Sites/groups with their own start time; ranking filter by site | missing | missing | — | — |
| U11 | Administrators with roles and optional TOTP 2FA | partial (no TOTP) | partial | adminweb/system.go, adminweb/auth.go | adminweb.TestLoginRolesAndAudit, adminweb.TestAdministratorSafety |

## §4 Contest configuration

| ID | Requirement | Initial | Status | Files | Tests |
|----|-------------|---------|--------|-------|-------|
| C1 | General: name/slug, description, status draft/published/archived, clone, languages, UI languages, timezone, statements and attachments | partial (no status, no clone) | partial | adminweb/contests.go | adminweb.TestLoginRolesAndAudit |
| C2 | Schedule: start/end, per-user window with Start, analysis mode (unofficial), practice/upsolving, countdown, hot global extension | partial (no practice, no extension action) | partial | contest/timing.go, contestweb/ | contest.*, contestweb.TestPerUserTimeStart |
| C3 | Modality: individual or teams (max size, shared submissions); IOI or ICPC with penalty; default score mode and precision | partial (no team mode, no contest default score mode) | partial | ranking/, scoring/ | ranking.TestICPCRanking |
| C4 | Leaderboard: visibility, what contestants see, freeze last X min + manual unfreeze, during/after, subtasks/flags/institutions/hidden toggles, anonymized | missing (F7) | missing | — | — |
| C5 | Results and feedback: show score yes/no/at end, feedback level and per-testcase detail, compiler output, tokens | partial (no score visibility / compiler toggle, no token UI) | partial | contestweb/views.go | contestweb.TestScoredSubmissionShowsPublicScore |
| C6 | Submissions: max per contest and task, min interval, max file size, languages per task, user tests toggle and limits | partial (no per-task languages, no per-contest size, no user test UI) | partial | contest/limits.go, contestweb/handlers.go | contest.*, contestweb.TestSubmitFlow |
| C7 | Access: registration (admin / self with approval / invitation code), IP restriction, IP autologin, single login, password policy, session duration | partial (no registration, policy, duration) | partial | contestweb/auth.go | contestweb.TestSingleLogin, contestweb.TestIPRestrictionAndAutologin |
| C8 | Communication: questions, quick answers, announcements, private messages, live notifications | missing (F8; schema only) | missing | — | — |
| C9 | Printing: toggle, max pages per job and per contestant, staff queue | missing (F8; schema only) | missing | — | — |

## §5 Task configuration

| ID | Requirement | Initial | Status | Files | Tests |
|----|-------------|---------|--------|-------|-------|
| K1 | Task type with its options (structured form) | partial (JSON) | complete | adminweb/typeparams.go, web/templates/aws/dataset.html | e2e.TestEveryTaskTypeFromAdminUI |
| K2 | I/O file names | partial (JSON) | complete | adminweb/typeparams.go | adminweb.TestTaskAndDatasetManagement |
| K3 | Time, wall time, memory and output limits | complete | complete | adminweb/datasets.go | adminweb.TestTaskAndDatasetManagement |
| K4 | Maximum score | complete (from the score type, shown) | complete | adminweb/datasets.go | adminweb.TestTaskAndDatasetManagement |
| K5 | Subtasks with a visual editor (regex or selection) | missing | missing | — | — |
| K6 | Score type | complete | complete | adminweb/datasets.go | adminweb.TestTaskAndDatasetManagement |
| K7 | Feedback level | complete | complete | adminweb/tasks.go | adminweb.TestEveryPageRenders |
| K8 | Score mode | complete | complete | adminweb/tasks.go | dispatcher.TestMaxSubtaskAcrossSubmissions |
| K9 | Task tokens and submission limits | complete | complete | adminweb/tasks.go, contest/limits.go | contest.* |
| K10 | Allowed languages per task | missing | missing | — | — |
| K11 | Statements per language | complete | complete | adminweb/tasks.go | adminweb.TestTaskAndDatasetManagement |
| K12 | Attachments | complete | complete | adminweb/tasks.go | adminweb.TestEveryPageRenders |
| K13 | Checker / interactor / manager upload | partial (no interactor) | complete | adminweb/datasets.go, tasktypes/checker.go (.c/.cpp/binary) | e2e.TestEveryTaskTypeFromAdminUI |
| K14 | Graders and stubs per language | complete (managers by extension) | complete | adminweb/datasets.go, tasktypes/program.go | worker.TestBatchVariants |
| K15 | Zip testcases with input/output pair detection | complete | complete | adminweb/datasets.go | adminweb.TestTaskAndDatasetManagement |
| K16 | Datasets and live switch | complete | complete | adminweb/datasets.go, dispatcher/reeval.go | adminweb.TestTaskAndDatasetManagement, dispatcher.TestLiveDatasetChange |
| K17 | Task order in the contest | complete | complete | adminweb/contests.go | adminweb.TestTaskAndDatasetManagement |

## §6 Other CMS features

| ID | Requirement | Initial | Status | Files | Tests |
|----|-------------|---------|--------|-------|-------|
| X1 | Submission search/filters: user, task, verdict, language, date | partial (no date/verdict filters) | partial | adminweb/submissions.go, db/queries/aws.sql | adminweb.TestEveryPageRenders |
| X2 | Source view with syntax highlighting | partial (plain) | partial | adminweb/submissions.go | adminweb.TestEveryPageRenders |
| X3 | Diff between submissions | complete | complete | adminweb/diff.go | adminweb.TestLineDiff, adminweb.TestEveryPageRenders |
| X4 | Download all submissions as zip | missing | missing | — | — |
| X5 | Rejudge by submission, user, task, contest | complete | complete | adminweb/submissions.go, dispatcher/reeval.go | adminweb.TestReevaluateFromUI, dispatcher.TestReevaluationLevels |
| X6 | Invalidate (exclude) submissions | missing | missing | — | — |
| X7 | Manual score adjustment with mandatory justification (audited) | missing | missing | — | — |
| X8 | Plagiarism: similarity report per task | missing | missing | — | — |
| X9 | Live system panel: workers, queues, stuck jobs, system errors, CPU/memory | partial (no stuck jobs, CPU/memory) | partial | adminweb/system.go | adminweb.TestEveryPageRenders |
| X10 | Task statistics: score distribution, submissions per verdict, first AC | partial (no first AC, verdicts per testcase only) | partial | adminweb/ranking.go | adminweb.TestEveryPageRenders |
| X11 | ICPC balloons (first AC per team and task) | missing | missing | — | — |
| X12 | Final results export: CSV, JSON, printable PDF | partial (no PDF) | partial | ranking/, adminweb/ranking.go | ranking.*, adminweb.TestEveryPageRenders |
| X13 | Optional certificates in PDF | missing | missing | — | — |
| X14 | Backups: scheduled dump and restore from the admin or the CLI | missing (F9) | missing | — | — |
| X15 | Audit log of every admin action, with filters | partial (admin filter only) | partial | adminweb/server.go, adminweb/system.go | adminweb.TestLoginRolesAndAudit |
| X16 | i18n es/en on every new screen | missing (admin is English only) | missing | — | — |

## §7 Closing

| ID | Requirement | Status | Evidence |
|----|-------------|--------|----------|
| Z1 | Full test suite green | — | `make test` |
| Z2 | F10 load tests repeated at the end | — | `make loadtest` |
| Z3 | Security battery | — | worker.TestMaliciousBattery, F10 web hardening tests |
| Z4 | Admin documentation: how to create each problem type, step by step (es/en) | — | docs/{es,en}/admin-*.md |
