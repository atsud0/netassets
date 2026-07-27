-- +goose Up
CREATE TABLE security_work_order_attachments (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    work_order_id uuid NOT NULL REFERENCES security_work_orders(id) ON DELETE CASCADE,
    category varchar(24) NOT NULL CHECK (category IN ('remediation', 'verification', 'reference')),
    file_name varchar(255) NOT NULL,
    stored_name varchar(128) NOT NULL UNIQUE,
    content_type varchar(128) NOT NULL,
    size_bytes bigint NOT NULL CHECK (size_bytes > 0 AND size_bytes <= 20971520),
    storage_path text NOT NULL,
    description varchar(500),
    uploaded_by uuid REFERENCES users(id) ON DELETE SET NULL,
    uploader_organization_id uuid REFERENCES organizations(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX security_work_order_attachments_order_created_idx
    ON security_work_order_attachments(work_order_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS security_work_order_attachments;
