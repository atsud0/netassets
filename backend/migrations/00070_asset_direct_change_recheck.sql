-- +goose Up
-- A group administrator can write the formal asset ledger directly: create,
-- edit, change ownership and delete. Every other role has to go through the
-- inventory task (organization working version -> submit -> group review), so
-- those direct writes are the only path into the formal ledger that nobody
-- reviews. The asset register is the source of truth for asset ownership and
-- vulnerability accounting, so each direct write must leave a reviewable record
-- and hand the organization a recheck item.
--
-- The table below records those writes. It is intentionally append-only in
-- spirit: an open row is a promise that the owning organization still has to
-- confirm the change, and closing it is the only way to record that.
CREATE TABLE asset_direct_change_recheck_tasks (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    asset_id uuid REFERENCES assets(id) ON DELETE CASCADE,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    action varchar(24) NOT NULL CHECK (action IN ('create', 'update', 'delete')),
    reason text NOT NULL,
    snapshot jsonb NOT NULL DEFAULT '{}'::jsonb,
    changed_fields jsonb NOT NULL DEFAULT '[]'::jsonb,
    status varchar(24) NOT NULL DEFAULT 'open'
        CHECK (status IN ('open', 'confirmed', 'disputed')),
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    confirmed_by uuid REFERENCES users(id) ON DELETE SET NULL,
    confirmed_at timestamptz,
    confirmation_note text,
    request_id varchar(128),
    request_ip inet
);

-- The open items are the work queue: an index that supports "this
-- organization's open recheck items, newest first" without scanning history.
CREATE INDEX asset_direct_change_recheck_open_idx
    ON asset_direct_change_recheck_tasks(organization_id, status, created_at DESC)
    WHERE status = 'open';

COMMENT ON TABLE asset_direct_change_recheck_tasks IS
    'Recheck items raised when a group administrator writes the formal asset ledger directly (bypassing the inventory task). One open row per direct write; the owning organization confirms or disputes it.';

COMMENT ON COLUMN asset_direct_change_recheck_tasks.snapshot IS
    'Asset state after the direct write (and, for update, the state before it under "before").';
COMMENT ON COLUMN asset_direct_change_recheck_tasks.changed_fields IS
    'Field-level before/after list for update actions; empty for create and delete.';

-- +goose Down
DROP TABLE IF EXISTS asset_direct_change_recheck_tasks;
