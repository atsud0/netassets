-- +goose Up
CREATE TABLE security_finding_imports (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    test_batch_id uuid NOT NULL REFERENCES security_test_batches(id) ON DELETE CASCADE,
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
CREATE INDEX security_finding_imports_batch_created_idx ON security_finding_imports(test_batch_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS security_finding_imports;
