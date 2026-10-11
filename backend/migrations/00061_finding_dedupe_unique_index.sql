-- +goose Up
-- Finding de-duplication was enforced only by a SELECT EXISTS check inside the
-- import transaction. Two concurrent LinkFindingImportCandidate calls for the
-- same asset and title lock different candidate rows, so they never serialise
-- before the check and both insert, leaving duplicate findings for one issue.
--
-- Collapse duplicates that already exist before the constraint can be added.
-- security_findings is referenced by three tables:
--   security_work_order_findings.finding_id      ON DELETE RESTRICT, PK (work_order_id, finding_id)
--   remediation_tasks.finding_id                 ON DELETE RESTRICT, partial unique (work_order_id, finding_id) WHERE status <> 'cancelled'
--   security_finding_import_candidates.imported_finding_id  ON DELETE SET NULL
-- Each reference must be moved onto the surviving finding, or dropped when the
-- target already carries it. The statements below run separately on purpose:
-- data-modifying CTEs share one snapshot and cannot observe each other, so the
-- drops must be committed as their own statements before the repoints run.
CREATE TEMP TABLE finding_dedupe_map ON COMMIT DROP AS
SELECT id AS duplicate_id, keep_id
FROM (
    SELECT id,
           first_value(id) OVER w AS keep_id,
           row_number() OVER w AS position
    FROM security_findings
    WINDOW w AS (
        PARTITION BY COALESCE(test_batch_id, '00000000-0000-0000-0000-000000000000'::uuid),
                     COALESCE(asset_id, '00000000-0000-0000-0000-000000000000'::uuid),
                     lower(btrim(title))
        ORDER BY created_at, id
    )
) ranked
WHERE position > 1;

-- Several duplicates can be linked to the same work order, so every link is
-- first projected onto the finding it will end up pointing at and all but the
-- earliest row per (work order, surviving finding) are dropped. The primary key
-- on (work_order_id, finding_id) would otherwise reject the repoint below.
WITH projected_links AS (
    SELECT link.ctid AS link_id,
           row_number() OVER (
               PARTITION BY link.work_order_id, COALESCE(map.keep_id, link.finding_id)
               ORDER BY link.created_at, link.finding_id
           ) AS position
    FROM security_work_order_findings link
    LEFT JOIN finding_dedupe_map map ON map.duplicate_id = link.finding_id
)
DELETE FROM security_work_order_findings link
USING projected_links projected
WHERE link.ctid = projected.link_id AND projected.position > 1;

UPDATE security_work_order_findings link
SET finding_id = map.keep_id
FROM finding_dedupe_map map
WHERE link.finding_id = map.duplicate_id;

-- One remediation task per (work order, finding) is enforced by a partial unique
-- index that only covers tasks with status <> 'cancelled'. Every task pointing
-- at a duplicate must move onto the survivor, but when a work order would end up
-- with two live tasks for the same finding, all but one of them are cancelled
-- first. Cancelling keeps the audit trail instead of dropping rows, and cancelled
-- tasks are excluded from the partial index so the repoint cannot collide.
WITH projected AS (
    SELECT task.id AS task_id,
           row_number() OVER (
               PARTITION BY task.work_order_id, map.keep_id
               ORDER BY CASE WHEN task.finding_id = map.keep_id THEN 0 ELSE 1 END,
                        task.created_at, task.id
           ) AS position
    FROM remediation_tasks task
    JOIN finding_dedupe_map map
      ON task.finding_id = map.duplicate_id OR task.finding_id = map.keep_id
    WHERE task.status <> 'cancelled'
)
UPDATE remediation_tasks task
SET status = 'cancelled'
FROM projected
WHERE task.id = projected.task_id AND projected.position > 1;

-- Every task, including cancelled ones, still holds a foreign key to the
-- duplicate finding, so all of them must move. The partial unique index above
-- only covers tasks with status <> 'cancelled', which is why conflicting live
-- tasks were cancelled rather than merged away.
UPDATE remediation_tasks task
SET finding_id = map.keep_id
FROM finding_dedupe_map map
WHERE task.finding_id = map.duplicate_id;

UPDATE security_finding_import_candidates candidate
SET imported_finding_id = NULL
FROM finding_dedupe_map map
WHERE candidate.imported_finding_id = map.duplicate_id;

DELETE FROM security_findings finding
USING finding_dedupe_map map
WHERE finding.id = map.duplicate_id;

DROP TABLE finding_dedupe_map;

-- One finding per (test batch, asset, normalised title). Findings without an
-- asset are keyed on a business system instead and are not covered here.
CREATE UNIQUE INDEX security_findings_batch_asset_title_unique
    ON security_findings(COALESCE(test_batch_id, '00000000-0000-0000-0000-000000000000'::uuid),
                         asset_id,
                         lower(btrim(title)))
    WHERE asset_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS security_findings_batch_asset_title_unique;
