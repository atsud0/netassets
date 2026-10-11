-- +goose Up
-- Collapse duplicates created before this constraint was introduced. Keep the
-- oldest discovery and move all finding-import rows to it before deleting the
-- redundant rows, otherwise ON DELETE SET NULL would strand those findings.
WITH ranked AS (
    SELECT id,
           first_value(id) OVER (PARTITION BY asset_type, normalized_value ORDER BY created_at, id) AS keep_id,
           row_number() OVER (PARTITION BY asset_type, normalized_value ORDER BY created_at, id) AS row_number
    FROM asset_candidates
    WHERE status = 'pending' AND normalized_value <> ''
), reassigned AS (
    UPDATE security_finding_import_candidates fic
    SET asset_candidate_id = ranked.keep_id
    FROM ranked
    WHERE ranked.row_number > 1 AND fic.asset_candidate_id = ranked.id
    RETURNING fic.id
)
DELETE FROM asset_candidates candidate
USING ranked
WHERE ranked.row_number > 1 AND candidate.id = ranked.id;

-- One open discovery represents one canonical asset value. This is enforced in
-- the database because imports can be processed concurrently.
CREATE UNIQUE INDEX asset_candidates_pending_value_unique
    ON asset_candidates(asset_type, normalized_value)
    WHERE status = 'pending' AND normalized_value <> '';

ALTER TABLE assets DROP CONSTRAINT IF EXISTS assets_source_check;
ALTER TABLE assets ADD CONSTRAINT assets_source_check
    CHECK (source IN ('manual', 'import', 'api', 'plugin', 'finding_import'));

ALTER TABLE inventory_asset_drafts DROP CONSTRAINT IF EXISTS inventory_asset_drafts_source_check;
ALTER TABLE inventory_asset_drafts ADD CONSTRAINT inventory_asset_drafts_source_check
    CHECK (source IN ('manual', 'import', 'api', 'plugin', 'finding_import'));

-- +goose Down
ALTER TABLE inventory_asset_drafts DROP CONSTRAINT IF EXISTS inventory_asset_drafts_source_check;
ALTER TABLE inventory_asset_drafts ADD CONSTRAINT inventory_asset_drafts_source_check
    CHECK (source IN ('manual', 'import', 'api', 'plugin'));
ALTER TABLE assets DROP CONSTRAINT IF EXISTS assets_source_check;
ALTER TABLE assets ADD CONSTRAINT assets_source_check
    CHECK (source IN ('manual', 'import', 'api', 'plugin'));
DROP INDEX IF EXISTS asset_candidates_pending_value_unique;
