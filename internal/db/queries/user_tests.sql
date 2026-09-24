-- name: CreateUserTest :one
INSERT INTO user_tests (participation_id, task_id, submitted_at, language, input_digest)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: CreateUserTestFiles :copyfrom
INSERT INTO user_test_files (user_test_id, filename, digest) VALUES ($1, $2, $3);

-- name: GetUserTest :one
SELECT * FROM user_tests WHERE id = $1;

-- name: ListUserTestFiles :many
SELECT * FROM user_test_files WHERE user_test_id = $1 ORDER BY filename;

-- name: ListUserTestsByParticipationTask :many
SELECT * FROM user_tests WHERE participation_id = $1 AND task_id = $2 ORDER BY submitted_at, id;

-- name: UserTestStats :one
SELECT count(*)::bigint AS contest_count,
       (count(*) FILTER (WHERE task_id = @task_id::bigint))::bigint AS task_count,
       COALESCE(max(submitted_at), 'epoch')::timestamptz AS contest_last,
       COALESCE(max(submitted_at) FILTER (WHERE task_id = @task_id::bigint), 'epoch')::timestamptz AS task_last
FROM user_tests WHERE participation_id = @participation_id::bigint;

-- name: EnsureUserTestResult :exec
INSERT INTO user_test_results (user_test_id, dataset_id) VALUES ($1, $2) ON CONFLICT DO NOTHING;

-- name: GetUserTestResult :one
SELECT * FROM user_test_results WHERE user_test_id = $1 AND dataset_id = $2;

-- name: ListUserTestResultsByTests :many
SELECT * FROM user_test_results WHERE user_test_id = ANY(@ids::bigint[]) AND dataset_id = ANY(@dataset_ids::bigint[]);

-- name: SetUserTestCompilation :one
UPDATE user_test_results SET
    compilation_outcome = $3, compilation_text = $4, compilation_stdout = $5, compilation_stderr = $6,
    compilation_tries = compilation_tries + 1, compilation_time = $7, compilation_wall_time = $8,
    compilation_memory = $9, completed_at = CASE WHEN $3 = 'fail' THEN now() ELSE completed_at END
WHERE user_test_id = $1 AND dataset_id = $2 AND generation = @generation::integer
RETURNING user_test_id;

-- name: InsertUserTestExecutable :exec
INSERT INTO user_test_executables (user_test_id, dataset_id, filename, digest) VALUES ($1, $2, $3, $4)
ON CONFLICT (user_test_id, dataset_id, filename) DO UPDATE SET digest = EXCLUDED.digest;

-- name: ListUserTestExecutables :many
SELECT * FROM user_test_executables WHERE user_test_id = $1 AND dataset_id = $2 ORDER BY filename;

-- name: SetUserTestEvaluation :one
UPDATE user_test_results SET
    evaluation_outcome = 'ok', evaluation_text = $3, evaluation_tries = evaluation_tries + 1,
    output_digest = $4, execution_time = $5, execution_wall_time = $6, execution_memory = $7,
    exit_status = $8, completed_at = now()
WHERE user_test_id = $1 AND dataset_id = $2 AND generation = @generation::integer
RETURNING user_test_id;

-- name: SetUserTestSystemError :exec
UPDATE user_test_results SET system_error = $3, completed_at = now() WHERE user_test_id = $1 AND dataset_id = $2;

-- name: ListPendingUserTestResults :many
SELECT user_test_id, dataset_id, generation, compilation_outcome, compilation_tries, evaluation_tries
FROM user_test_results WHERE completed_at IS NULL ORDER BY user_test_id LIMIT $1;
