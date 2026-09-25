-- name: ListContestSolves :many
-- Every solved (participation, task) of a contest, first solves first, for
-- the balloons page (index: participations (contest_id, user_id) unique,
-- then the participation_task_scores primary key).
SELECT p.id AS participation_id, s.task_id, s.icpc_solved_at, u.username, u.first_name, u.last_name,
       tm.code AS team_code, tm.name AS team_name, st.name AS site_name
FROM participations p
JOIN participation_task_scores s ON s.participation_id = p.id
JOIN users u ON u.id = p.user_id
LEFT JOIN teams tm ON tm.id = p.team_id
LEFT JOIN sites st ON st.id = p.site_id
WHERE p.contest_id = $1 AND s.icpc_solved AND s.icpc_solved_at IS NOT NULL AND NOT p.hidden AND p.approved
ORDER BY s.icpc_solved_at, p.id;

-- name: ListBalloonDeliveries :many
SELECT b.task_id, b.recipient, b.delivered_at, COALESCE(a.username, '')::text AS delivered_by
FROM balloons b
JOIN tasks t ON t.id = b.task_id
LEFT JOIN admins a ON a.id = b.delivered_by
WHERE t.contest_id = sqlc.arg(contest_id)::bigint;

-- name: DeliverBalloon :exec
INSERT INTO balloons (task_id, recipient, delivered_by) VALUES ($1, $2, $3)
ON CONFLICT (task_id, recipient) DO NOTHING;

-- name: UndeliverBalloon :exec
DELETE FROM balloons WHERE task_id = $1 AND recipient = $2;
