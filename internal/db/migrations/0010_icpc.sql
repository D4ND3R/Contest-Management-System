-- SPEC_CLOSE B7: ICPC verdicts and balloons.

-- Binary verdict of a scored submission (AC, WA, TLE, MLE, RE, OLE, CE),
-- set together with the score; contestants of ICPC contests see it instead
-- of the score.
ALTER TABLE submission_results ADD COLUMN verdict text;

-- Balloons handed to a team (or contestant) for a solved task. The list of
-- balloons to deliver is computed from participation_task_scores; this
-- table only records deliveries. recipient is the ranking key of the team
-- or participation.
CREATE TABLE balloons (
    task_id      bigint NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    recipient    text NOT NULL,
    delivered_at timestamptz NOT NULL DEFAULT now(),
    delivered_by bigint REFERENCES admins(id) ON DELETE SET NULL,
    PRIMARY KEY (task_id, recipient)
);
