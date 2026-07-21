-- +goose Up
CREATE TABLE asset_imports (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    file_name varchar(255) NOT NULL,
    file_type varchar(16) NOT NULL CHECK (file_type IN ('csv', 'xlsx')),
    status varchar(16) NOT NULL CHECK (status IN ('completed', 'partial', 'failed')),
    total_count integer NOT NULL DEFAULT 0,
    success_count integer NOT NULL DEFAULT 0,
    failure_count integer NOT NULL DEFAULT 0,
    duplicate_count integer NOT NULL DEFAULT 0,
    errors jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX asset_imports_org_created_idx ON asset_imports(organization_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS asset_imports;
