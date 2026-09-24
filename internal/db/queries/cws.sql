-- name: GetParticipationView :one
-- A contestant's participation with the fields every page needs.
SELECT p.id, p.contest_id, p.user_id, p.starting_time, p.delay_time_s, p.extra_time_s, p.hidden, p.unrestricted,
       p.login_nonce, p.ip, u.username, u.first_name, u.last_name, u.timezone, u.preferred_languages,
       t.code AS team_code, t.name AS team_name
FROM participations p
JOIN users u ON u.id = p.user_id
LEFT JOIN teams t ON t.id = p.team_id
WHERE p.id = $1;

-- name: ListScoresByParticipation :many
SELECT task_id, score, subtask_scores, icpc_solved, icpc_attempts, icpc_solved_at, pending
FROM participation_task_scores WHERE participation_id = $1;

-- name: ListSubmissionsWithResults :many
-- A contestant's submissions to a task with their result on the live
-- dataset (index: submissions_participation_task_idx + result PK).
SELECT s.id, s.submitted_at, s.language, s.official, (k.submission_id IS NOT NULL)::boolean AS tokened,
       sr.compilation_outcome, sr.evaluation_outcome, sr.testcases_done, sr.testcases_total,
       sr.score, sr.public_score, sr.scored_at, sr.system_error
FROM submissions s
LEFT JOIN submission_results sr ON sr.submission_id = s.id AND sr.dataset_id = @dataset_id::bigint
LEFT JOIN tokens k ON k.submission_id = s.id
WHERE s.participation_id = @participation_id::bigint AND s.task_id = @task_id::bigint
ORDER BY s.submitted_at DESC, s.id DESC;

-- name: GetSubmissionWithResult :one
SELECT s.id, s.participation_id, s.task_id, s.submitted_at, s.language, s.official,
       (k.submission_id IS NOT NULL)::boolean AS tokened,
       sr.compilation_outcome, sr.compilation_text, sr.compilation_stdout, sr.compilation_stderr,
       sr.compilation_time, sr.compilation_memory,
       sr.evaluation_outcome, sr.testcases_done, sr.testcases_total,
       sr.score, sr.score_details, sr.public_score, sr.public_score_details, sr.scored_at, sr.system_error
FROM submissions s
LEFT JOIN submission_results sr ON sr.submission_id = s.id AND sr.dataset_id = @dataset_id::bigint
LEFT JOIN tokens k ON k.submission_id = s.id
WHERE s.id = @id::bigint;

-- name: ListActiveContests :many
-- Contests shown on the CWS landing page.
SELECT id, name, description, start_time, stop_time FROM contests
WHERE stop_time > now() - interval '30 days' OR analysis_stop > now()
ORDER BY start_time DESC;
