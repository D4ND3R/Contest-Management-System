-- name: ListReplaySubmissions :many
-- A contest's official, valid submissions in arrival order, with who and
-- which task (by its place in the contest): what a rehearsal replays
-- (submissions_participation_task_idx through the contest's
-- participations).
SELECT s.id, s.submitted_at, s.language, u.username, t.num AS task_num, t.name AS task_name
FROM participations p
JOIN users u ON u.id = p.user_id
JOIN submissions s ON s.participation_id = p.id
JOIN tasks t ON t.id = s.task_id AND t.contest_id = p.contest_id
WHERE p.contest_id = @contest_id::bigint AND s.official AND s.invalidated_at IS NULL AND NOT s.tester
ORDER BY s.submitted_at, s.id;

-- name: ReplayLatencies :many
-- For replayed submissions: seconds from arrival to score on the live
-- dataset (-1 while not scored).
SELECT s.id, COALESCE(extract(epoch FROM sr.scored_at - s.submitted_at), -1)::float8 AS seconds, (sr.system_error IS NOT NULL)::boolean AS failed
FROM submissions s
JOIN tasks t ON t.id = s.task_id
LEFT JOIN submission_results sr ON sr.submission_id = s.id AND sr.dataset_id = t.active_dataset_id
WHERE s.id = ANY(@ids::bigint[]);
