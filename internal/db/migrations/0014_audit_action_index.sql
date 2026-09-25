-- SPEC_CLOSE D6: the audit log filters by action prefix ("contest." or
-- "score.adjust"), newest first; text_pattern_ops lets LIKE 'prefix%' use
-- the index whatever the collation.
CREATE INDEX audit_log_action_idx ON audit_log (action text_pattern_ops, id DESC);
