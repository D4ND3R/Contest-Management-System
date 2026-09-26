-- name: CreateAppeal :one
INSERT INTO appeals (participation_id, task_id, submission_id, text)
VALUES (@participation_id::bigint, sqlc.narg(task_id), sqlc.narg(submission_id), @text::text)
RETURNING *;

-- name: ListAppealsByParticipation :many
-- A contestant's appeals, newest first (appeals_participation_idx).
SELECT a.*, t.name AS task_name
FROM appeals a LEFT JOIN tasks t ON t.id = a.task_id
WHERE a.participation_id = @participation_id::bigint
ORDER BY a.created_at DESC;

-- name: CountAppealsByParticipation :one
SELECT count(*)::bigint FROM appeals WHERE participation_id = @participation_id::bigint;

-- name: AdminListAppeals :many
-- A contest's appeals, open ones first (the participations of the contest,
-- then appeals_participation_idx); optionally one status.
SELECT a.*, u.username, t.name AS task_name, ad.username AS handler
FROM participations p
JOIN appeals a ON a.participation_id = p.id
JOIN users u ON u.id = p.user_id
LEFT JOIN tasks t ON t.id = a.task_id
LEFT JOIN admins ad ON ad.id = a.handled_by
WHERE p.contest_id = @contest_id::bigint
  AND (sqlc.narg(status)::text IS NULL OR a.status = sqlc.narg(status)::text)
ORDER BY (a.status = 'open') DESC, a.created_at;

-- name: CountOpenAppeals :one
SELECT count(*)::bigint FROM participations p JOIN appeals a ON a.participation_id = p.id
WHERE p.contest_id = @contest_id::bigint AND a.status = 'open';

-- name: GetAppealContest :one
-- The contest of an appeal (to go back to its list).
SELECT p.contest_id FROM appeals a JOIN participations p ON p.id = a.participation_id WHERE a.id = @id::bigint;

-- name: AnswerAppeal :exec
UPDATE appeals SET status = @status::text, response = @response::text, handled_by = sqlc.narg(handled_by), handled_at = now()
WHERE id = @id::bigint;

-- name: GetOwnSubmissionTask :one
-- The task of a submission, if it belongs to one of the participations.
SELECT task_id FROM submissions WHERE id = @id::bigint AND participation_id = ANY(@participation_ids::bigint[]);
