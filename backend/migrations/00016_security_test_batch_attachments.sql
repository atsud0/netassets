-- +goose Up
CREATE TABLE security_test_batch_attachments (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    batch_id uuid NOT NULL REFERENCES security_test_batches(id) ON DELETE CASCADE,
    category varchar(24) NOT NULL CHECK (category IN ('test_report', 'reference')),
    file_name varchar(255) NOT NULL,
    stored_name varchar(128) NOT NULL UNIQUE,
    content_type varchar(128) NOT NULL,
    size_bytes bigint NOT NULL CHECK (size_bytes > 0 AND size_bytes <= 20971520),
    storage_path text NOT NULL,
    description varchar(500),
    uploaded_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX security_test_batch_attachments_batch_idx
    ON security_test_batch_attachments(batch_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS security_test_batch_attachments;
