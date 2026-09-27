-- Ranking tie-break and photos (SPEC_IOI §10), the "last" score mode (§6)
-- and hidden checker messages (§5).

-- How equal rankings are ordered: 'shared' (the IOI rule: equal totals
-- share a place) or 'time' (who reached the total first ranks higher; in
-- ICPC mode, the earliest last accepted submission).
ALTER TABLE contests ADD COLUMN ranking_tie_break text NOT NULL DEFAULT 'shared'
    CHECK (ranking_tie_break IN ('shared', 'time'));

-- When the task score last changed to its current value (the submission
-- time; NULL while the score is not positive). Maintained by the
-- dispatcher with the rest of the row; read with it, so no index.
ALTER TABLE participation_task_scores ADD COLUMN score_reached_at timestamptz;

-- Score mode "last" (SPEC_IOI §6): the last submission that compiled
-- counts, whether better or worse than the earlier ones.
ALTER TABLE tasks DROP CONSTRAINT tasks_score_mode_check,
    ADD CONSTRAINT tasks_score_mode_check CHECK (score_mode IN ('max', 'max_subtask', 'max_tokened_last', 'last'));
ALTER TABLE contests DROP CONSTRAINT contests_default_score_mode_check,
    ADD CONSTRAINT contests_default_score_mode_check
    CHECK (default_score_mode IN ('max', 'max_subtask', 'max_tokened_last', 'last'));

-- Participants' photos on the public scoreboards (opt-in: they are often
-- minors).
ALTER TABLE contests ADD COLUMN ranking_show_photos boolean NOT NULL DEFAULT false;

-- A task can hide its checker's own messages from the contestants, who
-- then see the standard message of each outcome (staff still see them).
ALTER TABLE tasks ADD COLUMN hide_checker_messages boolean NOT NULL DEFAULT false;
