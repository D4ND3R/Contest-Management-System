-- name: CreateDataset :one
INSERT INTO datasets (
    task_id, description, autojudge, time_limit_ms, wall_time_limit_ms, memory_limit_bytes,
    output_limit_bytes, process_limit, source_size_limit_bytes, task_type, task_type_params,
    score_type, score_type_params
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING *;

-- name: GetDataset :one
SELECT * FROM datasets WHERE id = $1;

-- name: ListDatasetsByTask :many
SELECT * FROM datasets WHERE task_id = $1 ORDER BY id;

-- name: ListDatasetsByIDs :many
SELECT * FROM datasets WHERE id = ANY(@ids::bigint[]);

-- name: ListLiveDatasetsByContest :many
SELECT d.* FROM datasets d JOIN tasks t ON t.active_dataset_id = d.id WHERE t.contest_id = $1;

-- name: ListJudgedDatasetsByTask :many
-- Datasets on which new submissions must be judged: the live one plus
-- every autojudge dataset.
SELECT d.* FROM datasets d JOIN tasks t ON t.id = d.task_id
WHERE d.task_id = $1 AND (d.id = t.active_dataset_id OR d.autojudge)
ORDER BY (d.id = t.active_dataset_id) DESC, d.id;

-- name: UpdateDataset :one
UPDATE datasets SET
    description = $2, autojudge = $3, time_limit_ms = $4, wall_time_limit_ms = $5,
    memory_limit_bytes = $6, output_limit_bytes = $7, process_limit = $8,
    source_size_limit_bytes = $9, task_type = $10, task_type_params = $11,
    score_type = $12, score_type_params = $13
WHERE id = $1
RETURNING *;

-- name: DeleteDataset :exec
DELETE FROM datasets WHERE id = $1;

-- name: CloneDatasetContents :exec
-- Copies managers and testcases of @src into @dst.
WITH m AS (
    INSERT INTO managers (dataset_id, filename, digest)
    SELECT @dst::bigint, filename, digest FROM managers WHERE dataset_id = @src::bigint
)
INSERT INTO testcases (dataset_id, codename, public, input_digest, output_digest)
SELECT @dst::bigint, codename, public, input_digest, output_digest FROM testcases WHERE dataset_id = @src::bigint;

-- name: UpsertManager :one
INSERT INTO managers (dataset_id, filename, digest) VALUES ($1, $2, $3)
ON CONFLICT (dataset_id, filename) DO UPDATE SET digest = EXCLUDED.digest
RETURNING *;

-- name: ListManagers :many
SELECT * FROM managers WHERE dataset_id = $1 ORDER BY filename;

-- name: ListManagersByDatasets :many
SELECT * FROM managers WHERE dataset_id = ANY(@ids::bigint[]) ORDER BY dataset_id, filename;

-- name: DeleteManager :exec
DELETE FROM managers WHERE dataset_id = $1 AND filename = $2;

-- name: UpsertTestcase :one
INSERT INTO testcases (dataset_id, codename, public, input_digest, output_digest) VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (dataset_id, codename) DO UPDATE
SET public = EXCLUDED.public, input_digest = EXCLUDED.input_digest, output_digest = EXCLUDED.output_digest
RETURNING *;

-- name: GetTestcase :one
SELECT * FROM testcases WHERE id = $1;

-- name: ListTestcases :many
SELECT * FROM testcases WHERE dataset_id = $1 ORDER BY codename;

-- name: ListTestcasesByDatasets :many
SELECT * FROM testcases WHERE dataset_id = ANY(@ids::bigint[]) ORDER BY dataset_id, codename;

-- name: SetTestcasePublic :exec
UPDATE testcases SET public = $2 WHERE id = $1;

-- name: DeleteTestcase :exec
DELETE FROM testcases WHERE id = $1;

-- name: CountTestcases :one
SELECT count(*) FROM testcases WHERE dataset_id = $1;
