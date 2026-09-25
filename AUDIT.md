# Compatibility and completeness audit (SPEC_AUDIT.md + SPEC_CLOSE.md)

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
| U1 | User fields: username, names, email, institution, country, region, photo, preferred language, timezone | partial (no institution/country/region/photo) | complete | adminweb/users.go, db/migrations/0004_users.sql | adminweb.TestUserAccountActions, adminweb.TestUserImportAndParticipations |
| U2 | CSV import with preview and per-row error report | partial (no preview) | complete | adminweb/users.go (preview → confirm), web/templates/aws/user_import.html | adminweb.TestUserImportAndParticipations |
| U3 | CSV export | missing | complete | adminweb/accounts.go (/users/export.csv) | adminweb.TestUserImportAndParticipations |
| U4 | Secure password generation, password reset (single and bulk) | partial (import only) | complete | adminweb/accounts.go | adminweb.TestUserAccountActions |
| U5 | Printable credentials sheet (PDF, 1/2/4/6/8 per page: user, password, name, site, URL) | missing | complete | internal/pdf, adminweb/accounts.go | pdf.TestDocumentStructure, adminweb.TestUserImportAndParticipations |
| U6 | Edit, disable/enable, delete with confirmation, force logout, active sessions and IPs | partial (no disable/sessions) | complete | adminweb/accounts.go, webkit/sessions.go, contestweb/server.go | e2e.TestAdminControlsContestantSessions, adminweb.TestUserAccountActions |
| U7 | View as contestant (read-only, audited) | missing | complete | adminweb/accounts.go, contestweb/auth.go (impersonate), webkit/session.go | e2e.TestAdminControlsContestantSessions |
| U8 | Participations one by one or in bulk with overrides (password, IPs, extra/delay time, hidden, unrestricted) | complete | complete | adminweb/participations.go, adminweb/users.go | adminweb.TestUserImportAndParticipations |
| U9 | Teams: CRUD, members, name, flag/logo, institution | partial (no institution, members via participations) | complete | adminweb/users.go, adminweb/sites.go | adminweb.TestTeamsSitesAndMembers |
| U10 | Sites/groups with their own start time; ranking filter by site | missing | complete | adminweb/sites.go, contest/timing.go, ranking/ranking.go | contest.TestSiteStartAndPractice, adminweb.TestTeamsSitesAndMembers |
| U11 | Administrators with roles and optional TOTP 2FA | partial (no TOTP) | complete | adminweb/totp.go, auth/totp.go | auth.TestTOTPRFC6238Vectors, adminweb.TestAdminTOTP, adminweb.TestLoginRolesAndAudit |

## §4 Contest configuration

