-- name: CreateSubmission :one
INSERT INTO submissions (participation_id, task_id, submitted_at, language, comment, official)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: CreateSubmissionFiles :copyfrom
INSERT INTO submission_files (submission_id, filename, digest) VALUES ($1, $2, $3);

-- name: GetSubmission :one
SELECT * FROM submissions WHERE id = $1;

-- name: ListSubmissionFiles :many
SELECT * FROM submission_files WHERE submission_id = $1 ORDER BY filename;

-- name: ListSubmissionFilesBySubmissions :many
SELECT * FROM submission_files WHERE submission_id = ANY(@ids::bigint[]) ORDER BY submission_id, filename;

-- name: ListSubmissionsByParticipationTask :many
SELECT * FROM submissions WHERE participation_id = @participation_id::bigint AND task_id = @task_id::bigint ORDER BY submitted_at, id;

-- name: ListSubmissionsByParticipation :many
SELECT * FROM submissions WHERE participation_id = @participation_id::bigint ORDER BY submitted_at, id;

-- name: SubmissionStats :one
-- Counts and last submission time used to enforce the contest-wide and
-- per-task limits (of a contestant, or of a whole team) in one index scan.
SELECT count(*)::bigint AS contest_count,
       (count(*) FILTER (WHERE task_id = @task_id::bigint))::bigint AS task_count,
       COALESCE(max(submitted_at), 'epoch')::timestamptz AS contest_last,
       COALESCE(max(submitted_at) FILTER (WHERE task_id = @task_id::bigint), 'epoch')::timestamptz AS task_last
FROM submissions WHERE participation_id = ANY(@participation_ids::bigint[]) AND official;

-- name: DeleteSubmission :exec
DELETE FROM submissions WHERE id = $1;

-- name: CreateToken :one
INSERT INTO tokens (submission_id, played_at) VALUES ($1, $2) RETURNING *;

-- name: ListTokenTimesByParticipation :many
-- Token history of a participation: (task, time) pairs for token accounting.
SELECT s.task_id, k.played_at, k.submission_id
FROM tokens k JOIN submissions s ON s.id = k.submission_id
WHERE s.participation_id = @participation_id::bigint
ORDER BY k.played_at;

-- name: GetToken :one
SELECT * FROM tokens WHERE submission_id = $1;

-- name: EnsureSubmissionResult :exec
INSERT INTO submission_results (submission_id, dataset_id) VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: GetSubmissionResult :one
SELECT * FROM submission_results WHERE submission_id = $1 AND dataset_id = $2;

-- name: ListSubmissionResultsBySubmissions :many
SELECT * FROM submission_results WHERE submission_id = ANY(@ids::bigint[]) AND dataset_id = ANY(@dataset_ids::bigint[]);

-- name: ListExecutables :many
SELECT * FROM executables WHERE submission_id = $1 AND dataset_id = $2 ORDER BY filename;

-- name: ListEvaluations :many
SELECT * FROM evaluations WHERE submission_id = $1 AND dataset_id = $2;

-- name: ListEvaluationsWithTestcase :many
SELECT e.*, t.codename, t.public
FROM evaluations e JOIN testcases t ON t.id = e.testcase_id
WHERE e.submission_id = $1 AND e.dataset_id = $2
ORDER BY t.codename;

-- name: SetCompilationResult :one
-- Stores a compilation outcome unless the result was invalidated meanwhile
-- (generation mismatch); returns the number of updated rows.
UPDATE submission_results SET
    compilation_outcome = $3, compilation_text = $4, compilation_stdout = $5,
    compilation_stderr = $6, compilation_tries = compilation_tries + 1, compilation_time = $7,
    compilation_wall_time = $8, compilation_memory = $9, compilation_worker = $10,
    testcases_total = $11
WHERE submission_id = $1 AND dataset_id = $2 AND generation = @generation::integer
RETURNING submission_id;

-- name: InsertExecutable :exec
INSERT INTO executables (submission_id, dataset_id, filename, digest) VALUES ($1, $2, $3, $4)
ON CONFLICT (submission_id, dataset_id, filename) DO UPDATE SET digest = EXCLUDED.digest;

-- name: UpsertEvaluation :exec
INSERT INTO evaluations (submission_id, dataset_id, testcase_id, outcome, text, execution_time,
    execution_wall_time, execution_memory, exit_status, exit_code, signal, worker)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (submission_id, dataset_id, testcase_id) DO UPDATE SET
    outcome = EXCLUDED.outcome, text = EXCLUDED.text, execution_time = EXCLUDED.execution_time,
    execution_wall_time = EXCLUDED.execution_wall_time, execution_memory = EXCLUDED.execution_memory,
    exit_status = EXCLUDED.exit_status, exit_code = EXCLUDED.exit_code, signal = EXCLUDED.signal,
    worker = EXCLUDED.worker, created_at = now();

-- name: RefreshTestcasesDone :one
UPDATE submission_results sr
SET testcases_done = (SELECT count(*) FROM evaluations e WHERE e.submission_id = sr.submission_id AND e.dataset_id = sr.dataset_id)
WHERE sr.submission_id = $1 AND sr.dataset_id = $2
RETURNING testcases_done, testcases_total;

-- name: SetEvaluationDone :exec
UPDATE submission_results SET evaluation_outcome = 'ok', evaluation_tries = evaluation_tries + 1
WHERE submission_id = $1 AND dataset_id = $2 AND generation = @generation::integer;

-- name: SetScore :exec
UPDATE submission_results SET score = $3, score_details = $4, public_score = $5,
    public_score_details = $6, ranking_score_details = $7, verdict = $8, scored_at = now()
