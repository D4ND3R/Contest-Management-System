-- Unofficial participants and medal cutoffs (SPEC_IOI §9.2, §10).

-- An unofficial participant (a guest, a host-country extra) is judged and
-- shown like everybody else but takes no place and no medal.
ALTER TABLE participations ADD COLUMN unofficial boolean NOT NULL DEFAULT false;

-- IOI medal cutoffs: none, computed for the administrators only, or also
-- shown on the public scoreboards.
ALTER TABLE contests ADD COLUMN medals text NOT NULL DEFAULT 'none'
    CHECK (medals IN ('none', 'admins', 'public'));

-- Clarification desk (SPEC_IOI §9.3): a staff member takes a question so
-- that two do not answer it at once. No index: the inbox lists pending
-- questions through questions_unanswered_idx and filters these few rows.
ALTER TABLE questions ADD COLUMN assigned_admin_id bigint REFERENCES admins(id) ON DELETE SET NULL;

-- Appeals (SPEC_IOI §14): once their contest is over, and until
-- appeals_until, contestants contest the evaluation of a task (optionally
-- one submission); the staff accept or reject each appeal with an answer.
ALTER TABLE contests ADD COLUMN appeals_until timestamptz;
CREATE TABLE appeals (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    participation_id bigint NOT NULL REFERENCES participations(id) ON DELETE CASCADE,
    task_id          bigint REFERENCES tasks(id) ON DELETE SET NULL,
    submission_id    bigint REFERENCES submissions(id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    text             text NOT NULL,
    status           text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'accepted', 'rejected')),
    response         text NOT NULL DEFAULT '',
    handled_by       bigint REFERENCES admins(id) ON DELETE SET NULL,
    handled_at       timestamptz
);
-- CWS: a contestant's appeals; AWS: a contest's, through its participations.
CREATE INDEX appeals_participation_idx ON appeals (participation_id, created_at);