| ID | Requirement | Initial | Status | Files | Tests |
|----|-------------|---------|--------|-------|-------|
| C1 | General: name/slug, description, status draft/published/archived, clone, languages, UI languages, timezone, statements and attachments | partial (no status, no clone) | complete (drafts invisible to contestants but previewable by admins; archived read-only and unlisted, also off the ranking servers while draft; copy with every setting, sites, tasks and datasets, optionally participants, never submissions) | db/migrations/0009_contest_settings.sql, adminweb/contests.go, adminweb/clone.go, contestweb/server.go, rankingpush/pusher.go | adminweb.TestContestClone, contestweb.TestContestStatus, db.TestRowToUpdateCopiesEveryField |
| C2 | Schedule: start/end, per-user window with Start, analysis mode (unofficial), practice/upsolving, countdown, hot global extension | partial (no practice, no extension action) | complete (practice after the contest and any analysis window; "Extend the contest" moves the end and per-user windows, open pages get a `clock` event and refetch their window) | contest/timing.go, contestweb/sse.go (clock), adminweb/contests.go (extend), web/static/app.js | contest.*, contestweb.TestPerUserTimeStart, contestweb.TestPracticeMode, contestweb.TestClockFollowsExtension, adminweb.TestContestExtend |
| C3 | Modality: individual or teams (max size, shared submissions); IOI or ICPC with penalty; default score mode and precision | partial (no team mode, no contest default score mode) | complete (team contests: members see and open each other's submissions with the author, share limits, merged task scores and live updates; team size enforced; new tasks and imported packages take the contest's score mode and precision) | contestweb/{server,views,sse,cache}.go, db/queries/{cws,submissions}.sql, adminweb/{contests,tasks}.go, problempkg/store.go, ranking/ | contestweb.TestTeamSharedSubmissions, adminweb.TestContestModality, problempkg.TestImportExportRoundTrip, ranking.TestICPCRanking |
| C4 | Leaderboard: visibility, what contestants see, freeze last X min + manual unfreeze, during/after, subtasks/flags/institutions/hidden toggles, anonymized | missing (F7) | complete (plus team ranking, cached JSON snapshot + SSE deltas, per-participant score history, admin-only boards behind a key; scoreboard updated ~0.2 s after scoring) | db/migrations/0007_ranking.sql, ranking/{replay,board,display}.go, rankingpush/, rankingweb/, contestweb/ranking.go, adminweb/ranking.go, web/templates/rws/* | ranking.TestReplayFreezeAndHistory, ranking.TestBoardPresentation, rankingweb.TestPushProtocolAndLiveRows, rankingweb.TestPrivateBoardsAssetsAndLimits, rankingpush.TestPusherFeedsRankingWeb, contestweb.TestContestantRanking, adminweb.TestRankingSettings, e2e.TestRankingWebLive |
| C5 | Results and feedback: show score yes/no/at end, feedback level and per-testcase detail, compiler output, tokens | partial (no score visibility / compiler toggle, no token UI) | complete (scores always / after the end / never on every page; compiler messages toggle; per-task feedback level; tokens: availability from the contest and task rules (initial, generation, cap, total, interval), "use a token" per submission, full result then shown, re-aggregation, no double spending) | contest/tokens.go, contestweb/{tokens,views,handlers}.go, adminweb/contests.go | contest.TestTokens, contestweb.TestTokens, contestweb.TestScoreVisibilityAndCompilerOutput, contestweb.TestScoredSubmissionShowsPublicScore |
| C6 | Submissions: max per contest and task, min interval, max file size, languages per task, user tests toggle and limits | partial (no per-task languages, no per-contest size, no user test UI) | complete (per-contest file size limit shown with the limits; user tests: source + uploaded or typed input, live status, inline output preview and downloads, contest and task limits, toggle) | contest/limits.go, contestweb/{handlers,usertests}.go, web/templates/cws/task.html | contest.*, contestweb.TestSubmitFlow, contestweb.TestTaskLanguages, contestweb.TestUserTests, e2e.TestUserTestJudged |
| C7 | Access: registration (admin / self with approval / invitation code), IP restriction, IP autologin, single login, password policy, session duration | partial (no registration, policy, duration) | partial | contestweb/auth.go | contestweb.TestSingleLogin, contestweb.TestIPRestrictionAndAutologin |
| C8 | Communication: questions, quick answers, announcements, private messages, live notifications | missing (F8; schema only) | complete (questions per task or general, private or public answers, 5 quick answers shown in the reader's language, announcements, messages to a user or a team, SSE with unread badge and optional sound, staff inbox oldest first with contest/task filters and a menu counter, per-minute question limit) | db/migrations/0006_communication.sql, contestweb/communication.go, adminweb/communication.go, web/templates/{cws,aws}/communication.html, aws/questions.html, web/static/app.js | e2e.TestCommunicationFlow, adminweb.TestQuestionInbox, adminweb.TestAdminHubForwardsOnlyStaffEvents, contestweb.TestAskQuestion |
| C9 | Printing: toggle, max pages per job and per contestant, staff queue | missing (F8; schema only) | missing | — | — |

## §5 Task configuration

| ID | Requirement | Initial | Status | Files | Tests |
|----|-------------|---------|--------|-------|-------|
| K1 | Task type with its options (structured form) | partial (JSON) | complete | adminweb/typeparams.go, web/templates/aws/dataset.html | e2e.TestEveryTaskTypeFromAdminUI |
| K2 | I/O file names | partial (JSON) | complete | adminweb/typeparams.go | adminweb.TestTaskAndDatasetManagement |
| K3 | Time, wall time, memory and output limits | complete | complete | adminweb/datasets.go | adminweb.TestTaskAndDatasetManagement |
| K4 | Maximum score | complete (from the score type, shown) | complete | adminweb/datasets.go | adminweb.TestTaskAndDatasetManagement |
| K5 | Subtasks with a visual editor (regex or selection) | missing | complete (regex, hand-picked testcases or next N; live preview of matches, uncovered and shared testcases; thresholds; raw JSON still available) | scoring/editor.go, adminweb/scoreeditor.go, web/templates/aws/partials.html (score-editor), web/static/admin.js | adminweb.TestSubtaskEditor, scoring.TestSubtaskEditorRoundTrip |
| K6 | Score type | complete | complete | adminweb/datasets.go | adminweb.TestTaskAndDatasetManagement |
| K7 | Feedback level | complete | complete | adminweb/tasks.go | adminweb.TestEveryPageRenders |
| K8 | Score mode | complete | complete | adminweb/tasks.go | dispatcher.TestMaxSubtaskAcrossSubmissions |
| K9 | Task tokens and submission limits | complete | complete | adminweb/tasks.go, contest/limits.go | contest.* |
| K10 | Allowed languages per task | missing | complete (task list narrows the contest list; submit, task page and tester) | db/migrations/0005_task_languages.sql, adminweb/tasks.go, adminweb/tester.go, contestweb/cache.go, contestweb/handlers.go | adminweb.TestTaskLanguages, contestweb.TestTaskLanguages |
| K11 | Statements per language | complete | complete | adminweb/tasks.go | adminweb.TestTaskAndDatasetManagement |
| K12 | Attachments | complete | complete | adminweb/tasks.go | adminweb.TestEveryPageRenders |
| K13 | Checker / interactor / manager upload | partial (no interactor) | complete | adminweb/datasets.go, tasktypes/checker.go (.c/.cpp/binary) | e2e.TestEveryTaskTypeFromAdminUI |
| K14 | Graders and stubs per language | complete (managers by extension) | complete | adminweb/datasets.go, tasktypes/program.go | worker.TestBatchVariants |
| K15 | Zip testcases with input/output pair detection | complete | complete | adminweb/datasets.go | adminweb.TestTaskAndDatasetManagement |
| K16 | Datasets and live switch | complete | complete | adminweb/datasets.go, dispatcher/reeval.go | adminweb.TestTaskAndDatasetManagement, dispatcher.TestLiveDatasetChange |
| K17 | Task order in the contest | complete | complete | adminweb/contests.go | adminweb.TestTaskAndDatasetManagement |
| K18 | Own problem package: zip with `problem.yaml`, `statement/`, `tests/`, checker/interactor/manager, `graders/`, `attachments/`; documented with one example per type | missing | complete | problempkg/config.go, problempkg/read.go, docs/en/problem-package.md, docs/es/paquete-de-problema.md, docs/examples/packages/* | problempkg.TestExamplePackages, problempkg.TestReadReportsEveryProblem, problempkg.TestConfigParams |
| K19 | Admin upload: drag and drop, preview, per-file errors before creating anything, create a task or add a dataset to an existing one | missing | complete (also `cmsctl task-import`) | adminweb/packages.go, web/templates/aws/task_import.html, web/static/admin.js (drop zones), problempkg/store.go, cli/ctl_packages.go | adminweb.TestPackageImportPreview, problempkg.TestImportExportRoundTrip, cli.TestTaskImportExport, e2e.TestProblemPackagesFromAdminUI |
| K20 | Automatic validation on import: compile checker/interactor/manager, run `solutions/` (expected verdict in the name) with the task tester, report before publishing | missing | complete (managers compile on the worker during the runs; failures appear as system errors) | adminweb/packages.go (runPackageSolutions, validation report), problempkg/verdict.go, db/queries/aws.sql (AdminPackageSolutionRuns) | problempkg.TestCheck, e2e.TestProblemPackagesFromAdminUI |
| K21 | Export any task in the same format | missing | complete (admin and `cmsctl task-export`; solutions included) | problempkg/write.go, problempkg/export.go, adminweb/packages.go | problempkg.TestImportExportRoundTrip, adminweb.TestPackageImportPreview, e2e.TestProblemPackagesFromAdminUI |

## §6 Other CMS features

| ID | Requirement | Initial | Status | Files | Tests |
|----|-------------|---------|--------|-------|-------|
| X1 | Submission search/filters: user, task, verdict, language, date | partial (no date/verdict filters) | partial | adminweb/submissions.go, db/queries/aws.sql | adminweb.TestEveryPageRenders |
| X2 | Source view with syntax highlighting | partial (plain) | partial | adminweb/submissions.go | adminweb.TestEveryPageRenders |
| X3 | Diff between submissions | complete | complete | adminweb/diff.go | adminweb.TestLineDiff, adminweb.TestEveryPageRenders |
| X4 | Download all submissions as zip | missing | missing | — | — |
| X5 | Rejudge by submission, user, task, contest | complete | complete | adminweb/submissions.go, dispatcher/reeval.go | adminweb.TestReevaluateFromUI, dispatcher.TestReevaluationLevels |
| X6 | Invalidate (exclude) submissions | missing | complete (reason mandatory and shown to the contestant; restorable; excluded from task scores, ICPC attempts, rankings and output-only merges; audited) | db/migrations/0008_invalidation.sql, adminweb/invalidate.go, dispatcher/judging.go (reaggregateSubmission), db/queries/{dispatcher,aws,cws}.sql, web/templates/{aws,cws}/submission*.html | dispatcher.TestInvalidatedSubmissionsDoNotCount, e2e.TestInvalidateSubmissionFromAdminUI |
| X7 | Manual score adjustment with mandatory justification (audited) | missing | missing | — | — |
| X8 | Plagiarism: similarity report per task | missing | missing | — | — |
| X9 | Live system panel: workers, queues, stuck jobs, system errors, CPU/memory | partial (no stuck jobs, CPU/memory) | partial | adminweb/system.go | adminweb.TestEveryPageRenders |
| X10 | Task statistics: score distribution, submissions per verdict, first AC | partial (no first AC, verdicts per testcase only) | partial | adminweb/ranking.go | adminweb.TestEveryPageRenders |
| X11 | ICPC balloons (first AC per team and task) | missing | missing | — | — |
| X12 | Final results export: CSV, JSON, printable PDF | partial (no PDF) | partial | ranking/, adminweb/ranking.go | ranking.*, adminweb.TestEveryPageRenders |
| X13 | Optional certificates in PDF | missing | missing | — | — |
| X14 | Backups: scheduled dump and restore from the admin or the CLI | missing (F9) | complete (one zstd tar file with DB + blobs and SHA-256 manifest; `cmsctl dump/restore/backup-verify/backups`; schedule with a contest interval, rotation, throttle, optional S3 copy; admin page with "Back up now", progress, download and delete, audited; restore is CLI-only with services stopped) | backup/archive.go, backup/restore.go, backup/runner.go, backup/remote.go, cli/ctl_backup.go, adminweb/backups.go, db/migrate.go (MigrateTo), docs/{en/backups.md,es/respaldos.md} | backup.TestDumpRestoreIdentical, backup.TestRestoreRefusesUsedDatabase, backup.TestVerifyDetectsDamage, backup.TestRestoreOlderBackup, backup.TestDumpThrottle, backup.TestRunnerScheduleAndRotation, cli.TestDumpVerifyRestore, adminweb.TestBackupsFromAdmin |
| X15 | Audit log of every admin action, with filters | partial (admin filter only) | partial | adminweb/server.go, adminweb/system.go | adminweb.TestLoginRolesAndAudit |
| X16 | i18n es/en on every new screen | missing (admin is English only) | complete (admin and contest web; new screens keep it) | i18n/es.go, i18n/es_admin.go, adminweb/page.go (adminLang, page.T), adminweb/forms.go, web/templates/aws/* | adminweb.TestAdminInSpanish, adminweb.TestMessagesTranslated, adminweb.TestDynamicKeysTranslated, contestweb.TestTemplatesTranslated |

## §7 Closing

| ID | Requirement | Status | Evidence |
|----|-------------|--------|----------|
| Z1 | Full test suite green | — | `make test` |
| Z2 | F10 load tests repeated at the end | — | `make loadtest` |
| Z3 | Security battery | — | worker.TestMaliciousBattery, F10 web hardening tests |
| Z4 | Admin documentation: how to create each problem type, step by step (es/en) | — | docs/{es,en}/admin-*.md |

## §8 Operations (SPEC_CLOSE A5, A6)

| ID | Requirement | Initial | Status | Files | Tests |
|----|-------------|---------|--------|-------|-------|
| O1 | Host verification: kernel, cgroups v2, isolate install/permissions, `isolate --cg` run, cores, SMT/turbo/governor, swap, clock; security battery and sample solutions judged twice with identical verdicts; OK/FAIL report with fixes; "do not start the contest if it fails" documented | missing | complete (run on every judging machine; the VM used for development has no SMT/turbo to report) | scripts/verify-host.sh, selftest/selftest.go (embedded battery and samples, shared with the worker tests), cli/ctl_selftest.go (`cms ctl judge-selftest`), docs/{en/verify-host.md,es/verificar-host.md} | cli.TestVerifyHost (script end to end: OK run with two judged runs, FAIL without isolate), worker.TestMaliciousBattery, worker.TestSampleSolutions |
| O2 | Deployment guide es/en for a clean Ubuntu/Debian VPS: dependencies, isolate, cgroups v2, PostgreSQL and Valkey tuned for 2 vCPU, system user, systemd, HTTPS (Caddy or nginx + Let's Encrypt), firewall | missing (F11) | complete | docs/{en/deployment.md,es/despliegue.md} | cli.TestDocsLinks |
| O3 | Idempotent `scripts/install.sh` doing the above (main server or extra worker, LAN mode) | missing | complete (`--render-only` writes every generated file for review) | scripts/install.sh | cli.TestInstallScriptRender (2 and 8 vCPUs, Caddy and nginx, LAN, worker role, second run changes nothing) |
| O4 | systemd units per service with automatic restart and CPU limits (sandbox pinned to the reserved core) | missing | complete (`cms.target`; web services, PostgreSQL, Valkey and the proxy pinned to the web CPUs by drop-ins; the worker pins its boxes to `worker.cores`) | deploy/systemd/*, scripts/install.sh | cli.TestInstallScriptRender |
| O5 | Adding a worker on another machine | missing | complete (`cms blob-server` + `blob.backend: http` so remote workers need only Valkey and the blob server over a private network) | blobserver/server.go, blob/http.go, cli/services.go, docs/{en/external-worker.md,es/worker-externo.md} | blobserver.TestBlobServerRoundTrip, e2e.TestExternalWorker |
| O6 | Contest-day runbook: before (verify-host, backup, rehearsal), during (what to watch, extend time, rejudge, worker down, VPS reboot), after (freeze, export, final backup) | missing | complete | docs/{en/contest-day.md,es/dia-del-concurso.md} | cli.TestDocsLinks |
