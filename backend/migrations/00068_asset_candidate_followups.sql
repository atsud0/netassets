-- +goose Up
-- Candidate follow-up is a small collaboration ledger, not a second work-order
-- system. It lets headquarters security record a reminder or handoff request
-- without granting asset confirmation or publication rights.
CREATE TABLE asset_candidate_followups (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    asset_candidate_id uuid NOT NULL REFERENCES asset_candidates(id) ON DELETE CASCADE,
    actor_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    action varchar(24) NOT NULL CHECK (action IN ('note', 'reminder', 'handoff_request')),
    comment text NOT NULL CHECK (length(btrim(comment)) > 0),
    request_id varchar(128),
    request_ip inet,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX asset_candidate_followups_candidate_created_idx
    ON asset_candidate_followups(asset_candidate_id, created_at DESC, id DESC);

COMMENT ON TABLE asset_candidate_followups IS
    '审计型资产候选跟进记录；不改变候选审核状态，也不授予资产确认或发布权限。';

-- +goose Down
DROP TABLE IF EXISTS asset_candidate_followups;
