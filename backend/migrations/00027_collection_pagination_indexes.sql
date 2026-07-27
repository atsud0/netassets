-- +goose Up
-- Supports stable keyset ordering for submission resources while LIMIT/OFFSET
-- pages are requested, and avoids an extra sort for quality issue timelines.
CREATE INDEX inventory_submission_items_page_idx
    ON inventory_submission_items(submission_id, resource_type, resource_key, resource_id);

CREATE INDEX data_quality_issue_events_page_idx
    ON data_quality_issue_events(issue_id, created_at DESC, id DESC);

-- +goose Down
DROP INDEX IF EXISTS data_quality_issue_events_page_idx;
DROP INDEX IF EXISTS inventory_submission_items_page_idx;
