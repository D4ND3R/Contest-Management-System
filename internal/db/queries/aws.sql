-- Queries of the admin web server (AWS).

-- name: AdminListSubmissions :many
-- Submissions of a contest, newest first, with their live-dataset result.
-- Keyset pagination on the id (before_id); every filter is optional.
SELECT s.id, s.submitted_at, s.language, s.official, s.participation_id, s.task_id,
       u.username, t.name AS task_name, t.score_precision,
       sr.compilation_outcome, sr.testcases_done, sr.testcases_total, sr.score, sr.scored_at,
       sr.system_error, (tk.submission_id IS NOT NULL)::boolean AS tokened,
       (s.invalidated_at IS NOT NULL)::boolean AS invalidated
FROM submissions s
JOIN participations p ON p.id = s.participation_id
JOIN users u ON u.id = p.user_id
JOIN tasks t ON t.id = s.task_id
LEFT JOIN submission_results sr ON sr.submission_id = s.id AND sr.dataset_id = t.active_dataset_id
LEFT JOIN tokens tk ON tk.submission_id = s.id
WHERE p.contest_id = @contest_id::bigint
  AND (sqlc.narg(task_id)::bigint IS NULL OR s.task_id = sqlc.narg(task_id)::bigint)
  AND (sqlc.narg(participation_id)::bigint IS NULL OR s.participation_id = sqlc.narg(participation_id)::bigint)
  AND (sqlc.narg(username)::text IS NULL OR u.username ILIKE '%' || sqlc.narg(username)::text || '%')
  AND (sqlc.narg(language)::text IS NULL OR s.language = sqlc.narg(language)::text)
  AND (sqlc.narg(min_score)::float8 IS NULL OR sr.score >= sqlc.narg(min_score)::float8)
  AND (sqlc.narg(max_score)::float8 IS NULL OR sr.score <= sqlc.narg(max_score)::float8)
  AND (sqlc.narg(status)::text IS NULL OR CASE sqlc.narg(status)::text
        WHEN 'pending' THEN sr.scored_at IS NULL AND sr.system_error IS NULL
        WHEN 'compile_failed' THEN sr.compilation_outcome = 'fail'
        WHEN 'scored' THEN sr.scored_at IS NOT NULL AND sr.compilation_outcome = 'ok'
        WHEN 'error' THEN sr.system_error IS NOT NULL
        ELSE true END)
  AND (sqlc.narg(before_id)::bigint IS NULL OR s.id < sqlc.narg(before_id)::bigint)
ORDER BY s.id DESC
LIMIT @lim::int;

-- name: AdminGetSubmission :one
-- A submission (or a task tester run, which has no participation).
SELECT s.*, COALESCE(u.id, 0)::bigint AS user_id, COALESCE(u.username, '')::text AS username,
       t.name AS task_name, t.title AS task_title, t.active_dataset_id,
       COALESCE(p.contest_id, t.contest_id, 0)::bigint AS contest_id, (tk.submission_id IS NOT NULL)::boolean AS tokened,
       COALESCE(a.username, '')::text AS tester_username
FROM submissions s
LEFT JOIN participations p ON p.id = s.participation_id
LEFT JOIN users u ON u.id = p.user_id
JOIN tasks t ON t.id = s.task_id
LEFT JOIN tokens tk ON tk.submission_id = s.id
LEFT JOIN admins a ON a.id = s.tester_admin_id
WHERE s.id = $1;

-- name: AdminListTesterRuns :many
-- Task tester runs of a task (newest first) with their result on every
-- dataset (index: submissions_tester_idx).
SELECT s.id, s.submitted_at, s.language, COALESCE(a.username, '')::text AS admin_username,
       sr.dataset_id, d.description AS dataset_description, sr.compilation_outcome, sr.testcases_done,
       sr.testcases_total, sr.score, sr.scored_at, sr.system_error
FROM (SELECT * FROM submissions WHERE task_id = @task_id::bigint AND tester ORDER BY id DESC LIMIT 30) s
LEFT JOIN admins a ON a.id = s.tester_admin_id
LEFT JOIN submission_results sr ON sr.submission_id = s.id
LEFT JOIN datasets d ON d.id = sr.dataset_id
ORDER BY s.id DESC, sr.dataset_id;

-- name: CreateTesterSubmission :one
INSERT INTO submissions (participation_id, task_id, submitted_at, language, official, tester, tester_admin_id, comment)
VALUES (NULL, @task_id::bigint, now(), @language, false, true, @admin_id::bigint, @comment::text)
RETURNING *;

