-- +goose Up
ALTER TABLE security_finding_import_candidates
    ADD COLUMN suggested_asset_id uuid REFERENCES assets(id) ON DELETE SET NULL;
CREATE INDEX security_finding_import_candidates_suggested_asset_idx
    ON security_finding_import_candidates(suggested_asset_id)
    WHERE suggested_asset_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS security_finding_import_candidates_suggested_asset_idx;
ALTER TABLE security_finding_import_candidates DROP COLUMN IF EXISTS suggested_asset_id;
