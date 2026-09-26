-- Suspicious submissions (SPEC_IOI H3): what a scan of the source noticed
-- when it arrived (process creation, sockets, raw system calls, system
-- files...) or what the sandbox saw (a forbidden system call). A flag never
-- changes a score: it tells the staff where to look.
CREATE TABLE submission_flags (
    submission_id bigint NOT NULL REFERENCES submissions(id) ON DELETE CASCADE,
    kind          text NOT NULL CHECK (kind IN ('source', 'runtime')),
    reason        text NOT NULL,
    -- Where it was seen (file:line and the line, or the testcase).
    detail        text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    -- A reason is recorded once per submission (re-evaluations repeat it).
    PRIMARY KEY (submission_id, kind, reason)
);
