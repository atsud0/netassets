-- +goose Up
-- Finding de-duplication was enforced only by a SELECT EXISTS check inside the
-- import transaction. Two concurrent LinkFindingImportCandidate calls for the
-- same asset and title lock different candidate rows, so they never serialise
-- before the check and both insert, leaving duplicate findings for one issue.
--
-- Collapse duplicates that already exist before the constraint can be added.
-- security_findings is referenced by three tables, all with ON DELETE RESTRICT
-- or SET NULL, so every reference must be repointed or cleared before the extra
-- rows can be removed:
--   security_work_order_findings.finding_id      ON DELETE RESTRICT
--   remediation_tasks.finding_id                 ON DELETE RESTRICT
--   security_finding_import_candidates.imported_finding_id  ON DELETE SET NULL
-- A reference is repointed to the surviving finding; if that would duplicate a
-- link that already exists it is removed instead, so neither the repoint nor a
-- uniqueness constraint is violated.
WITH ranked AS (
    SELECT id,
           first_value(id) OVER w AS keep_id,
           row_number() OVER w AS row_number
    FROM security_findings
    WINDOW w AS (
        PARTITION BY COALESCE(test_batch_id, '00000000-0000-0000-0000-000000000000'::uuid),
                     COALESCE(asset_id, '00000000-0000-0000-0000-000000000000'::uuid),
                     lower(btrim(title))
        ORDER BY created_at, id
    )
), repointed_order_links AS (
    UPDATE security_work_order_findings link
    SET finding_id = ranked.keep_id
    FROM ranked
    WHERE ranked.row_number > 1
      AND link.finding_id = ranked.id
    RETURNING link.work_order_id, link.finding_id
), dropped_order_links AS (
    DELETE FROM security_work_order_findings link
    USING ranked
    WHERE ranked.row_number > 1
      AND link.finding_id = ranked.id
    RETURNING link.work_order_id, link.finding_id
), repointed_tasks AS (
    UPDATE remediation_tasks task
    SET finding_id = ranked.keep_id
    FROM ranked
    WHERE ranked.row_number > 1
      AND task.finding_id = ranked.id
    RETURNING task.task_no
), cleared_candidates AS (
    UPDATE security_finding_import_candidates candidate
    SET imported_finding_id = NULL
    FROM ranked
    WHERE ranked.row_number > 1
      AND candidate.imported_finding_id = ranked.id
    RETURNING candidate.finding_import_id, candidate.source_row_number
)
DELETE FROM security_findings finding
USING ranked
WHERE ranked.row_number > 1
  AND finding.id = ranked.id;

-- One finding per (test batch, asset, normalised title). Findings without an
-- asset are keyed on a business system instead and are not covered here.
CREATE UNIQUE INDEX security_findings_batch_asset_title_unique
    ON security_findings(COALESCE(test_batch_id, '00000000-0000-0000-0000-000000000000'::uuid),
                         asset_id,
                         lower(btrim(title)))
    WHERE asset_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS security_findings_batch_asset_title_unique;
