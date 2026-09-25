-- Task tester (SPEC_AUDIT §2): administrators judge reference solutions of a
-- task on every dataset without a participation. Such submissions have no
-- participation, so every participation-based view (contestant pages,
-- rankings, statistics, exports) ignores them, and scoring never
-- aggregates them.
ALTER TABLE submissions ALTER COLUMN participation_id DROP NOT NULL;
ALTER TABLE submissions ADD COLUMN tester boolean NOT NULL DEFAULT false;
ALTER TABLE submissions ADD COLUMN tester_admin_id bigint REFERENCES admins(id) ON DELETE SET NULL;
ALTER TABLE submissions ADD CONSTRAINT submissions_owner_check CHECK ((participation_id IS NULL) = tester);
-- AWS task tester: the runs of a task, newest first (small, partial).
CREATE INDEX submissions_tester_idx ON submissions (task_id, id DESC) WHERE tester;
