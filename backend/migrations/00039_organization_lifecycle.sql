-- +goose Up
ALTER TABLE organizations
    ADD COLUMN deleted_at timestamptz,
    ADD COLUMN deleted_by uuid REFERENCES users(id) ON DELETE SET NULL;

CREATE INDEX organizations_active_parent_idx
    ON organizations(parent_id, organization_code)
    WHERE deleted_at IS NULL;

CREATE INDEX organizations_deleted_idx
    ON organizations(deleted_at DESC)
    WHERE deleted_at IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS organizations_deleted_idx;
DROP INDEX IF EXISTS organizations_active_parent_idx;
ALTER TABLE organizations DROP COLUMN IF EXISTS deleted_by;
ALTER TABLE organizations DROP COLUMN IF EXISTS deleted_at;
