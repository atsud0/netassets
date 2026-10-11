-- +goose Up
-- A finding import can discover an asset before its ownership is known. Keep the
-- candidate unassigned until an administrator confirms the owning organization.
ALTER TABLE asset_candidates
    ALTER COLUMN organization_id DROP NOT NULL;

ALTER TABLE security_finding_import_candidates
    ADD COLUMN asset_candidate_id uuid REFERENCES asset_candidates(id) ON DELETE SET NULL;

CREATE INDEX security_finding_import_candidates_asset_candidate_idx
    ON security_finding_import_candidates(asset_candidate_id)
    WHERE asset_candidate_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS security_finding_import_candidates_asset_candidate_idx;
ALTER TABLE security_finding_import_candidates
    DROP COLUMN IF EXISTS asset_candidate_id;
ALTER TABLE asset_candidates
    ALTER COLUMN organization_id SET NOT NULL;
