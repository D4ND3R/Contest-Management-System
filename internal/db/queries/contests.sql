-- name: CreateContest :one
INSERT INTO contests (
    name, description, allowed_localizations, languages, submissions_download_allowed,
    allow_questions, allow_user_tests, allow_printing, block_hidden_participations,
    allow_password_authentication, ip_restriction, ip_autologin, single_login,
    token_mode, token_max_number, token_min_interval_s, token_gen_initial, token_gen_number,
    token_gen_interval_s, token_gen_max, start_time, stop_time, analysis_enabled,
    analysis_start, analysis_stop, timezone, per_user_time_s, max_submission_number,
    max_user_test_number, min_submission_interval_s, min_user_test_interval_s, score_precision,
    scoring_mode, icpc_penalty_minutes, ranking_freeze_time, max_print_jobs, max_print_pages
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20,
    $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31, $32, $33, $34, $35, $36, $37
) RETURNING *;

-- name: GetContest :one
SELECT * FROM contests WHERE id = $1;

-- name: GetContestByName :one
SELECT * FROM contests WHERE name = $1;

-- name: ListContests :many
SELECT * FROM contests ORDER BY start_time DESC, id DESC;

-- name: UpdateContest :one
UPDATE contests SET
    name = $2, description = $3, allowed_localizations = $4, languages = $5,
    submissions_download_allowed = $6, allow_questions = $7, allow_user_tests = $8,
    allow_printing = $9, block_hidden_participations = $10, allow_password_authentication = $11,
    ip_restriction = $12, ip_autologin = $13, single_login = $14, token_mode = $15,
    token_max_number = $16, token_min_interval_s = $17, token_gen_initial = $18,
    token_gen_number = $19, token_gen_interval_s = $20, token_gen_max = $21, start_time = $22,
    stop_time = $23, analysis_enabled = $24, analysis_start = $25, analysis_stop = $26,
    timezone = $27, per_user_time_s = $28, max_submission_number = $29,
    max_user_test_number = $30, min_submission_interval_s = $31, min_user_test_interval_s = $32,
    score_precision = $33, scoring_mode = $34, icpc_penalty_minutes = $35,
    ranking_freeze_time = $36, max_print_jobs = $37, max_print_pages = $38, questions_per_minute = $39,
    ranking_visibility = $40, ranking_contestant_view = $41, ranking_when = $42, ranking_freeze_minutes = $43,
    ranking_show_subtasks = $44, ranking_show_flags = $45, ranking_show_institutions = $46,
    ranking_show_hidden = $47, ranking_anonymous = $48, status = $49, practice_enabled = $50,
    default_score_mode = $51, score_visibility = $52, show_compilation_output = $53,
    max_submission_bytes = $54, registration = $55, invitation_code = $56, password_min_length = $57,
    session_minutes = $58, team_mode = $59, max_team_size = $60, max_print_total_pages = $61,
    title = $62, location = $63, tagline = $64, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetContestBanner :exec
-- The banner image (NULL digest: none).
UPDATE contests SET banner_digest = sqlc.narg(digest), banner_type = @media_type, updated_at = now() WHERE id = @id;

-- name: SetContestRankingUnfrozen :exec
UPDATE contests SET ranking_unfrozen = $2, updated_at = now() WHERE id = $1;

-- name: DeleteContest :exec
DELETE FROM contests WHERE id = $1;

-- name: ExtendContest :exec
-- Moves the end (and each per-user window) by some minutes.
UPDATE contests SET
    stop_time = stop_time + (sqlc.arg(minutes)::integer * interval '1 minute'),
    per_user_time_s = per_user_time_s + (sqlc.arg(minutes)::integer * 60),
    updated_at = now()
WHERE id = sqlc.arg(id)::bigint;

-- name: ListRunningContests :many
-- Published contests whose official window is open now for somebody (the
-- stop time plus the longest delay and extra time of a participant):
-- `cmsctl upgrade` does not stop the services then. Few contests: the scan
-- needs no index (participations are read through their contest_id index).
SELECT c.id, c.name,
       (c.stop_time + make_interval(secs => coalesce(max(p.delay_time_s + p.extra_time_s), 0)::double precision))::timestamptz AS ends_at
FROM contests c
LEFT JOIN participations p ON p.contest_id = c.id
WHERE c.status = 'published' AND c.start_time <= now()
GROUP BY c.id
HAVING c.stop_time + make_interval(secs => coalesce(max(p.delay_time_s + p.extra_time_s), 0)::double precision) > now()
ORDER BY c.start_time;