-- name: AdminListSubmissionResults :many
-- Results of a submission on every dataset it was judged on.
SELECT sr.*, d.description AS dataset_description, (d.id = t.active_dataset_id)::boolean AS live
FROM submission_results sr
JOIN datasets d ON d.id = sr.dataset_id
JOIN tasks t ON t.id = d.task_id
WHERE sr.submission_id = $1
ORDER BY (d.id = t.active_dataset_id) DESC, d.id;

-- name: AdminTaskSubmissionStats :many
-- Per-task submission counters on the live dataset.
SELECT t.id AS task_id,
       count(s.id) AS submissions,
       count(DISTINCT s.participation_id) AS participants,
       count(s.id) FILTER (WHERE sr.compilation_outcome = 'fail') AS compile_failed,
       count(s.id) FILTER (WHERE sr.scored_at IS NULL AND sr.system_error IS NULL) AS pending,
       count(s.id) FILTER (WHERE sr.system_error IS NOT NULL) AS errors
FROM tasks t
LEFT JOIN submissions s ON s.task_id = t.id AND s.official
LEFT JOIN submission_results sr ON sr.submission_id = s.id AND sr.dataset_id = t.active_dataset_id
WHERE t.contest_id = $1
GROUP BY t.id;

-- name: AdminTaskVerdictStats :many
-- Testcase verdict distribution on the live dataset of each task.
SELECT s.task_id,
       (CASE WHEN e.exit_status <> 'ok' THEN e.exit_status
             WHEN e.outcome >= 1 THEN 'correct'
             WHEN e.outcome <= 0 THEN 'wrong'
             ELSE 'partial' END)::text AS verdict,
       count(*) AS n,
       COALESCE(avg(e.execution_time), 0)::float8 AS avg_time,
       COALESCE(max(e.execution_time), 0)::float8 AS max_time,
       COALESCE(max(e.execution_memory), 0)::bigint AS max_memory
FROM evaluations e
JOIN submissions s ON s.id = e.submission_id
JOIN tasks t ON t.id = s.task_id AND t.active_dataset_id = e.dataset_id
WHERE t.contest_id = $1 AND s.official
GROUP BY s.task_id, verdict
ORDER BY s.task_id, verdict;

-- name: AdminListUsers :many
-- Users with the number of participations, filtered by a search string.
SELECT u.*, (SELECT count(*) FROM participations p WHERE p.user_id = u.id) AS participations
FROM users u
WHERE (sqlc.narg(search)::text IS NULL
       OR u.username ILIKE '%' || sqlc.narg(search)::text || '%'
       OR u.first_name ILIKE '%' || sqlc.narg(search)::text || '%'
       OR u.last_name ILIKE '%' || sqlc.narg(search)::text || '%')
ORDER BY u.username
LIMIT @lim::int OFFSET @off::int;

-- name: AdminCountUsers :one
SELECT count(*) FROM users;

-- name: AdminListUserParticipations :many
SELECT p.*, c.name AS contest_name, t.code AS team_code
FROM participations p
JOIN contests c ON c.id = p.contest_id
LEFT JOIN teams t ON t.id = p.team_id
WHERE p.user_id = $1
ORDER BY c.start_time DESC;

-- name: AdminGetParticipation :one
SELECT p.*, u.username, u.first_name, u.last_name, c.name AS contest_name
FROM participations p
JOIN users u ON u.id = p.user_id
JOIN contests c ON c.id = p.contest_id
WHERE p.id = $1;

-- name: AdminContestCounts :many
-- Dashboard counters per contest.
SELECT c.id,
       (SELECT count(*) FROM participations p WHERE p.contest_id = c.id) AS participations,
       (SELECT count(*) FROM tasks t WHERE t.contest_id = c.id) AS tasks,
       (SELECT count(*) FROM submissions s JOIN participations p ON p.id = s.participation_id
         WHERE p.contest_id = c.id) AS submissions
FROM contests c;

-- name: AdminListSystemErrors :many
-- Submissions that could not be judged (most recent first).
SELECT sr.submission_id, sr.dataset_id, sr.system_error, sr.created_at, s.task_id, t.name AS task_name,
       u.username, p.contest_id
FROM submission_results sr
JOIN submissions s ON s.id = sr.submission_id
JOIN tasks t ON t.id = s.task_id
JOIN participations p ON p.id = s.participation_id
JOIN users u ON u.id = p.user_id
WHERE sr.system_error IS NOT NULL
ORDER BY sr.submission_id DESC
LIMIT 50;

