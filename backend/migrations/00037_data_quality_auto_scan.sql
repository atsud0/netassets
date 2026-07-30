-- +goose Up
ALTER TABLE data_quality_scan_batches
    ADD COLUMN trigger_type varchar(16) NOT NULL DEFAULT 'manual'
        CHECK (trigger_type IN ('manual', 'scheduled')),
    ADD COLUMN scheduled_for date,
    ADD CONSTRAINT data_quality_scan_batches_schedule_check CHECK (
        (trigger_type = 'manual' AND scheduled_for IS NULL)
        OR (trigger_type = 'scheduled' AND scheduled_for IS NOT NULL)
    );

CREATE UNIQUE INDEX data_quality_scan_batches_scheduled_for_unique_idx
    ON data_quality_scan_batches(scheduled_for)
    WHERE trigger_type = 'scheduled';

-- +goose Down
DROP INDEX IF EXISTS data_quality_scan_batches_scheduled_for_unique_idx;
ALTER TABLE data_quality_scan_batches
    DROP CONSTRAINT IF EXISTS data_quality_scan_batches_schedule_check,
    DROP COLUMN IF EXISTS scheduled_for,
    DROP COLUMN IF EXISTS trigger_type;
