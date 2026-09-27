-- name: GetServerSettings :one
SELECT timezone, updated_at FROM server_settings WHERE id;

-- name: SetServerTimezone :exec
UPDATE server_settings SET timezone = $1, updated_at = now() WHERE id;

-- The contests follow the new zone (after SetServerTimezone, in the same
-- transaction: the trigger copies the server's zone); their ids tell the
-- web servers which caches to drop.
-- name: SyncContestsTimezone :many
UPDATE contests SET timezone = $1 WHERE timezone <> $1 RETURNING id;
