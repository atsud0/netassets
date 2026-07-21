-- +goose Up
ALTER TABLE inventory_task_organizations
    ADD COLUMN started_at timestamptz,
    ADD COLUMN submission_note text,
    ADD COLUMN submitted_system_count bigint NOT NULL DEFAULT 0 CHECK (submitted_system_count >= 0),
    ADD COLUMN submitted_asset_count bigint NOT NULL DEFAULT 0 CHECK (submitted_asset_count >= 0);

-- +goose Down
ALTER TABLE inventory_task_organizations
    DROP COLUMN IF EXISTS submitted_asset_count,
    DROP COLUMN IF EXISTS submitted_system_count,
    DROP COLUMN IF EXISTS submission_note,
    DROP COLUMN IF EXISTS started_at;
