-- name: GetSubmissionMeta :one
-- What the dispatcher needs about a submission, in one round trip.
SELECT s.id, s.participation_id, s.task_id, s.submitted_at, s.language, s.official,
       p.contest_id, t.active_dataset_id, t.score_mode, t.score_precision
FROM submissions s
JOIN participations p ON p.id = s.participation_id
JOIN tasks t ON t.id = s.task_id
WHERE s.id = $1;

-- name: LockSubmissionResult :one
-- Serialises result processing per (submission, dataset).
SELECT generation, compilation_outcome, evaluation_outcome, testcases_total, scored_at, system_error
FROM submission_results WHERE submission_id = $1 AND dataset_id = $2
FOR UPDATE;

-- name: MarkJobsEnqueued :exec
UPDATE submission_results SET jobs_enqueued_at = now() WHERE submission_id = $1 AND dataset_id = $2;

-- name: ListEvaluatedTestcaseIDs :many
SELECT testcase_id FROM evaluations WHERE submission_id = $1 AND dataset_id = $2;

-- name: ListSweepableResults :many
-- Results needing work: never enqueued (new or invalidated) or enqueued
-- longer ago than the grace period.
SELECT submission_id, dataset_id, generation, compilation_outcome, evaluation_outcome, jobs_enqueued_at
FROM submission_results
WHERE scored_at IS NULL AND system_error IS NULL
  AND (jobs_enqueued_at IS NULL OR jobs_enqueued_at < @stale_before::timestamptz)
ORDER BY jobs_enqueued_at NULLS FIRST, submission_id
LIMIT @max_rows::integer;

-- name: ListSubmissionsMissingResults :many
-- Submissions lacking a result on a dataset they must be judged on (live
-- or autojudge), e.g. a lost "new submission" notification.
SELECT s.id AS submission_id, d.id AS dataset_id
FROM submissions s
JOIN tasks t ON t.id = s.task_id
JOIN datasets d ON d.task_id = s.task_id AND (d.id = t.active_dataset_id OR d.autojudge)
WHERE NOT EXISTS (SELECT 1 FROM submission_results sr WHERE sr.submission_id = s.id AND sr.dataset_id = d.id)
ORDER BY s.id
LIMIT $1;

-- name: SetCompilationFailedScore :exec
-- A submission that does not compile scores zero.
UPDATE submission_results SET score = 0, public_score = 0, score_details = '{}', public_score_details = '{}',
    ranking_score_details = '[]', scored_at = now()
WHERE submission_id = $1 AND dataset_id = $2;

-- name: ListTaskSubmissionsForScore :many
-- Every submission of a participation on a task with its result on a
-- dataset, for the task-score aggregation.
SELECT s.id, s.submitted_at, s.official, (k.submission_id IS NOT NULL)::boolean AS tokened,
       sr.compilation_outcome, sr.score, sr.ranking_score_details, sr.scored_at
FROM submissions s
LEFT JOIN submission_results sr ON sr.submission_id = s.id AND sr.dataset_id = @dataset_id::bigint
LEFT JOIN tokens k ON k.submission_id = s.id
WHERE s.participation_id = @participation_id::bigint AND s.task_id = @task_id::bigint
ORDER BY s.submitted_at, s.id;

-- name: ListParticipationsWithSubmissions :many
-- (participation, task) pairs with submissions on a task (re-aggregation
-- after the live dataset changes).
SELECT DISTINCT participation_id FROM submissions WHERE task_id = $1;

-- name: GetParticipationTiming :one
SELECT p.starting_time, p.delay_time_s, c.start_time, c.per_user_time_s, c.scoring_mode, c.icpc_penalty_minutes
FROM participations p JOIN contests c ON c.id = p.contest_id
WHERE p.id = $1;

-- name: SetUserTestJobsEnqueued :exec
UPDATE user_test_results SET jobs_enqueued_at = now() WHERE user_test_id = $1 AND dataset_id = $2;

-- name: GetUserTestMeta :one
SELECT u.id, u.participation_id, u.task_id, u.language, u.input_digest, t.active_dataset_id
FROM user_tests u JOIN tasks t ON t.id = u.task_id
WHERE u.id = $1;

-- name: LockUserTestResult :one
SELECT generation, compilation_outcome, completed_at FROM user_test_results
WHERE user_test_id = $1 AND dataset_id = $2 FOR UPDATE;

-- name: ListSweepableUserTests :many
SELECT user_test_id, dataset_id, generation
FROM user_test_results
WHERE completed_at IS NULL AND system_error IS NULL
  AND (jobs_enqueued_at IS NULL OR jobs_enqueued_at < @stale_before::timestamptz)
ORDER BY jobs_enqueued_at NULLS FIRST, user_test_id
LIMIT @max_rows::integer;

-- name: ListUserTestsMissingResults :many
SELECT u.id AS user_test_id, t.active_dataset_id::bigint AS dataset_id
FROM user_tests u JOIN tasks t ON t.id = u.task_id
WHERE t.active_dataset_id IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM user_test_results r WHERE r.user_test_id = u.id AND r.dataset_id = t.active_dataset_id)
ORDER BY u.id
LIMIT $1;
