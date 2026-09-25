-- SPEC_CLOSE B8: printing.

-- Pages a contestant may print in all (null: no limit besides the number
-- of jobs times the pages per job).
ALTER TABLE contests ADD COLUMN max_print_total_pages integer CHECK (max_print_total_pages > 0);

-- The printing service stamps when it handed the job to the printer; the
-- staff marks when they gave the pages to the contestant.
ALTER TABLE print_jobs ADD COLUMN printed_at timestamptz;
ALTER TABLE print_jobs ADD COLUMN delivered_at timestamptz;
ALTER TABLE print_jobs ADD COLUMN delivered_by bigint REFERENCES admins(id) ON DELETE SET NULL;
