-- name: CreateDataset :one
INSERT INTO datasets (
    task_id, description, autojudge, time_limit_ms, wall_time_limit_ms, memory_limit_bytes,
    output_limit_bytes, process_limit, source_size_limit_bytes, task_type, task_type_params,
    score_type, score_type_params, short_circuit
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
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
    score_type = $12, score_type_params = $13, short_circuit = $14
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

-- name: CreateTestcases :copyfrom
-- Bulk load of a new dataset's testcases (problem package import).
INSERT INTO testcases (dataset_id, codename, public, input_digest, output_digest) VALUES ($1, $2, $3, $4, $5);

-- name: CreateManagers :copyfrom
INSERT INTO managers (dataset_id, filename, digest) VALUES ($1, $2, $3);

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

-- name: DeleteDatasetTestcases :exec
-- Every testcase of a dataset (a package filling it replaces them).
DELETE FROM testcases WHERE dataset_id = $1;

-- name: DeleteDatasetManagers :exec
DELETE FROM managers WHERE dataset_id = $1;

-- name: SetDatasetPublicTestcases :exec
-- problem.yaml edited in the administration: exactly @public are public.
UPDATE testcases SET public = (codename = ANY(@public::text[]))
WHERE dataset_id = @dataset_id::bigint AND public <> (codename = ANY(@public::text[]));

-- name: ListTestcasesWithSizes :many
-- The testcases window: sizes come from the blob registry (primary key).
SELECT t.*, COALESCE(bi.size, -1)::bigint AS input_size, COALESCE(bo.size, -1)::bigint AS output_size
FROM testcases t
LEFT JOIN blobs bi ON bi.digest = t.input_digest
LEFT JOIN blobs bo ON bo.digest = t.output_digest
WHERE t.dataset_id = $1
ORDER BY t.codename;

-- name: CountTestcases :one
SELECT count(*) FROM testcases WHERE dataset_id = $1;

-- name: ListWarmDigests :many
-- What the workers download ahead of time (SPEC_IOI §11): the testcases
-- and managers of the live datasets of the contests running now or
-- starting before @until. A handful of contests; testcases and managers
-- are read through their (dataset_id, ...) unique indexes.
SELECT DISTINCT x.digest::text AS digest FROM (
    SELECT tc.input_digest AS digest
    FROM contests c JOIN tasks t ON t.contest_id = c.id JOIN testcases tc ON tc.dataset_id = t.active_dataset_id
    WHERE c.start_time < @until::timestamptz AND c.stop_time > now()
  UNION ALL
    SELECT tc.output_digest
    FROM contests c JOIN tasks t ON t.contest_id = c.id JOIN testcases tc ON tc.dataset_id = t.active_dataset_id
    WHERE c.start_time < @until::timestamptz AND c.stop_time > now()
  UNION ALL
    SELECT m.digest
    FROM contests c JOIN tasks t ON t.contest_id = c.id JOIN managers m ON m.dataset_id = t.active_dataset_id
    WHERE c.start_time < @until::timestamptz AND c.stop_time > now()
) x;

-- name: CompareDatasetSubmissions :many
-- Every counted submission of a task with its results on two datasets
-- (the dataset comparison page; submissions_task_idx, then the results'
-- primary key): scores per submission and the inputs of the task score.
SELECT s.id, s.participation_id, u.username, s.submitted_at, s.official, (k.submission_id IS NOT NULL)::boolean AS tokened,
       ra.compilation_outcome AS compilation_a, ra.score AS score_a, ra.ranking_score_details AS details_a, ra.scored_at AS scored_at_a,
       rb.compilation_outcome AS compilation_b, rb.score AS score_b, rb.ranking_score_details AS details_b, rb.scored_at AS scored_at_b
FROM submissions s
JOIN participations p ON p.id = s.participation_id
JOIN users u ON u.id = p.user_id
LEFT JOIN submission_results ra ON ra.submission_id = s.id AND ra.dataset_id = @dataset_a::bigint
LEFT JOIN submission_results rb ON rb.submission_id = s.id AND rb.dataset_id = @dataset_b::bigint
LEFT JOIN tokens k ON k.submission_id = s.id
WHERE s.task_id = @task_id::bigint AND s.invalidated_at IS NULL AND NOT s.tester
ORDER BY u.username, s.submitted_at, s.id;
