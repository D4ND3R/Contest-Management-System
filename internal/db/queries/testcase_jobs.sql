-- name: CreateTestcaseJob :one
INSERT INTO testcase_jobs (dataset_id, codename, public, admin_id, input_digest, output, solution_id, user_test_id, state)
VALUES (@dataset_id, @codename, @public, @admin_id, @input_digest, @output, @solution_id, @user_test_id, @state)
RETURNING *;

-- name: ListTestcaseJobs :many
-- The dataset page: jobs still running or failed (testcase_jobs_dataset_idx).
SELECT * FROM testcase_jobs WHERE dataset_id = $1 ORDER BY id;

-- name: LockTestcaseJobByUserTest :one
-- The dispatcher: the job waiting for a run that ended (testcase_jobs_user_test_idx).
SELECT * FROM testcase_jobs WHERE user_test_id = $1 AND state IN ('input', 'output') FOR UPDATE;

-- name: SetTestcaseJobStep :exec
UPDATE testcase_jobs SET state = @state, input_digest = @input_digest, user_test_id = @user_test_id, error = @error
WHERE id = @id;

-- name: DeleteTestcaseJob :exec
DELETE FROM testcase_jobs WHERE id = $1;

-- name: DeleteFailedTestcaseJobs :exec
DELETE FROM testcase_jobs WHERE dataset_id = $1 AND state = 'failed';

-- name: CountTestcaseJobsRunning :one
SELECT count(*)::bigint FROM testcase_jobs WHERE dataset_id = $1 AND state IN ('input', 'output');
