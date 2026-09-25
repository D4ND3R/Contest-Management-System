-- Queries of the admin web server (AWS).

-- name: AdminListSubmissions :many
-- Submissions of a contest, newest first, with their live-dataset result.
-- Keyset pagination on the id (before_id); every filter is optional.
SELECT s.id, s.submitted_at, s.language, s.official, s.participation_id, s.task_id,
       u.username, t.name AS task_name, t.score_precision,
       sr.compilation_outcome, sr.testcases_done, sr.testcases_total, sr.score, sr.scored_at,
       sr.system_error, (tk.submission_id IS NOT NULL)::boolean AS tokened
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
SELECT s.*, u.id AS user_id, u.username, t.name AS task_name, t.title AS task_title,
       t.active_dataset_id, p.contest_id, (tk.submission_id IS NOT NULL)::boolean AS tokened
FROM submissions s
JOIN participations p ON p.id = s.participation_id
JOIN users u ON u.id = p.user_id
JOIN tasks t ON t.id = s.task_id
LEFT JOIN tokens tk ON tk.submission_id = s.id
WHERE s.id = $1;

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
