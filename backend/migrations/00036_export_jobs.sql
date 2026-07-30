-- +goose Up
CREATE TABLE export_jobs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_type varchar(32) NOT NULL CHECK (resource_type IN ('asset', 'business_system', 'inventory_task')),
    format varchar(8) NOT NULL DEFAULT 'xlsx' CHECK (format = 'xlsx'),
    filter_snapshot jsonb NOT NULL DEFAULT '{}'::jsonb,
    principal_snapshot jsonb NOT NULL,
    requested_by uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    organization_id uuid REFERENCES organizations(id) ON DELETE SET NULL,
    required_permission varchar(128) NOT NULL,
    status varchar(16) NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'completed', 'failed', 'expired')),
    total_rows integer NOT NULL DEFAULT 0 CHECK (total_rows >= 0),
    processed_rows integer NOT NULL DEFAULT 0 CHECK (processed_rows >= 0),
    file_name text NOT NULL DEFAULT '',
    content_type text NOT NULL DEFAULT '',
    file_size bigint NOT NULL DEFAULT 0 CHECK (file_size >= 0),
    error_message text NOT NULL DEFAULT '',
    started_at timestamptz,
    completed_at timestamptz,
    expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX export_jobs_requester_created_idx ON export_jobs(requested_by, created_at DESC);
CREATE INDEX export_jobs_queue_idx ON export_jobs(status, created_at) WHERE status IN ('queued', 'running');
CREATE INDEX export_jobs_expiry_idx ON export_jobs(expires_at) WHERE status = 'completed';

-- +goose Down
DROP TABLE IF EXISTS export_jobs;
