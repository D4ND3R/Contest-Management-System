-- name: CreateQuestion :one
INSERT INTO questions (participation_id, contest_id, task_id, asked_at, subject, text)
VALUES (@participation_id, (SELECT contest_id FROM participations WHERE id = @participation_id), sqlc.narg(task_id), @asked_at, @subject, @text)
RETURNING *;

-- name: ListQuestionsByParticipation :many
SELECT * FROM questions WHERE participation_id = $1 ORDER BY asked_at DESC, id DESC;

-- name: ListPublicAnswers :many
-- Public answers of a contest, newest first (questions_public_idx).
SELECT * FROM questions WHERE contest_id = $1 AND public AND reply_at IS NOT NULL
ORDER BY reply_at DESC LIMIT 200;

-- name: ListQuestionsByContest :many
SELECT q.*, u.username
FROM questions q
JOIN participations p ON p.id = q.participation_id
JOIN users u ON u.id = p.user_id
WHERE q.contest_id = $1
ORDER BY (q.reply_at IS NULL AND NOT q.ignored) DESC, q.asked_at DESC;

-- name: AdminListQuestions :many
-- Staff inbox: pending questions oldest first (questions_pending_idx), then
-- the most recently answered or ignored ones; optionally one contest/task.
SELECT q.*, u.username, c.name AS contest_name, t.name AS task_name,
       (q.reply_at IS NULL AND NOT q.ignored)::boolean AS pending
FROM questions q
JOIN participations p ON p.id = q.participation_id
JOIN users u ON u.id = p.user_id
JOIN contests c ON c.id = q.contest_id
LEFT JOIN tasks t ON t.id = q.task_id
WHERE (sqlc.narg(contest_id)::bigint IS NULL OR q.contest_id = sqlc.narg(contest_id))
  AND (sqlc.narg(task_id)::bigint IS NULL OR q.task_id = sqlc.narg(task_id))
  AND (sqlc.narg(question_id)::bigint IS NULL OR q.id = sqlc.narg(question_id))
  AND (@with_answered::boolean OR (q.reply_at IS NULL AND NOT q.ignored))
ORDER BY (q.reply_at IS NULL AND NOT q.ignored) DESC,
         CASE WHEN q.reply_at IS NULL AND NOT q.ignored THEN q.asked_at END ASC,
         COALESCE(q.reply_at, q.asked_at) DESC
LIMIT 300;

-- name: CountPendingQuestions :one
-- Admin menu counter (questions_pending_idx).
SELECT count(*) FROM questions WHERE reply_at IS NULL AND NOT ignored;

-- name: GetQuestion :one
SELECT * FROM questions WHERE id = $1;

-- name: ReplyQuestion :one
UPDATE questions SET reply_at = now(), reply_subject = $2, reply_text = $3, reply_admin_id = $4, ignored = false,
    public = @public
WHERE id = $1 RETURNING *;

-- name: SetQuestionIgnored :exec
UPDATE questions SET ignored = $2 WHERE id = $1;

-- name: CountUnreadCommunication :one
-- Announcements, messages and answers (own, or public ones of others)
-- newer than the participant's last visit to the communication page (read
-- here, not from the participation cache, so a visit resets it at once).
WITH seen AS (SELECT communication_seen_at AS since FROM participations WHERE id = @participation_id)
SELECT ((SELECT count(*) FROM announcements a, seen WHERE a.contest_id = @contest_id AND a.created_at > seen.since)
      + (SELECT count(*) FROM messages m, seen WHERE m.participation_id = @participation_id AND m.created_at > seen.since)
      + (SELECT count(*) FROM questions q, seen WHERE q.participation_id = @participation_id AND q.reply_at > seen.since)
      + (SELECT count(*) FROM questions q, seen WHERE q.contest_id = @contest_id AND q.public AND q.reply_at > seen.since
             AND q.participation_id <> @participation_id))::bigint AS unread;

-- name: SetCommunicationSeen :exec
UPDATE participations SET communication_seen_at = now() WHERE id = $1;

-- name: CreateAnnouncement :one
INSERT INTO announcements (contest_id, subject, text, admin_id) VALUES ($1, $2, $3, $4) RETURNING *;

-- name: ListAnnouncements :many
SELECT * FROM announcements WHERE contest_id = $1 ORDER BY created_at DESC, id DESC;

-- name: GetAnnouncement :one
SELECT * FROM announcements WHERE id = $1;

-- name: DeleteAnnouncement :exec
DELETE FROM announcements WHERE id = $1;

-- name: CreateMessage :one
INSERT INTO messages (participation_id, subject, text, admin_id) VALUES ($1, $2, $3, $4) RETURNING *;

-- name: ListMessagesByContest :many
-- Messages sent in a contest, newest first (for the staff).
SELECT m.*, u.username, a.username AS admin_username
FROM messages m
JOIN participations p ON p.id = m.participation_id
JOIN users u ON u.id = p.user_id
LEFT JOIN admins a ON a.id = m.admin_id
WHERE p.contest_id = $1
ORDER BY m.created_at DESC, m.id DESC
LIMIT 200;

-- name: ListTeamParticipations :many
-- Participations of a team in a contest (messages to a team; a rare staff
-- action scanning the contest's participations).
SELECT p.id FROM participations p WHERE p.contest_id = $1 AND p.team_id = $2;

-- name: ListMessagesByParticipation :many
SELECT * FROM messages WHERE participation_id = $1 ORDER BY created_at DESC, id DESC;

-- name: CreatePrintJob :one
INSERT INTO print_jobs (participation_id, created_at, filename, digest) VALUES ($1, $2, $3, $4) RETURNING *;

-- name: ListPrintJobsByParticipation :many
SELECT * FROM print_jobs WHERE participation_id = $1 ORDER BY created_at DESC, id DESC;

-- name: CountPrintJobsByParticipation :one
SELECT count(*) FROM print_jobs WHERE participation_id = $1;

-- name: ClaimPrintJob :one
-- Printing service: atomically take the oldest queued job.
UPDATE print_jobs SET status = 'printing'
WHERE id = (SELECT id FROM print_jobs WHERE status = 'queued' ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED)
RETURNING *;

-- name: FinishPrintJob :exec
UPDATE print_jobs SET status = $2, status_text = $3, pages = $4 WHERE id = $1;

-- name: ListPrintJobsByContest :many
SELECT j.*, u.username FROM print_jobs j
JOIN participations p ON p.id = j.participation_id
JOIN users u ON u.id = p.user_id
WHERE p.contest_id = $1 ORDER BY j.created_at DESC;
