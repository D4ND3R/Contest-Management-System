-- name: UpsertParticipationTaskScore :one
-- Stores the score computed from the submissions plus the manual
-- adjustments of the row; returns the stored score.
INSERT INTO participation_task_scores (participation_id, task_id, score, subtask_scores, icpc_solved,
    icpc_attempts, icpc_solved_at, pending, last_submission_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
ON CONFLICT (participation_id, task_id) DO UPDATE SET
    score = EXCLUDED.score + participation_task_scores.adjustment, subtask_scores = EXCLUDED.subtask_scores,
    icpc_solved = EXCLUDED.icpc_solved, icpc_attempts = EXCLUDED.icpc_attempts, icpc_solved_at = EXCLUDED.icpc_solved_at,
    pending = EXCLUDED.pending, last_submission_at = EXCLUDED.last_submission_at, updated_at = now()
RETURNING score;

-- name: CreateScoreAdjustment :one
INSERT INTO score_adjustments (participation_id, task_id, points, reason, admin_id) VALUES ($1, $2, $3, $4, $5) RETURNING *;

-- name: ApplyScoreAdjustment :exec
-- Adds points to a task score and to its adjustment (creating the row).
INSERT INTO participation_task_scores (participation_id, task_id, score, adjustment, updated_at)
VALUES ($1, $2, sqlc.arg(points)::float8, sqlc.arg(points)::float8, now())
ON CONFLICT (participation_id, task_id) DO UPDATE SET
    score = participation_task_scores.score + EXCLUDED.adjustment,
    adjustment = participation_task_scores.adjustment + EXCLUDED.adjustment, updated_at = now();

-- name: ListScoreAdjustments :many
-- The adjustments of some participations (a contestant or a team), oldest first.
SELECT a.*, t.name AS task_name, COALESCE(ad.username, '')::text AS admin_username, u.username
FROM score_adjustments a
JOIN tasks t ON t.id = a.task_id
JOIN participations p ON p.id = a.participation_id
JOIN users u ON u.id = p.user_id
LEFT JOIN admins ad ON ad.id = a.admin_id
WHERE a.participation_id = ANY(@participation_ids::bigint[])
ORDER BY a.created_at, a.id;

-- name: ListContestScoreAdjustments :many
-- Every adjustment of a contest, for the ranking replay.
SELECT a.participation_id, a.task_id, a.points, a.created_at
FROM score_adjustments a
JOIN participations p ON p.id = a.participation_id
WHERE p.contest_id = $1
ORDER BY a.created_at, a.id;

-- name: ListParticipationTaskScoresByContest :many
SELECT s.* FROM participation_task_scores s
JOIN participations p ON p.id = s.participation_id
WHERE p.contest_id = $1;

-- name: GetParticipationTaskScore :one
SELECT * FROM participation_task_scores WHERE participation_id = $1 AND task_id = $2;
