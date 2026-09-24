-- name: CreateTask :one
INSERT INTO tasks (
    contest_id, num, name, title, primary_statements, submission_format,
    token_mode, token_max_number, token_min_interval_s, token_gen_initial, token_gen_number,
    token_gen_interval_s, token_gen_max, max_submission_number, max_user_test_number,
    min_submission_interval_s, min_user_test_interval_s, feedback_level, score_precision, score_mode
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20
) RETURNING *;

-- name: GetTask :one
SELECT * FROM tasks WHERE id = $1;

-- name: GetTaskByName :one
SELECT * FROM tasks WHERE name = $1;

-- name: ListTasksByContest :many
SELECT * FROM tasks WHERE contest_id = $1 ORDER BY num, id;

-- name: ListTasks :many
SELECT * FROM tasks ORDER BY contest_id NULLS LAST, num, id;

-- name: UpdateTask :one
UPDATE tasks SET
    contest_id = $2, num = $3, name = $4, title = $5, primary_statements = $6,
    submission_format = $7, token_mode = $8, token_max_number = $9, token_min_interval_s = $10,
    token_gen_initial = $11, token_gen_number = $12, token_gen_interval_s = $13,
    token_gen_max = $14, max_submission_number = $15, max_user_test_number = $16,
    min_submission_interval_s = $17, min_user_test_interval_s = $18, feedback_level = $19,
    score_precision = $20, score_mode = $21, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetTaskContest :exec
UPDATE tasks SET contest_id = $2, num = $3, updated_at = now() WHERE id = $1;

-- name: SetActiveDataset :exec
UPDATE tasks SET active_dataset_id = $2, updated_at = now() WHERE id = $1;

-- name: DeleteTask :exec
DELETE FROM tasks WHERE id = $1;

-- name: UpsertStatement :one
INSERT INTO statements (task_id, language, digest, content_type) VALUES ($1, $2, $3, $4)
ON CONFLICT (task_id, language) DO UPDATE SET digest = EXCLUDED.digest, content_type = EXCLUDED.content_type
RETURNING *;

-- name: ListStatements :many
SELECT * FROM statements WHERE task_id = $1 ORDER BY language;

-- name: ListStatementsByContest :many
SELECT s.* FROM statements s JOIN tasks t ON t.id = s.task_id WHERE t.contest_id = $1 ORDER BY s.task_id, s.language;

-- name: DeleteStatement :exec
DELETE FROM statements WHERE task_id = $1 AND language = $2;

-- name: UpsertAttachment :one
INSERT INTO attachments (task_id, filename, digest) VALUES ($1, $2, $3)
ON CONFLICT (task_id, filename) DO UPDATE SET digest = EXCLUDED.digest
RETURNING *;

-- name: ListAttachments :many
SELECT * FROM attachments WHERE task_id = $1 ORDER BY filename;

-- name: ListAttachmentsByContest :many
SELECT a.* FROM attachments a JOIN tasks t ON t.id = a.task_id WHERE t.contest_id = $1 ORDER BY a.task_id, a.filename;

-- name: DeleteAttachment :exec
DELETE FROM attachments WHERE task_id = $1 AND filename = $2;
