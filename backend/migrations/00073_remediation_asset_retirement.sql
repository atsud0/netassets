-- +goose Up
-- An asset soft delete retires the remediation work that hangs on it. The task
-- itself is NOT cancelled: cancelling would destroy the delivery history the
-- group relies on during an audit. Instead the task records when its asset was
-- retired, which makes it visibly un-current for the assignee, blocks further
-- progress, and gives the reviewer one honest reason instead of a generic
-- "authorization expired" message.
ALTER TABLE remediation_tasks
    ADD COLUMN IF NOT EXISTS asset_retired_at timestamptz;
CREATE INDEX IF NOT EXISTS remediation_tasks_asset_retired_idx
    ON remediation_tasks(work_order_id) WHERE asset_retired_at IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS remediation_tasks_asset_retired_idx;
ALTER TABLE remediation_tasks DROP COLUMN IF EXISTS asset_retired_at;