-- name: AdminUnassignedTasks :many
SELECT * FROM tasks WHERE contest_id IS NULL ORDER BY name;

-- name: AdminNextTaskNum :one
SELECT COALESCE(max(num) + 1, 0)::int FROM tasks WHERE contest_id = $1;

-- name: AdminExportParticipants :many
SELECT u.username, u.first_name, u.last_name, u.email, u.institution, u.country, u.region, u.timezone,
       t.code AS team_code, st.name AS site_name, p.hidden, p.unrestricted, p.ip, p.delay_time_s, p.extra_time_s
FROM participations p
JOIN users u ON u.id = p.user_id
LEFT JOIN teams t ON t.id = p.team_id
LEFT JOIN sites st ON st.id = p.site_id
WHERE p.contest_id = $1
ORDER BY u.username;

-- name: ListPackageSolutionFiles :many
-- Files of the newest tester run of every problem-package solution of a
-- task (comment "solutions/<name>"; index: submissions_tester_idx). Runs
-- without a language are output-only solutions (one file per output).
SELECT s.id AS submission_id, s.comment, (s.language IS NULL)::boolean AS output_only, f.filename, f.digest
FROM (SELECT DISTINCT ON (comment) id, comment, language FROM submissions
      WHERE task_id = @task_id::bigint AND tester AND comment LIKE 'solutions/%'
      ORDER BY comment, id DESC) s
JOIN submission_files f ON f.submission_id = s.id
ORDER BY s.comment, f.filename;

-- name: AdminPackageSolutionRuns :many
-- Newest tester run of every problem-package solution of a task with its
-- result and the failure kinds of its evaluations on every dataset of the
-- task (index: submissions_tester_idx; evaluations by primary key).
SELECT s.id, s.comment, s.language, d.id AS dataset_id, d.description AS dataset_description,
       (sr.submission_id IS NOT NULL)::boolean AS judged, sr.compilation_outcome, sr.score, sr.scored_at, sr.system_error,
       COALESCE(bool_or(e.exit_status IN ('timeout', 'timeout_wall')), false)::boolean AS any_tle,
       COALESCE(bool_or(e.exit_status = 'memory'), false)::boolean AS any_mle,
       COALESCE(bool_or(e.exit_status IN ('signal', 'nonzero', 'output_limit')), false)::boolean AS any_re,
       COALESCE(bool_or(e.exit_status = 'ok' AND e.outcome < 1), false)::boolean AS any_wa
FROM (SELECT DISTINCT ON (comment) id, comment, language FROM submissions
      WHERE task_id = @task_id::bigint AND tester AND comment LIKE 'solutions/%'
      ORDER BY comment, id DESC) s
JOIN datasets d ON d.task_id = @task_id::bigint
LEFT JOIN submission_results sr ON sr.submission_id = s.id AND sr.dataset_id = d.id
LEFT JOIN evaluations e ON e.submission_id = s.id AND e.dataset_id = d.id
GROUP BY s.id, s.comment, s.language, d.id, d.description, sr.submission_id, sr.dataset_id
ORDER BY s.comment, d.id;

-- name: ListContestSubmissionsForRanking :many
-- Official submissions of a contest with their result on each task's live
-- dataset, in time order, for the ranking replay (frozen ranking and score
-- history; submissions_task_idx per task of the contest).
SELECT s.id, s.participation_id::bigint AS participation_id, s.task_id, s.submitted_at,
       (k.submission_id IS NOT NULL)::boolean AS tokened,
       sr.compilation_outcome, sr.score, sr.ranking_score_details, sr.scored_at
FROM tasks t
JOIN submissions s ON s.task_id = t.id
JOIN participations p ON p.id = s.participation_id
LEFT JOIN submission_results sr ON sr.submission_id = s.id AND sr.dataset_id = t.active_dataset_id
LEFT JOIN tokens k ON k.submission_id = s.id
WHERE t.contest_id = @contest_id::bigint AND p.contest_id = @contest_id::bigint AND s.official AND NOT s.tester
  AND s.invalidated_at IS NULL
ORDER BY s.submitted_at, s.id;

-- name: SetSubmissionInvalidated :one
UPDATE submissions SET invalidated_at = now(), invalidated_reason = @reason::text, invalidated_by = sqlc.narg(admin_id)::bigint
WHERE id = @id::bigint AND NOT tester
RETURNING *;

-- name: ClearSubmissionInvalidated :one
UPDATE submissions SET invalidated_at = NULL, invalidated_reason = '', invalidated_by = NULL
WHERE id = $1 RETURNING *;
