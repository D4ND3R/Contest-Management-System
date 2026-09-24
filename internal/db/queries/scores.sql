-- name: UpsertParticipationTaskScore :exec
INSERT INTO participation_task_scores (participation_id, task_id, score, subtask_scores, icpc_solved,
    icpc_attempts, icpc_solved_at, pending, last_submission_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
ON CONFLICT (participation_id, task_id) DO UPDATE SET
    score = EXCLUDED.score, subtask_scores = EXCLUDED.subtask_scores, icpc_solved = EXCLUDED.icpc_solved,
    icpc_attempts = EXCLUDED.icpc_attempts, icpc_solved_at = EXCLUDED.icpc_solved_at,
    pending = EXCLUDED.pending, last_submission_at = EXCLUDED.last_submission_at, updated_at = now();

-- name: ListParticipationTaskScoresByContest :many
SELECT s.* FROM participation_task_scores s
JOIN participations p ON p.id = s.participation_id
WHERE p.contest_id = $1;

-- name: GetParticipationTaskScore :one
SELECT * FROM participation_task_scores WHERE participation_id = $1 AND task_id = $2;
