-- name: CreateAdmin :one
INSERT INTO admins (name, username, password_hash, enabled, role, team_id) VALUES ($1, $2, $3, $4, $5, sqlc.narg(team_id)) RETURNING *;

-- name: GetAdmin :one
SELECT * FROM admins WHERE id = $1;

-- name: GetAdminByUsername :one
SELECT * FROM admins WHERE username = $1;

-- name: ListAdmins :many
SELECT * FROM admins ORDER BY username;

-- name: UpdateAdmin :one
UPDATE admins SET name = $2, username = $3, enabled = $4, role = $5, team_id = sqlc.narg(team_id) WHERE id = $1 RETURNING *;

-- name: SetAdminPassword :exec
UPDATE admins SET password_hash = $2, password_change_required = false WHERE id = $1;

-- name: RequireAdminPasswordChange :exec
UPDATE admins SET password_change_required = true WHERE id = $1;

-- name: DeleteAdmin :exec
DELETE FROM admins WHERE id = $1;

-- name: CountAdmins :one
SELECT count(*) FROM admins;

-- name: InsertAuditLog :exec
INSERT INTO audit_log (admin_id, action, target_type, target_id, details, ip) VALUES ($1, $2, $3, $4, $5, $6);

-- name: ListAuditLog :many
SELECT a.*, ad.username AS admin_username
FROM audit_log a LEFT JOIN admins ad ON ad.id = a.admin_id
WHERE (sqlc.narg(admin_id)::bigint IS NULL OR a.admin_id = sqlc.narg(admin_id))
  AND (sqlc.narg(action)::text IS NULL OR a.action LIKE sqlc.narg(action)::text || '%')
  AND (sqlc.narg(from_time)::timestamptz IS NULL OR a.created_at >= sqlc.narg(from_time)::timestamptz)
  AND (sqlc.narg(to_time)::timestamptz IS NULL OR a.created_at < sqlc.narg(to_time)::timestamptz)
  AND (sqlc.narg(before_id)::bigint IS NULL OR a.id < sqlc.narg(before_id))
  -- Submission receipts only when asked for (an action filter).
  AND (sqlc.narg(action)::text IS NOT NULL OR a.action <> 'submission.received')
ORDER BY a.id DESC
LIMIT $1;

-- name: ListAuditActions :many
-- The actions recorded, for the filter's suggestions.
SELECT DISTINCT action FROM audit_log ORDER BY action;

-- name: SetAdminTOTP :exec
UPDATE admins SET totp_secret = $2 WHERE id = $1;

-- name: InsertSubmissionReceipt :exec
-- Tamper evidence: every submission, with the SHA-256 of its files, joins
-- the audit chain as it arrives.
INSERT INTO audit_log (actor, action, target_type, target_id, details, ip)
VALUES (@actor::text, 'submission.received', 'submission', @submission_id::bigint, @details::jsonb, @ip::text);

-- name: AuditHead :one
-- The latest entry of the chain (audit_log_seq_idx).
SELECT seq::bigint AS seq, hash::text AS hash, created_at FROM audit_log WHERE seq IS NOT NULL ORDER BY seq DESC LIMIT 1;

-- name: AuditChain :many
-- The chain in order with each entry's recomputed hash (verification;
-- audit_log_seq_idx), a page at a time.
SELECT seq::bigint AS seq, id, prev_hash, coalesce(hash, '')::text AS hash,
       audit_hash(prev_hash, seq, actor, action, target_type, target_id, details, ip, created_at)::text AS expected
FROM audit_log
WHERE seq > @after_seq::bigint
ORDER BY seq
LIMIT @max_rows::integer;

-- name: CountUnchainedAudit :one
-- Entries outside the chain (should be none).
SELECT count(*)::bigint FROM audit_log WHERE seq IS NULL OR hash IS NULL;

-- name: LeaderParticipations :many
-- A delegation leader's contestants: the participations of their team
-- (a sequential scan of participations, one row per contestant and
-- contest; leaders' pages are rare).
SELECT p.id, p.contest_id, c.name AS contest_name, c.title AS contest_title, c.start_time, c.stop_time,
       c.score_visibility, c.scoring_mode, u.username, u.first_name, u.last_name
FROM participations p
JOIN users u ON u.id = p.user_id
JOIN contests c ON c.id = p.contest_id
WHERE p.team_id = @team_id::bigint
ORDER BY c.start_time DESC, u.username;

-- name: LeaderSubmissions :many
-- Their submissions with what the contestants themselves see (the public
-- score and verdict on the live dataset), newest first
-- (submissions_participation_task_idx).
SELECT s.id, s.participation_id, s.submitted_at, s.language, t.name AS task_name, t.score_precision,
       sr.public_score, sr.verdict, (sr.scored_at IS NOT NULL)::boolean AS scored, sr.compilation_outcome,
       s.invalidated_at
FROM submissions s
JOIN tasks t ON t.id = s.task_id
LEFT JOIN submission_results sr ON sr.submission_id = s.id AND sr.dataset_id = t.active_dataset_id
WHERE s.participation_id = ANY(@participation_ids::bigint[])
ORDER BY s.id DESC
LIMIT @max_rows::integer;

-- name: LeaderSubmission :one
-- One submission, if it belongs to the team.
SELECT s.id, s.participation_id, s.submitted_at, s.language, t.name AS task_name, t.title AS task_title, t.score_precision,
       sr.public_score, sr.verdict, (sr.scored_at IS NOT NULL)::boolean AS scored, sr.compilation_outcome, sr.compilation_text,
       u.username, c.name AS contest_name, c.stop_time, c.score_visibility, c.scoring_mode
FROM submissions s
JOIN participations p ON p.id = s.participation_id
JOIN users u ON u.id = p.user_id
JOIN contests c ON c.id = p.contest_id
JOIN tasks t ON t.id = s.task_id
LEFT JOIN submission_results sr ON sr.submission_id = s.id AND sr.dataset_id = t.active_dataset_id
WHERE s.id = @id::bigint AND p.team_id = @team_id::bigint;
