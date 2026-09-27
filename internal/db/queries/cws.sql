-- name: GetParticipationView :one
-- A contestant's participation with the fields every page needs.
SELECT p.id, p.contest_id, p.user_id, p.team_id, p.approved, p.starting_time, p.delay_time_s, p.extra_time_s, p.hidden, p.unrestricted,
       p.login_nonce, p.ip, u.username, u.first_name, u.last_name, u.timezone, u.preferred_languages,
       u.disabled, t.code AS team_code, t.name AS team_name, s.start_time AS site_start_time
FROM participations p
JOIN users u ON u.id = p.user_id
LEFT JOIN teams t ON t.id = p.team_id
LEFT JOIN sites s ON s.id = p.site_id
WHERE p.id = $1;

-- name: ListScoresByParticipation :many
SELECT task_id, score, subtask_scores, icpc_solved, icpc_attempts, icpc_solved_at, pending, adjustment
FROM participation_task_scores WHERE participation_id = $1;

-- name: ListScoresByParticipations :many
-- Task scores of a contestant, or of every member of a team (merged by the
-- caller).
SELECT participation_id, task_id, score, subtask_scores, pending, icpc_solved, icpc_attempts, icpc_solved_at, adjustment
FROM participation_task_scores WHERE participation_id = ANY(@participation_ids::bigint[]);

-- name: ListSubmissionsWithResults :many
-- A contestant's (or a team's) submissions to a task with their result on
-- the live dataset (index: submissions_participation_task_idx + result PK).
SELECT s.id, s.submitted_at, s.language, s.official, (k.submission_id IS NOT NULL)::boolean AS tokened,
       s.invalidated_at, s.invalidated_reason, u.username AS author,
       sr.compilation_outcome, sr.evaluation_outcome, sr.testcases_done, sr.testcases_total,
       sr.score, sr.public_score, sr.scored_at, sr.system_error, sr.verdict
FROM submissions s
JOIN participations p ON p.id = s.participation_id
JOIN users u ON u.id = p.user_id
LEFT JOIN submission_results sr ON sr.submission_id = s.id AND sr.dataset_id = @dataset_id::bigint
LEFT JOIN tokens k ON k.submission_id = s.id
WHERE s.participation_id = ANY(@participation_ids::bigint[]) AND s.task_id = @task_id::bigint
ORDER BY s.submitted_at DESC, s.id DESC;

-- name: GetSubmissionWithResult :one
SELECT s.id, s.participation_id, s.task_id, s.submitted_at, s.language, s.official,
       (k.submission_id IS NOT NULL)::boolean AS tokened, s.invalidated_at, s.invalidated_reason,
       sr.compilation_outcome, sr.compilation_text, sr.compilation_stdout, sr.compilation_stderr,
       sr.compilation_time, sr.compilation_memory,
       sr.evaluation_outcome, sr.testcases_done, sr.testcases_total,
       sr.score, sr.score_details, sr.public_score, sr.public_score_details, sr.scored_at, sr.system_error, sr.verdict,
       COALESCE(u.username, '')::text AS author
FROM submissions s
LEFT JOIN participations p ON p.id = s.participation_id
LEFT JOIN users u ON u.id = p.user_id
LEFT JOIN submission_results sr ON sr.submission_id = s.id AND sr.dataset_id = @dataset_id::bigint
LEFT JOIN tokens k ON k.submission_id = s.id
WHERE s.id = @id::bigint;

-- name: ListActiveContests :many
-- Contests shown on the CWS landing page.
SELECT id, name, description, title, start_time, stop_time FROM contests
WHERE status = 'published' AND (stop_time > now() - interval '30 days' OR analysis_stop > now() OR practice_enabled)
ORDER BY start_time DESC;

-- name: BestPreviousOutputs :many
-- Output-only tasks: for every output file name, the file of the
-- participation's previous submission that scored best on the matching
-- testcase of the dataset (the latest one on ties or when unjudged).
-- Invalidated submissions are never reused.
SELECT DISTINCT ON (f.filename) f.filename, f.digest
FROM submissions s
JOIN submission_files f ON f.submission_id = s.id
LEFT JOIN testcases tc ON tc.dataset_id = @dataset_id::bigint AND f.filename = replace(@pattern::text, '%s', tc.codename)
LEFT JOIN evaluations e ON e.submission_id = s.id AND e.dataset_id = @dataset_id::bigint AND e.testcase_id = tc.id
WHERE s.participation_id = ANY(@participation_ids::bigint[]) AND s.task_id = @task_id::bigint AND s.invalidated_at IS NULL
ORDER BY f.filename, e.outcome DESC NULLS LAST, s.submitted_at DESC, s.id DESC;

-- name: LockParticipation :exec
-- Serialises token plays of a participation (no double spending).
SELECT id FROM participations WHERE id = $1 FOR UPDATE;

-- name: ListRecentSubmissions :many
-- A contestant's (or a team's) latest submissions to any task with their
-- result on the task's live dataset, for the overview
-- (submissions_participation_task_idx per member; a few hundred at most).
SELECT s.id, s.task_id, s.submitted_at, s.language, s.official, (k.submission_id IS NOT NULL)::boolean AS tokened,
       s.invalidated_at, sr.compilation_outcome, sr.evaluation_outcome, sr.testcases_done, sr.testcases_total,
       sr.score, sr.public_score, sr.scored_at, sr.system_error, sr.verdict
FROM submissions s
JOIN tasks t ON t.id = s.task_id
LEFT JOIN submission_results sr ON sr.submission_id = s.id AND sr.dataset_id = t.active_dataset_id
LEFT JOIN tokens k ON k.submission_id = s.id
WHERE s.participation_id = ANY(@participation_ids::bigint[])
ORDER BY s.submitted_at DESC, s.id DESC
LIMIT @lim::int;

-- name: CountSubmissionsByTask :many
-- A contestant's (or a team's) submissions per task, for the overview
-- (submissions_participation_task_idx).
SELECT task_id, count(*)::bigint AS n FROM submissions
WHERE participation_id = ANY(@participation_ids::bigint[])
GROUP BY task_id;

-- name: CountSubmissionsAhead :one
-- Submissions to live datasets still being judged that arrived before
-- @before_id: the queue position shown while waiting (read through
-- submission_results_pending_idx, the unfinished results only).
SELECT count(*)::bigint
FROM submission_results sr
JOIN submissions s ON s.id = sr.submission_id
JOIN tasks t ON t.id = s.task_id AND t.active_dataset_id = sr.dataset_id
WHERE sr.scored_at IS NULL AND sr.system_error IS NULL AND sr.submission_id < @before_id::bigint AND NOT s.tester;

-- name: RecentJudgingLatency :one
-- The median time from arrival to score of the latest judged submissions
-- (the newest 200 through the primary key), in seconds; 0 when none.
SELECT COALESCE(percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM sr.scored_at - s.submitted_at)), 0)::float8
FROM (SELECT id, submitted_at, task_id FROM submissions WHERE NOT tester ORDER BY id DESC LIMIT 200) s
JOIN tasks t ON t.id = s.task_id
JOIN submission_results sr ON sr.submission_id = s.id AND sr.dataset_id = t.active_dataset_id
WHERE sr.scored_at IS NOT NULL;
