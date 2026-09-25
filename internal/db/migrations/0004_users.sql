-- User management (SPEC_AUDIT §3).

-- Profile fields, a photo, and a switch that blocks logins without
-- deleting anything.
ALTER TABLE users ADD COLUMN institution text NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN country text NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN region text NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN photo_digest sha256_digest;
ALTER TABLE users ADD COLUMN disabled boolean NOT NULL DEFAULT false;

ALTER TABLE teams ADD COLUMN institution text NOT NULL DEFAULT '';

-- Sites (venues) of a multi-site contest. A site may start at its own time
-- (the contest duration is kept); participants belong to at most one site
-- and the ranking can be filtered by site.
CREATE TABLE sites (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    contest_id bigint NOT NULL REFERENCES contests(id) ON DELETE CASCADE,
    name       text NOT NULL,
    start_time timestamptz,
    UNIQUE (contest_id, name)
);
ALTER TABLE participations ADD COLUMN site_id bigint REFERENCES sites(id) ON DELETE SET NULL;

-- Optional TOTP second factor of administrators (base32 secret).
ALTER TABLE admins ADD COLUMN totp_secret text;

-- Team contests: members of a team act together (shared submissions);
-- max_team_size bounds the members of a team in the contest.
ALTER TABLE contests ADD COLUMN team_mode boolean NOT NULL DEFAULT false;
ALTER TABLE contests ADD COLUMN max_team_size integer CHECK (max_team_size > 0);
