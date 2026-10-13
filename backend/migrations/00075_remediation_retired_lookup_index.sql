-- +goose Up
-- 00073 shipped a partial index whose only key column was work_order_id. A
-- partial index filters on asset_retired_at, not work_order_id, so that index
-- could not be probed by the retirement/revive statements and only added write
-- overhead. The retirement and restore paths both reach tasks through the
-- finding's asset, so finding_id is the key column that matches the predicate
-- AND lets the planner return the retired row set directly.
DROP INDEX IF EXISTS remediation_tasks_asset_retired_idx;
CREATE INDEX IF NOT EXISTS remediation_tasks_finding_retired_idx
    ON remediation_tasks(finding_id) WHERE asset_retired_at IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS remediation_tasks_finding_retired_idx;
CREATE INDEX IF NOT EXISTS remediation_tasks_asset_retired_idx
    ON remediation_tasks(work_order_id) WHERE asset_retired_at IS NOT NULL;
