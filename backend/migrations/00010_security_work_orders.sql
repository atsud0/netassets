-- +goose Up
CREATE TABLE security_findings (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    finding_code varchar(64) NOT NULL UNIQUE,
    title varchar(255) NOT NULL,
    severity varchar(16) NOT NULL CHECK (severity IN ('critical', 'high', 'medium', 'low', 'info')),
    status varchar(24) NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'assigned', 'resolved', 'accepted_risk', 'false_positive')),
    assignee_organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    asset_id uuid REFERENCES assets(id) ON DELETE SET NULL,
    business_system_id uuid REFERENCES business_systems(id) ON DELETE SET NULL,
    vulnerability_type varchar(128),
    description text NOT NULL,
    evidence text,
    remediation_advice text,
    source varchar(24) NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'import', 'api', 'plugin')),
    discovered_at timestamptz NOT NULL DEFAULT now(),
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    updated_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX security_findings_org_status_idx ON security_findings(assignee_organization_id, status, discovered_at DESC);
CREATE INDEX security_findings_asset_idx ON security_findings(asset_id) WHERE asset_id IS NOT NULL;
CREATE INDEX security_findings_system_idx ON security_findings(business_system_id) WHERE business_system_id IS NOT NULL;

CREATE TABLE security_work_orders (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    work_order_no varchar(64) NOT NULL UNIQUE,
    title varchar(255) NOT NULL,
    assignee_organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    severity varchar(16) NOT NULL CHECK (severity IN ('critical', 'high', 'medium', 'low', 'info')),
    status varchar(32) NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'issued', 'in_progress', 'pending_verification', 'returned', 'closed', 'cancelled')),
    description text,
    remediation_requirement text NOT NULL,
    due_at timestamptz NOT NULL,
    issued_at timestamptz,
    accepted_at timestamptz,
    submitted_at timestamptz,
    verified_at timestamptz,
    closed_at timestamptz,
    cancelled_at timestamptz,
    remediation_progress text,
    remediation_result text,
    verification_result text,
    return_reason text,
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    updated_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX security_work_orders_org_status_idx ON security_work_orders(assignee_organization_id, status, due_at);
CREATE INDEX security_work_orders_status_due_idx ON security_work_orders(status, due_at);

CREATE TABLE security_work_order_findings (
    work_order_id uuid NOT NULL REFERENCES security_work_orders(id) ON DELETE CASCADE,
    finding_id uuid NOT NULL REFERENCES security_findings(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (work_order_id, finding_id)
);
CREATE INDEX security_work_order_findings_finding_idx ON security_work_order_findings(finding_id);

CREATE TABLE security_work_order_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    work_order_id uuid NOT NULL REFERENCES security_work_orders(id) ON DELETE CASCADE,
    actor_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    actor_organization_id uuid REFERENCES organizations(id) ON DELETE SET NULL,
    action varchar(64) NOT NULL,
    from_status varchar(32),
    to_status varchar(32),
    comment text,
    details jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX security_work_order_events_order_created_idx ON security_work_order_events(work_order_id, created_at, id);

-- +goose Down
DROP TABLE IF EXISTS security_work_order_events;
DROP TABLE IF EXISTS security_work_order_findings;
DROP TABLE IF EXISTS security_work_orders;
DROP TABLE IF EXISTS security_findings;
