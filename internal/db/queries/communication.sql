-- name: CreateQuestion :one
INSERT INTO questions (participation_id, asked_at, subject, text) VALUES ($1, $2, $3, $4) RETURNING *;

-- name: ListQuestionsByParticipation :many
SELECT * FROM questions WHERE participation_id = $1 ORDER BY asked_at DESC, id DESC;

-- name: ListQuestionsByContest :many
SELECT q.*, u.username
FROM questions q
JOIN participations p ON p.id = q.participation_id
JOIN users u ON u.id = p.user_id
WHERE p.contest_id = $1
ORDER BY (q.reply_at IS NULL AND NOT q.ignored) DESC, q.asked_at DESC;

-- name: GetQuestion :one
SELECT * FROM questions WHERE id = $1;

-- name: ReplyQuestion :one
UPDATE questions SET reply_at = now(), reply_subject = $2, reply_text = $3, reply_admin_id = $4, ignored = false
WHERE id = $1 RETURNING *;

-- name: SetQuestionIgnored :exec
UPDATE questions SET ignored = $2 WHERE id = $1;

-- name: CreateAnnouncement :one
INSERT INTO announcements (contest_id, subject, text, admin_id) VALUES ($1, $2, $3, $4) RETURNING *;

-- name: ListAnnouncements :many
SELECT * FROM announcements WHERE contest_id = $1 ORDER BY created_at DESC, id DESC;

-- name: DeleteAnnouncement :exec
DELETE FROM announcements WHERE id = $1;

-- name: CreateMessage :one
INSERT INTO messages (participation_id, subject, text, admin_id) VALUES ($1, $2, $3, $4) RETURNING *;

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
