-- Ranking configuration per contest (SPEC_CLOSE A2).
ALTER TABLE contests
    -- Who sees the ranking: public (ranking web server and contestants),
    -- contestants (contest web server only), admins (admin panel, and the
    -- ranking web server behind a secret link, e.g. for a projector) or
    -- hidden (the admin panel only).
    ADD COLUMN ranking_visibility text NOT NULL DEFAULT 'public'
        CHECK (ranking_visibility IN ('public', 'contestants', 'admins', 'hidden')),
    -- What contestants see in the contest web server: the full ranking,
    -- only their own position, or nothing.
    ADD COLUMN ranking_contestant_view text NOT NULL DEFAULT 'full'
        CHECK (ranking_contestant_view IN ('full', 'own', 'none')),
    -- When it is shown: always (during and after) or only after the end.
    ADD COLUMN ranking_when text NOT NULL DEFAULT 'always' CHECK (ranking_when IN ('always', 'after')),
    -- Freeze the last minutes of the contest (0: no freeze); the explicit
    -- ranking_freeze_time is used when this is 0.
    ADD COLUMN ranking_freeze_minutes integer NOT NULL DEFAULT 0 CHECK (ranking_freeze_minutes >= 0),
    ADD COLUMN ranking_show_subtasks boolean NOT NULL DEFAULT true,
    ADD COLUMN ranking_show_flags boolean NOT NULL DEFAULT true,
    ADD COLUMN ranking_show_institutions boolean NOT NULL DEFAULT true,
    ADD COLUMN ranking_show_hidden boolean NOT NULL DEFAULT false,
    -- Names replaced by anonymous labels.
    ADD COLUMN ranking_anonymous boolean NOT NULL DEFAULT false;
