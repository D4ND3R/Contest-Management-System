-- Dispatcher bookkeeping: when jobs were last enqueued for a result. The
-- sweeper re-enqueues work for results whose jobs are older than a grace
-- period (lost queue messages, dispatcher crash between commit and enqueue)
-- and picks up invalidated results (NULL) immediately.
ALTER TABLE submission_results ADD COLUMN jobs_enqueued_at timestamptz;
ALTER TABLE user_test_results ADD COLUMN jobs_enqueued_at timestamptz;

-- Replace the pending index so the sweeper's filter is fully covered.
DROP INDEX submission_results_pending_idx;
-- Dispatcher sweeper: results in flight, oldest enqueue first.
CREATE INDEX submission_results_pending_idx ON submission_results (jobs_enqueued_at NULLS FIRST, submission_id)
    WHERE scored_at IS NULL AND system_error IS NULL;
DROP INDEX user_test_results_pending_idx;
-- Dispatcher sweeper: user tests in flight.
CREATE INDEX user_test_results_pending_idx ON user_test_results (jobs_enqueued_at NULLS FIRST, user_test_id)
    WHERE completed_at IS NULL AND system_error IS NULL;
