-- +goose Up
-- Asset candidates discovered by a finding import used to be accepted straight
-- into the formal assets table by a group administrator. That bypassed the
-- organization working version, so the snapshot, quality checks, publishing and
-- rollback that every other asset change goes through never applied, and the
-- organization that actually owns the address had no way to confirm it.
--
-- Acceptance now produces an organization working version instead. The candidate
-- points at the draft so the publish step can settle it, and a finding may hold
-- an unpublished draft so an imported vulnerability stops being blocked on asset
-- bookkeeping.
ALTER TABLE asset_candidates
    ADD COLUMN working_version_draft_id uuid REFERENCES inventory_asset_drafts(id) ON DELETE SET NULL;

CREATE INDEX asset_candidates_working_version_idx
    ON asset_candidates(working_version_draft_id)
    WHERE working_version_draft_id IS NOT NULL;

-- A candidate is accepted once its working version has been created, and only
-- reaches 'accepted' when that draft is published. In the meantime the draft
-- itself records what happened, so these rows stay pending and remain visible.
COMMENT ON COLUMN asset_candidates.working_version_draft_id IS
    'Organization working version created when a finding-import asset candidate was confirmed; the candidate becomes accepted once this draft is published.';

ALTER TABLE security_findings
    ADD COLUMN pending_asset_draft_id uuid REFERENCES inventory_asset_drafts(id) ON DELETE SET NULL;

CREATE INDEX security_findings_pending_asset_draft_idx
    ON security_findings(pending_asset_draft_id)
    WHERE pending_asset_draft_id IS NOT NULL;

COMMENT ON COLUMN security_findings.pending_asset_draft_id IS
    'Unpublished asset working version this finding belongs to. Lets a vulnerability be tracked before the asset register catches up; set when the draft is published.';

-- +goose Down
DROP INDEX IF EXISTS security_findings_pending_asset_draft_idx;
ALTER TABLE security_findings DROP COLUMN IF EXISTS pending_asset_draft_id;
DROP INDEX IF EXISTS asset_candidates_working_version_idx;
ALTER TABLE asset_candidates DROP COLUMN IF EXISTS working_version_draft_id;
