-- +goose Up
-- Promote older unmatched vulnerability imports into the same asset-discovery
-- workflow introduced after those imports were already stored.
WITH created AS (
    INSERT INTO asset_candidates
        (asset_type, asset_value, normalized_value, suggested_data, organization_id,
         source, source_reference, status, created_by)
    SELECT DISTINCT ON (c.asset_type, COALESCE(c.normalized_value, ''))
        c.asset_type,
        c.asset_value,
        COALESCE(c.normalized_value, ''),
        jsonb_build_object(
            'source', 'security_finding_import',
            'finding_import_id', c.finding_import_id::text,
            'finding_title', c.title,
            'severity', c.severity,
            'vulnerability_type', COALESCE(c.vulnerability_type, ''),
            'description', c.description,
            'evidence', COALESCE(c.evidence, ''),
            'discovered_at', c.discovered_at,
            'organization_pending', true
        ),
        NULL,
        'finding_import',
        left('漏洞导入：' || i.file_name, 255),
        'pending',
        i.created_by
    FROM security_finding_import_candidates c
    JOIN security_finding_imports i ON i.id=c.finding_import_id
    WHERE c.status='pending' AND c.test_batch_id IS NULL AND c.suggested_asset_id IS NULL AND c.asset_candidate_id IS NULL
    ORDER BY c.asset_type, COALESCE(c.normalized_value, ''), c.created_at
    RETURNING id, asset_type, normalized_value
)
UPDATE security_finding_import_candidates c
SET asset_candidate_id=created.id
FROM created
WHERE c.status='pending'
  AND c.test_batch_id IS NULL
  AND c.suggested_asset_id IS NULL
  AND c.asset_candidate_id IS NULL
  AND c.asset_type=created.asset_type
  AND COALESCE(c.normalized_value, '') = created.normalized_value;

-- +goose Down
-- The migration intentionally does not remove asset discoveries because they
-- may have been reviewed by an administrator after the backfill.
