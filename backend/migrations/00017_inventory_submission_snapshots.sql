-- +goose Up
CREATE TABLE inventory_submissions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id uuid NOT NULL REFERENCES inventory_tasks(id) ON DELETE CASCADE,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    submission_version integer NOT NULL CHECK (submission_version > 0),
    previous_submission_id uuid REFERENCES inventory_submissions(id) ON DELETE SET NULL,
    submitted_by uuid REFERENCES users(id) ON DELETE SET NULL,
    submitted_at timestamptz NOT NULL DEFAULT now(),
    submission_note text,
    system_count bigint NOT NULL DEFAULT 0 CHECK (system_count >= 0),
    asset_count bigint NOT NULL DEFAULT 0 CHECK (asset_count >= 0),
    UNIQUE (task_id, organization_id, submission_version)
);
CREATE INDEX inventory_submissions_org_time_idx
    ON inventory_submissions(organization_id, submitted_at DESC);
CREATE INDEX inventory_submissions_task_org_idx
    ON inventory_submissions(task_id, organization_id, submission_version DESC);

CREATE TABLE inventory_submission_items (
    submission_id uuid NOT NULL REFERENCES inventory_submissions(id) ON DELETE CASCADE,
    resource_type varchar(32) NOT NULL CHECK (resource_type IN ('business_system', 'network_asset')),
    resource_id uuid NOT NULL,
    resource_key text NOT NULL,
    resource_name text NOT NULL,
    snapshot_data jsonb NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (submission_id, resource_type, resource_id)
);
CREATE INDEX inventory_submission_items_lookup_idx
    ON inventory_submission_items(submission_id, resource_type, resource_key);

ALTER TABLE inventory_task_organizations
    ADD COLUMN latest_submission_id uuid REFERENCES inventory_submissions(id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE inventory_task_organizations DROP COLUMN IF EXISTS latest_submission_id;
DROP TABLE IF EXISTS inventory_submission_items;
DROP TABLE IF EXISTS inventory_submissions;
