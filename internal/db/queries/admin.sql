-- name: CreateAdmin :one
INSERT INTO admins (name, username, password_hash, enabled, role) VALUES ($1, $2, $3, $4, $5) RETURNING *;

-- name: GetAdmin :one
SELECT * FROM admins WHERE id = $1;

-- name: GetAdminByUsername :one
SELECT * FROM admins WHERE username = $1;

-- name: ListAdmins :many
SELECT * FROM admins ORDER BY username;

-- name: UpdateAdmin :one
UPDATE admins SET name = $2, username = $3, enabled = $4, role = $5 WHERE id = $1 RETURNING *;

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
ORDER BY a.id DESC
LIMIT $1;

-- name: ListAuditActions :many
-- The actions recorded, for the filter's suggestions.
SELECT DISTINCT action FROM audit_log ORDER BY action;

-- name: SetAdminTOTP :exec
UPDATE admins SET totp_secret = $2 WHERE id = $1;
