-- SPEC_CLOSE block B: contest settings.

-- draft: invisible to contestants; published: normal; archived: read-only
-- and not listed.
ALTER TABLE contests ADD COLUMN status text NOT NULL DEFAULT 'published'
    CHECK (status IN ('draft', 'published', 'archived'));
-- Unofficial submissions after the contest (and after any analysis window).
ALTER TABLE contests ADD COLUMN practice_enabled boolean NOT NULL DEFAULT false;
-- Score mode given to tasks created in the contest.
ALTER TABLE contests ADD COLUMN default_score_mode text NOT NULL DEFAULT 'max_subtask'
    CHECK (default_score_mode IN ('max', 'max_subtask', 'max_tokened_last'));
-- When contestants see scores: always, only after the end, never.
ALTER TABLE contests ADD COLUMN score_visibility text NOT NULL DEFAULT 'always'
    CHECK (score_visibility IN ('always', 'after', 'never'));
ALTER TABLE contests ADD COLUMN show_compilation_output boolean NOT NULL DEFAULT true;
-- Largest submission file (null: the server's contest_web.max_submission_bytes).
ALTER TABLE contests ADD COLUMN max_submission_bytes bigint CHECK (max_submission_bytes > 0);
-- Who creates participations: admins only, contestants with approval, or
-- contestants holding the invitation code.
ALTER TABLE contests ADD COLUMN registration text NOT NULL DEFAULT 'admin'
    CHECK (registration IN ('admin', 'approval', 'code'));
ALTER TABLE contests ADD COLUMN invitation_code text NOT NULL DEFAULT '';
ALTER TABLE contests ADD COLUMN password_min_length integer NOT NULL DEFAULT 8 CHECK (password_min_length BETWEEN 4 AND 128);
-- Contestant sessions last this long (null: 24 hours).
ALTER TABLE contests ADD COLUMN session_minutes integer CHECK (session_minutes > 0);

-- Self-registered participations wait for an administrator.
ALTER TABLE participations ADD COLUMN approved boolean NOT NULL DEFAULT true;
