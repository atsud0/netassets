-- +goose Up
-- A finding import can report an address that the asset register does not hold
-- yet, and only the organization that owns the address can classify it. Group
-- administrators handled every confirmation themselves, which does not scale and
-- leaves the subsidiary with no way to close the gap for its own assets.
--
-- This table is the explicit opt-in that lets a subsidiary confirm its own asset
-- candidates. It is closed by default: without a row an organization user cannot
-- accept candidates, so headquarters stays in control of the delegation.
CREATE TABLE asset_candidate_self_service_policies (
    organization_id uuid PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    allow_self_service boolean NOT NULL DEFAULT false,
    allowed_asset_types text[] NOT NULL DEFAULT '{}',
    require_enabled_organization boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by uuid REFERENCES users(id) ON DELETE SET NULL
);

COMMENT ON TABLE asset_candidate_self_service_policies IS
    'Delegation that lets an organization confirm its own finding-import asset candidates instead of waiting for a group administrator. Closed by default.';

-- +goose Down
DROP TABLE IF EXISTS asset_candidate_self_service_policies;
