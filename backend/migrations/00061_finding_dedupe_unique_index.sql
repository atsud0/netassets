-- +goose Up
-- Finding de-duplication was enforced only by a SELECT EXISTS check inside the
-- import transaction. Two concurrent LinkFindingImportCandidate calls for the
-- same asset and title lock different candidate rows, so they never serialise
-- before the check and both insert, leaving duplicate findings for one issue.
--
-- Collapse any duplicates that already exist before the constraint can be added.
-- The oldest finding is kept; later copies are removed only when nothing points
-- at them, and dangling work-order links are reassigned to the survivor first.
WITH ranked AS (
    SELECT id,
           first_value(id) OVER (
               PARTITION BY COALESCE(test_batch_id, '00000000-0000-0000-0000-000000000000'::uuid),
                            COALESCE(asset_id, '00000000-0000-0000-0000-000000000000'::uuid),
                            lower(btrim(title))
               ORDER BY created_at, id
           ) AS keep_id,
           row_number() OVER (
               PARTITION BY COALESCE(test_batch_id, '00000000-0000-0000-0000-000000000000'::uuid),
                            COALESCE(asset_id, '00000000-0000-0000-0000-000000000000'::uuid),
                            lower(btrim(title))
               ORDER BY created_at, id
           ) AS row_number
    FROM security_findings
), reassigned AS (
    UPDATE security_work_order_findings link
    SET finding_id = ranked.keep_id
    FROM ranked
    WHERE ranked.row_number > 1
      AND link.finding_id = ranked.id
      AND NOT EXISTS (
          SELECT 1 FROM security_work_order_findings other
          WHERE other.work_order_id = link.work_order_id
            AND other.finding_id = ranked.keep_id
      )
    RETURNING link.finding_id
), orphan_links AS (
    DELETE FROM security_work_order_findings link
    USING ranked
    WHERE ranked.row_number > 1
      AND link.finding_id = ranked.id
    RETURNING link.finding_id
)
DELETE FROM security_findings finding
USING ranked
WHERE ranked.row_number > 1
  AND finding.id = ranked.id;

-- One finding per (test batch, asset, normalised title). asset_id may be NULL for
-- business-system-only findings, which are keyed on the system instead, so the
-- index is partial on rows that actually carry an asset.
CREATE UNIQUE INDEX security_findings_batch_asset_title_unique
    ON security_findings(COALESCE(test_batch_id, '00000000-0000-0000-0000-000000000000'::uuid),
                         COALESCE(asset_id, '00000000-0000-0000-0000-000000000000'::uuid),
                         lower(btrim(title)))
    WHERE asset_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS security_findings_batch_asset_title_unique;
