-- name: CreateUser :one
INSERT INTO users (username, first_name, last_name, email, password_hash, timezone, preferred_languages,
                   institution, country, region)
VALUES (@username, @first_name, @last_name, @email, @password_hash, @timezone, @preferred_languages,
        @institution, @country, @region)
RETURNING *;

-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByUsername :one
SELECT * FROM users WHERE username = $1;

-- name: ListUsers :many
SELECT * FROM users ORDER BY username;

-- name: UpdateUser :one
UPDATE users SET username = @username, first_name = @first_name, last_name = @last_name, email = @email,
    timezone = @timezone, preferred_languages = @preferred_languages, institution = @institution,
    country = @country, region = @region
WHERE id = @id
RETURNING *;

-- name: SetUserPhoto :exec
UPDATE users SET photo_digest = $2 WHERE id = $1;

-- name: SetUserDisabled :exec
UPDATE users SET disabled = $2 WHERE id = $1;

-- name: SetUserPassword :exec
UPDATE users SET password_hash = $2 WHERE id = $1;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = $1;

-- name: CreateTeam :one
INSERT INTO teams (code, name, flag_digest, photo_digest, institution) VALUES ($1, $2, $3, $4, $5) RETURNING *;

-- name: GetTeam :one
SELECT * FROM teams WHERE id = $1;

-- name: GetTeamByCode :one
SELECT * FROM teams WHERE code = $1;

-- name: ListTeams :many
SELECT * FROM teams ORDER BY code;

-- name: UpdateTeam :one
UPDATE teams SET code = $2, name = $3, flag_digest = $4, photo_digest = $5, institution = $6 WHERE id = $1 RETURNING *;

-- name: DeleteTeam :exec
DELETE FROM teams WHERE id = $1;

-- name: CreateParticipation :one
INSERT INTO participations (contest_id, user_id, team_id, password_hash, ip, delay_time_s, extra_time_s, hidden, unrestricted)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetParticipation :one
SELECT * FROM participations WHERE id = $1;

-- name: GetParticipationByContestUser :one
SELECT * FROM participations WHERE contest_id = $1 AND user_id = $2;

-- name: GetLoginCandidate :one
-- Everything CWS needs to authenticate a contestant, in one round trip.
SELECT p.id AS participation_id, p.password_hash AS participation_password_hash, p.ip, p.hidden,
       p.login_nonce, u.id AS user_id, u.username, u.password_hash AS user_password_hash, u.disabled
FROM participations p JOIN users u ON u.id = p.user_id
WHERE p.contest_id = $1 AND u.username = $2;

-- name: ListParticipationsByContest :many
SELECT sqlc.embed(p), u.username, u.first_name, u.last_name, u.timezone AS user_timezone,
       u.institution, u.country, u.disabled, t.code AS team_code, t.name AS team_name,
       st.name AS site_name, st.start_time AS site_start_time
FROM participations p
JOIN users u ON u.id = p.user_id
LEFT JOIN teams t ON t.id = p.team_id
LEFT JOIN sites st ON st.id = p.site_id
WHERE p.contest_id = $1
ORDER BY u.username;

-- name: ListParticipationsByUser :many
SELECT * FROM participations WHERE user_id = $1 ORDER BY contest_id;

-- name: ListParticipationsWithIP :many
-- Candidates for IP autologin.
SELECT p.id, p.ip, p.login_nonce FROM participations p WHERE p.contest_id = $1 AND cardinality(p.ip) > 0;

-- name: UpdateParticipation :one
UPDATE participations SET team_id = $2, ip = $3, delay_time_s = $4, extra_time_s = $5, hidden = $6, unrestricted = $7,
    starting_time = $8, site_id = $9
WHERE id = $1
RETURNING *;

-- name: SetParticipationPassword :exec
UPDATE participations SET password_hash = $2 WHERE id = $1;

-- name: StartParticipation :one
-- Sets the per-user start time once (idempotent).
UPDATE participations SET starting_time = COALESCE(starting_time, $2) WHERE id = $1 RETURNING *;

-- name: BumpLoginNonce :one
UPDATE participations SET login_nonce = login_nonce + 1 WHERE id = $1 RETURNING login_nonce;

-- name: DeleteParticipation :exec
DELETE FROM participations WHERE id = $1;

-- name: CreateSite :one
INSERT INTO sites (contest_id, name, start_time) VALUES ($1, $2, $3) RETURNING *;

-- name: ListSites :many
SELECT * FROM sites WHERE contest_id = $1 ORDER BY name;

-- name: GetSite :one
SELECT * FROM sites WHERE id = $1;

-- name: UpdateSite :one
UPDATE sites SET name = $2, start_time = $3 WHERE id = $1 RETURNING *;

-- name: DeleteSite :exec
DELETE FROM sites WHERE id = $1;

-- name: ListTeamMembers :many
-- Participations of a team in every contest.
SELECT p.id, p.contest_id, c.name AS contest_name, u.id AS user_id, u.username, u.first_name, u.last_name
FROM participations p
JOIN users u ON u.id = p.user_id
JOIN contests c ON c.id = p.contest_id
WHERE p.team_id = $1
ORDER BY c.start_time DESC, u.username;

-- name: CountTeamMembers :one
SELECT count(*) FROM participations WHERE contest_id = $1 AND team_id = $2;
