-- SPEC_CLOSE D3: the system panel lists the results that could not be
-- judged on every visit; they are rare, so a partial index keeps that
-- query from scanning every result.
CREATE INDEX submission_results_system_error_idx ON submission_results (submission_id DESC) WHERE system_error IS NOT NULL;
