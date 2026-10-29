-- +goose Up
-- AssignIssue records an explicit audit event without changing the issue status.
-- The workflow was added after the original event constraint, which omitted it.
ALTER TABLE data_quality_issue_events
    DROP CONSTRAINT data_quality_issue_events_event_type_check;
ALTER TABLE data_quality_issue_events
    ADD CONSTRAINT data_quality_issue_events_event_type_check
    CHECK (event_type IN ('detected', 'assigned', 'started', 'submitted', 'approved', 'returned', 'exempted', 'reopened', 'auto_resolved'));

-- +goose Down
-- If assignment events exist, restoring the old constraint intentionally fails.
-- Do not rewrite or discard audit history to make a rollback succeed.
ALTER TABLE data_quality_issue_events
    DROP CONSTRAINT data_quality_issue_events_event_type_check;
ALTER TABLE data_quality_issue_events
    ADD CONSTRAINT data_quality_issue_events_event_type_check
    CHECK (event_type IN ('detected', 'started', 'submitted', 'approved', 'returned', 'exempted', 'reopened', 'auto_resolved'));
