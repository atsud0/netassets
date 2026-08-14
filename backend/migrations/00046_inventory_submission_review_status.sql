-- +goose Up
ALTER TABLE inventory_submissions
    ADD COLUMN review_status varchar(16) NOT NULL DEFAULT 'submitted'
        CHECK (review_status IN ('submitted', 'approved', 'returned')),
    ADD COLUMN reviewed_at timestamptz,
    ADD COLUMN reviewed_by uuid REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN return_reason text;

-- A newer version for the same task can only be submitted after the previous
-- version was returned, so this backfill is deterministic.
UPDATE inventory_submissions older
SET review_status = 'returned'
WHERE EXISTS (
    SELECT 1
    FROM inventory_submissions newer
    WHERE newer.task_id = older.task_id
      AND newer.organization_id = older.organization_id
      AND newer.submission_version > older.submission_version
);

-- The latest version mirrors the persisted organization progress. Historical
-- completed tasks are therefore restored as approved without inspecting audit
-- log text.
UPDATE inventory_submissions submission
SET review_status = CASE progress.status
        WHEN 'approved' THEN 'approved'
        WHEN 'returned' THEN 'returned'
        ELSE 'submitted'
    END,
    reviewed_at = CASE WHEN progress.status IN ('approved', 'returned') THEN progress.reviewed_at ELSE NULL END,
    reviewed_by = CASE WHEN progress.status IN ('approved', 'returned') THEN progress.reviewed_by ELSE NULL END,
    return_reason = CASE WHEN progress.status = 'returned' THEN progress.return_reason ELSE NULL END
FROM inventory_task_organizations progress
WHERE progress.latest_submission_id = submission.id;

CREATE INDEX inventory_submissions_resource_history_idx
    ON inventory_submission_items(resource_type, resource_id, submission_id);
CREATE INDEX inventory_submission_items_source_resource_idx
    ON inventory_submission_items(resource_type, (snapshot_data->>'_source_resource_id'), submission_id)
    WHERE snapshot_data ? '_source_resource_id';

-- +goose Down
DROP INDEX IF EXISTS inventory_submission_items_source_resource_idx;
DROP INDEX IF EXISTS inventory_submissions_resource_history_idx;
ALTER TABLE inventory_submissions
    DROP COLUMN IF EXISTS return_reason,
    DROP COLUMN IF EXISTS reviewed_by,
    DROP COLUMN IF EXISTS reviewed_at,
    DROP COLUMN IF EXISTS review_status;
