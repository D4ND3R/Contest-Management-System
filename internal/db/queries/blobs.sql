-- name: RegisterBlob :exec
INSERT INTO blobs (digest, size, description) VALUES ($1, $2, $3) ON CONFLICT (digest) DO NOTHING;

-- name: GetBlob :one
SELECT * FROM blobs WHERE digest = $1;

-- name: ListUnreferencedBlobs :many
-- Garbage-collection candidates: registered blobs no table references,
-- older than the grace period (uploads in flight are not yet referenced).
SELECT b.digest, b.size FROM blobs b
WHERE b.created_at < @older_than::timestamptz
  AND NOT EXISTS (SELECT 1 FROM statements x WHERE x.digest = b.digest)
  AND NOT EXISTS (SELECT 1 FROM attachments x WHERE x.digest = b.digest)
  AND NOT EXISTS (SELECT 1 FROM managers x WHERE x.digest = b.digest)
  AND NOT EXISTS (SELECT 1 FROM testcases x WHERE x.input_digest = b.digest OR x.output_digest = b.digest)
  AND NOT EXISTS (SELECT 1 FROM submission_files x WHERE x.digest = b.digest)
  AND NOT EXISTS (SELECT 1 FROM executables x WHERE x.digest = b.digest)
  AND NOT EXISTS (SELECT 1 FROM user_tests x WHERE x.input_digest = b.digest)
  AND NOT EXISTS (SELECT 1 FROM user_test_files x WHERE x.digest = b.digest)
  AND NOT EXISTS (SELECT 1 FROM user_test_executables x WHERE x.digest = b.digest)
  AND NOT EXISTS (SELECT 1 FROM user_test_results x WHERE x.output_digest = b.digest)
  AND NOT EXISTS (SELECT 1 FROM print_jobs x WHERE x.digest = b.digest)
  AND NOT EXISTS (SELECT 1 FROM teams x WHERE x.flag_digest = b.digest OR x.photo_digest = b.digest)
  AND NOT EXISTS (SELECT 1 FROM users x WHERE x.photo_digest = b.digest)
LIMIT $1;

-- name: DeleteBlobRecord :exec
DELETE FROM blobs WHERE digest = $1;

-- name: UpsertLanguage :exec
INSERT INTO languages (id, name, config) VALUES ($1, $2, $3)
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, config = EXCLUDED.config, updated_at = now();

-- name: ListLanguages :many
SELECT * FROM languages ORDER BY id;
