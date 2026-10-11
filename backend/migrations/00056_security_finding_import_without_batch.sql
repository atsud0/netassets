-- +goose Up
-- A test batch is useful context for an import, but it is not required for
-- externally confirmed findings whose asset has not been onboarded yet.
ALTER TABLE security_finding_imports
    ALTER COLUMN test_batch_id DROP NOT NULL;
ALTER TABLE security_finding_imports
    DROP CONSTRAINT IF EXISTS security_finding_imports_test_batch_id_fkey;
ALTER TABLE security_finding_imports
    ADD CONSTRAINT security_finding_imports_test_batch_id_fkey
    FOREIGN KEY (test_batch_id) REFERENCES security_test_batches(id) ON DELETE SET NULL;

ALTER TABLE security_finding_import_candidates
    ALTER COLUMN test_batch_id DROP NOT NULL;
ALTER TABLE security_finding_import_candidates
    DROP CONSTRAINT IF EXISTS security_finding_import_candidates_test_batch_id_fkey;
ALTER TABLE security_finding_import_candidates
    ADD CONSTRAINT security_finding_import_candidates_test_batch_id_fkey
    FOREIGN KEY (test_batch_id) REFERENCES security_test_batches(id) ON DELETE SET NULL;

ALTER TABLE security_finding_import_candidates
    ADD COLUMN matched_asset_id uuid REFERENCES assets(id) ON DELETE SET NULL;
CREATE INDEX security_finding_import_candidates_asset_idx
    ON security_finding_import_candidates(matched_asset_id)
    WHERE matched_asset_id IS NOT NULL;

COMMENT ON COLUMN security_finding_imports.test_batch_id IS
    'Optional source test batch. Null means the finding was imported independently of a test batch.';
COMMENT ON COLUMN security_finding_import_candidates.test_batch_id IS
    'Optional source test batch. Null means the candidate can be associated against the global asset inventory.';

-- +goose Down
DROP INDEX IF EXISTS security_finding_import_candidates_asset_idx;
ALTER TABLE security_finding_import_candidates DROP COLUMN IF EXISTS matched_asset_id;
ALTER TABLE security_finding_import_candidates
    DROP CONSTRAINT IF EXISTS security_finding_import_candidates_test_batch_id_fkey;
ALTER TABLE security_finding_import_candidates
    ADD CONSTRAINT security_finding_import_candidates_test_batch_id_fkey
    FOREIGN KEY (test_batch_id) REFERENCES security_test_batches(id) ON DELETE CASCADE;
ALTER TABLE security_finding_import_candidates
    ALTER COLUMN test_batch_id SET NOT NULL;
ALTER TABLE security_finding_imports
    DROP CONSTRAINT IF EXISTS security_finding_imports_test_batch_id_fkey;
ALTER TABLE security_finding_imports
    ADD CONSTRAINT security_finding_imports_test_batch_id_fkey
    FOREIGN KEY (test_batch_id) REFERENCES security_test_batches(id) ON DELETE CASCADE;
ALTER TABLE security_finding_imports
    ALTER COLUMN test_batch_id SET NOT NULL;