WHERE submission_id = $1 AND dataset_id = $2;

-- name: SetSubmissionSystemError :exec
UPDATE submission_results SET system_error = $3 WHERE submission_id = $1 AND dataset_id = $2;

-- name: InvalidateSubmissionResult :one
-- level: 'compilation' drops everything; 'evaluation' keeps the executables;
-- 'score' only clears the score. Returns the new generation.
UPDATE submission_results SET
    generation = generation + CASE WHEN @level::text = 'score' THEN 0 ELSE 1 END,
    compilation_outcome = CASE WHEN @level::text = 'compilation' THEN NULL ELSE compilation_outcome END,
    compilation_text = CASE WHEN @level::text = 'compilation' THEN '' ELSE compilation_text END,
    compilation_stdout = CASE WHEN @level::text = 'compilation' THEN '' ELSE compilation_stdout END,
    compilation_stderr = CASE WHEN @level::text = 'compilation' THEN '' ELSE compilation_stderr END,
    compilation_tries = CASE WHEN @level::text = 'compilation' THEN 0 ELSE compilation_tries END,
    evaluation_outcome = CASE WHEN @level::text = 'score' THEN evaluation_outcome ELSE NULL END,
    evaluation_tries = CASE WHEN @level::text = 'score' THEN evaluation_tries ELSE 0 END,
    testcases_done = CASE WHEN @level::text = 'score' THEN testcases_done ELSE 0 END,
    score = NULL, score_details = NULL, public_score = NULL, public_score_details = NULL,
    ranking_score_details = NULL, verdict = NULL, scored_at = NULL, system_error = NULL, jobs_enqueued_at = NULL
WHERE submission_id = $1 AND dataset_id = $2
RETURNING generation;

-- name: DeleteEvaluations :exec
DELETE FROM evaluations WHERE submission_id = $1 AND dataset_id = $2;

-- name: DeleteExecutables :exec
DELETE FROM executables WHERE submission_id = $1 AND dataset_id = $2;

-- name: ListPendingSubmissionResults :many
-- Sweeper: results not yet scored, oldest first.
SELECT sr.submission_id, sr.dataset_id, sr.generation, sr.compilation_outcome, sr.evaluation_outcome,
       sr.compilation_tries, sr.evaluation_tries, sr.system_error
FROM submission_results sr
WHERE sr.scored_at IS NULL
ORDER BY sr.submission_id
LIMIT $1;

-- name: PlagiarismCandidates :many
-- One submission per participation for the plagiarism report of a task:
-- the latest official, valid submission or, with best, the best scored
-- one (ties: the latest). Served by submissions_task_idx.
SELECT DISTINCT ON (s.participation_id)
    s.id, s.participation_id, s.submitted_at, s.language, u.username, p.team_id, r.score
FROM submissions s
JOIN participations p ON p.id = s.participation_id
JOIN users u ON u.id = p.user_id
JOIN tasks t ON t.id = s.task_id
LEFT JOIN submission_results r ON r.submission_id = s.id AND r.dataset_id = t.active_dataset_id
WHERE s.task_id = sqlc.arg(task_id)::bigint AND p.contest_id = sqlc.arg(contest_id)::bigint
  AND s.official AND s.invalidated_at IS NULL
ORDER BY s.participation_id,
    CASE WHEN sqlc.arg(best)::boolean THEN COALESCE(r.score, -1) ELSE 0 END DESC,
    s.submitted_at DESC, s.id DESC;

-- name: InsertSubmissionFlag :exec
INSERT INTO submission_flags (submission_id, kind, reason, detail) VALUES ($1, $2, $3, $4)
ON CONFLICT DO NOTHING;

-- name: ListSubmissionFlags :many
SELECT * FROM submission_flags WHERE submission_id = $1 ORDER BY created_at, kind, reason;

-- name: GetCompilationCache :one
-- A remembered compilation (SPEC_IOI H4), by the hash of its inputs.
SELECT * FROM compilation_cache WHERE key = $1;

-- name: ListCompilationCacheFiles :many
SELECT * FROM compilation_cache_files WHERE key = $1 ORDER BY filename;

-- name: UseCompilationCache :exec
UPDATE compilation_cache SET hits = hits + 1, used_at = now() WHERE key = $1;

-- name: InsertCompilationCache :execrows
INSERT INTO compilation_cache (key, text, stdout, stderr, time, wall_time, memory)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (key) DO NOTHING;

-- name: InsertCompilationCacheFile :exec
INSERT INTO compilation_cache_files (key, filename, digest, size) VALUES ($1, $2, $3, $4)
ON CONFLICT DO NOTHING;

-- name: PruneCompilationCache :execrows
-- Entries not used since before @before (compilation_cache_used_idx).
DELETE FROM compilation_cache WHERE used_at < @before::timestamptz;

-- name: ClearCompilationCache :exec
-- An explicit recompilation must run the compilers (they may have been
-- upgraded, which the cache key cannot see).
DELETE FROM compilation_cache;

-- name: ListUnfinishedOlderSubmissions :many
-- A contestant's earlier submissions to a task still being judged
-- (submissions_participation_task_idx; a handful at most).
SELECT DISTINCT s.id
FROM submissions s
JOIN submission_results sr ON sr.submission_id = s.id
WHERE s.participation_id = @participation_id::bigint AND s.task_id = @task_id::bigint AND s.id < @before_id::bigint
  AND sr.scored_at IS NULL AND sr.system_error IS NULL;
