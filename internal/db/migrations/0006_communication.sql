-- Communication (SPEC_CLOSE A1): questions about a task, public answers,
-- unread indicators and an anti-spam limit.

-- The contest of a question (denormalised from its participation so the
-- staff inbox and the public answers are one index scan).
ALTER TABLE questions ADD COLUMN contest_id bigint REFERENCES contests(id) ON DELETE CASCADE;
UPDATE questions q SET contest_id = p.contest_id FROM participations p WHERE p.id = q.participation_id;
ALTER TABLE questions ALTER COLUMN contest_id SET NOT NULL;
-- A question about a task (NULL: general).
ALTER TABLE questions ADD COLUMN task_id bigint REFERENCES tasks(id) ON DELETE SET NULL;
-- The answer is shown to every participant of the contest.
ALTER TABLE questions ADD COLUMN public boolean NOT NULL DEFAULT false;
DROP INDEX questions_unanswered_idx;
-- AWS inbox: pending questions of a contest, oldest first (replaces the
-- global index; the menu counter sums over contests with it too).
CREATE INDEX questions_pending_idx ON questions (contest_id, asked_at) WHERE reply_at IS NULL AND NOT ignored;
-- CWS: public answers of a contest, newest first.
CREATE INDEX questions_public_idx ON questions (contest_id, reply_at DESC) WHERE public AND reply_at IS NOT NULL;

-- When the participant last opened the communication page (unread count).
ALTER TABLE participations ADD COLUMN communication_seen_at timestamptz NOT NULL DEFAULT '1970-01-01 00:00:00+00';

-- Questions a participant may ask per minute (0: no limit).
ALTER TABLE contests ADD COLUMN questions_per_minute integer NOT NULL DEFAULT 3 CHECK (questions_per_minute >= 0);
