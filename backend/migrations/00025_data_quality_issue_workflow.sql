-- +goose Up
CREATE TABLE data_quality_scan_batches (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid REFERENCES organizations(id) ON DELETE SET NULL,
    resource_type varchar(32) CHECK (resource_type IS NULL OR resource_type IN ('asset', 'business_system')),
    severity varchar(16) CHECK (severity IS NULL OR severity IN ('error', 'warning')),
    asset_count bigint NOT NULL DEFAULT 0,
    system_count bigint NOT NULL DEFAULT 0,
    enabled_rule_count integer NOT NULL DEFAULT 0,
    total_issue_count bigint NOT NULL DEFAULT 0,
    error_count bigint NOT NULL DEFAULT 0,
    warning_count bigint NOT NULL DEFAULT 0,
    auto_closed_count bigint NOT NULL DEFAULT 0,
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX data_quality_scan_batches_created_idx ON data_quality_scan_batches(created_at DESC);
CREATE INDEX data_quality_scan_batches_org_idx ON data_quality_scan_batches(organization_id, created_at DESC);

CREATE TABLE data_quality_issues (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    rule_id uuid NOT NULL REFERENCES data_quality_rules(id) ON DELETE RESTRICT,
    resource_type varchar(32) NOT NULL CHECK (resource_type IN ('asset', 'business_system')),
    resource_id uuid NOT NULL,
    resource_name varchar(512) NOT NULL,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    organization_code varchar(128) NOT NULL,
    organization_name varchar(255) NOT NULL,
    rule_name varchar(128) NOT NULL,
    target_field varchar(128) NOT NULL,
    severity varchar(16) NOT NULL CHECK (severity IN ('error', 'warning')),
    status varchar(24) NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'in_progress', 'pending_review', 'closed', 'exempted')),
    assignee_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    rectification_note text,
    submitted_by uuid REFERENCES users(id) ON DELETE SET NULL,
    submitted_at timestamptz,
    review_note text,
    reviewed_by uuid REFERENCES users(id) ON DELETE SET NULL,
    reviewed_at timestamptz,
    exemption_reason text,
    exemption_expires_at timestamptz,
    resolution_type varchar(24)
        CHECK (resolution_type IS NULL OR resolution_type IN ('corrected', 'auto_resolved', 'exempted', 'false_positive')),
    first_detected_batch_id uuid NOT NULL REFERENCES data_quality_scan_batches(id) ON DELETE RESTRICT,
    last_detected_batch_id uuid NOT NULL REFERENCES data_quality_scan_batches(id) ON DELETE RESTRICT,
    first_detected_at timestamptz NOT NULL DEFAULT now(),
    last_detected_at timestamptz NOT NULL DEFAULT now(),
    occurrence_count integer NOT NULL DEFAULT 1,
    closed_at timestamptz,
    updated_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (rule_id, resource_type, resource_id, organization_id)
);
CREATE INDEX data_quality_issues_scope_idx ON data_quality_issues(organization_id, status, severity, updated_at DESC);
CREATE INDEX data_quality_issues_resource_idx ON data_quality_issues(resource_type, resource_id);
CREATE INDEX data_quality_issues_assignee_idx ON data_quality_issues(assignee_user_id, status, updated_at DESC);
CREATE INDEX data_quality_issues_last_batch_idx ON data_quality_issues(last_detected_batch_id);

CREATE TABLE data_quality_scan_batch_issues (
    batch_id uuid NOT NULL REFERENCES data_quality_scan_batches(id) ON DELETE CASCADE,
    issue_id uuid NOT NULL REFERENCES data_quality_issues(id) ON DELETE CASCADE,
    PRIMARY KEY (batch_id, issue_id)
);
CREATE INDEX data_quality_scan_batch_issues_issue_idx ON data_quality_scan_batch_issues(issue_id, batch_id);

CREATE TABLE data_quality_issue_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    issue_id uuid NOT NULL REFERENCES data_quality_issues(id) ON DELETE CASCADE,
    actor_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    event_type varchar(32) NOT NULL CHECK (event_type IN ('detected', 'started', 'submitted', 'approved', 'returned', 'exempted', 'reopened', 'auto_resolved')),
    from_status varchar(24),
    to_status varchar(24) NOT NULL,
    note text,
    details jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX data_quality_issue_events_issue_idx ON data_quality_issue_events(issue_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS data_quality_issue_events;
DROP TABLE IF EXISTS data_quality_scan_batch_issues;
DROP TABLE IF EXISTS data_quality_issues;
DROP TABLE IF EXISTS data_quality_scan_batches;
