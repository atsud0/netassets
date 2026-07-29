-- +goose Up
ALTER TABLE security_finding_imports
    ADD COLUMN pending_count integer NOT NULL DEFAULT 0;

CREATE TABLE security_finding_import_candidates (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    finding_import_id uuid NOT NULL REFERENCES security_finding_imports(id) ON DELETE CASCADE,
    test_batch_id uuid NOT NULL REFERENCES security_test_batches(id) ON DELETE CASCADE,
    source_row_number integer NOT NULL CHECK (source_row_number > 1),
    raw_data jsonb NOT NULL DEFAULT '{}'::jsonb,
    asset_type varchar(24) NOT NULL CHECK (asset_type IN ('domain', 'ip', 'url', 'service', 'other')),
    asset_value text NOT NULL,
    normalized_value text,
    title varchar(255) NOT NULL,
    severity varchar(16) NOT NULL CHECK (severity IN ('critical', 'high', 'medium', 'low', 'info')),
    vulnerability_type varchar(128),
    description text NOT NULL,
    evidence text,
    remediation_advice text,
    discovered_at timestamptz NOT NULL,
    status varchar(16) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'imported', 'ignored', 'invalid', 'duplicate')),
    suggested_scope_asset_id uuid REFERENCES security_test_scope_assets(id) ON DELETE SET NULL,
    matched_scope_asset_id uuid REFERENCES security_test_scope_assets(id) ON DELETE SET NULL,
    match_method varchar(32),
    match_confidence integer CHECK (match_confidence IS NULL OR (match_confidence >= 0 AND match_confidence <= 100)),
    review_note text,
    reviewed_by uuid REFERENCES users(id) ON DELETE SET NULL,
    reviewed_at timestamptz,
    imported_finding_id uuid REFERENCES security_findings(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (finding_import_id, source_row_number)
);

CREATE INDEX security_finding_import_candidates_status_created_idx
    ON security_finding_import_candidates(status, created_at DESC);
CREATE INDEX security_finding_import_candidates_batch_status_idx
    ON security_finding_import_candidates(test_batch_id, status, created_at DESC);
CREATE INDEX security_finding_import_candidates_import_idx
    ON security_finding_import_candidates(finding_import_id, source_row_number);

COMMENT ON TABLE security_finding_import_candidates IS
    'Unmatched security finding import rows awaiting manual association to an asset within the authorized test batch scope.';

-- +goose Down
DROP TABLE IF EXISTS security_finding_import_candidates;
ALTER TABLE security_finding_imports DROP COLUMN IF EXISTS pending_count;
