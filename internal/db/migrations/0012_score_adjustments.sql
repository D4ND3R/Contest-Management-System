-- SPEC_CLOSE D2: manual score adjustments.

-- Points added to (or, when negative, removed from) a contestant's task
-- score by an administrator, with the reason. Append-only: a correction
-- is another adjustment.
CREATE TABLE score_adjustments (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    participation_id bigint NOT NULL REFERENCES participations(id) ON DELETE CASCADE,
    task_id          bigint NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    points           double precision NOT NULL CHECK (points <> 0),
    reason           text NOT NULL CHECK (length(btrim(reason)) >= 5),
    admin_id         bigint REFERENCES admins(id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now()
);
-- Participation page: its adjustments (and the contestant's overview).
CREATE INDEX score_adjustments_participation_idx ON score_adjustments (participation_id, task_id);
-- Ranking replay: a contest's adjustments go through the participations
-- index; tasks are deleted with their adjustments.
CREATE INDEX score_adjustments_task_idx ON score_adjustments (task_id);

-- The sum of a task score's adjustments; the aggregation always adds it to
-- the score it computes from the submissions.
ALTER TABLE participation_task_scores ADD COLUMN adjustment double precision NOT NULL DEFAULT 0;
