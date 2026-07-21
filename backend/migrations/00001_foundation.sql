-- +goose Up
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE organizations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_id uuid REFERENCES organizations(id) ON DELETE RESTRICT,
    organization_code varchar(64) NOT NULL UNIQUE,
    organization_name varchar(255) NOT NULL,
    short_name varchar(128),
    organization_type varchar(32) NOT NULL CHECK (organization_type IN ('group', 'subsidiary', 'department', 'other')),
    manager varchar(128),
    contact varchar(255),
    status varchar(16) NOT NULL DEFAULT 'enabled' CHECK (status IN ('enabled', 'disabled')),
    notes text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX organizations_parent_idx ON organizations(parent_id);
CREATE INDEX organizations_type_status_idx ON organizations(organization_type, status);

CREATE TABLE users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    username varchar(128) NOT NULL UNIQUE,
    password_hash text NOT NULL,
    display_name varchar(128) NOT NULL,
    email varchar(255),
    status varchar(16) NOT NULL DEFAULT 'enabled' CHECK (status IN ('enabled', 'disabled', 'locked')),
    last_login_at timestamptz,
    password_changed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

CREATE TABLE user_organizations (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    is_primary boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, organization_id)
);
CREATE INDEX user_organizations_org_idx ON user_organizations(organization_id);

CREATE TABLE roles (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    role_key varchar(64) NOT NULL UNIQUE,
    role_name varchar(128) NOT NULL,
    role_scope varchar(32) NOT NULL CHECK (role_scope IN ('group', 'organization', 'system')),
    description text,
    is_system boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE permissions (
    permission_key varchar(128) PRIMARY KEY,
    permission_name varchar(128) NOT NULL,
    description text
);

CREATE TABLE role_permissions (
    role_id uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission_key varchar(128) NOT NULL REFERENCES permissions(permission_key) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_key)
);

CREATE TABLE user_roles (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    organization_id uuid REFERENCES organizations(id) ON DELETE CASCADE,
    data_scope varchar(40) NOT NULL CHECK (data_scope IN ('all', 'organization_and_descendants', 'organization', 'owned')),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX user_roles_unique_idx
    ON user_roles (user_id, role_id, organization_id) NULLS NOT DISTINCT;
CREATE INDEX user_roles_user_idx ON user_roles(user_id);
CREATE INDEX user_roles_org_idx ON user_roles(organization_id);

CREATE TABLE sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash char(64) NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    ip_address inet,
    user_agent text,
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz
);
CREATE INDEX sessions_user_idx ON sessions(user_id);
CREATE INDEX sessions_active_idx ON sessions(expires_at) WHERE revoked_at IS NULL;

CREATE TABLE audit_logs (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    organization_id uuid REFERENCES organizations(id) ON DELETE SET NULL,
    action varchar(128) NOT NULL,
    resource_type varchar(64),
    resource_id varchar(128),
    summary text,
    details jsonb NOT NULL DEFAULT '{}'::jsonb,
    request_ip inet,
    request_id varchar(128),
    result varchar(16) NOT NULL DEFAULT 'success' CHECK (result IN ('success', 'failure')),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_logs_created_idx ON audit_logs(created_at DESC);
CREATE INDEX audit_logs_org_idx ON audit_logs(organization_id, created_at DESC);
CREATE INDEX audit_logs_resource_idx ON audit_logs(resource_type, resource_id);

CREATE TABLE background_jobs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    job_type varchar(64) NOT NULL,
    organization_id uuid REFERENCES organizations(id) ON DELETE SET NULL,
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    status varchar(16) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'success', 'failed', 'cancelled')),
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    progress integer NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100),
    success_count integer NOT NULL DEFAULT 0,
    failure_count integer NOT NULL DEFAULT 0,
    skipped_count integer NOT NULL DEFAULT 0,
    attempts integer NOT NULL DEFAULT 0,
    max_attempts integer NOT NULL DEFAULT 3,
    available_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    finished_at timestamptz,
    error_message text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX background_jobs_claim_idx ON background_jobs(status, available_at) WHERE status = 'pending';
CREATE INDEX background_jobs_org_idx ON background_jobs(organization_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS background_jobs;
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS user_roles;
DROP TABLE IF EXISTS role_permissions;
DROP TABLE IF EXISTS permissions;
DROP TABLE IF EXISTS roles;
DROP TABLE IF EXISTS user_organizations;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS organizations;
