-- +goose Up
CREATE TABLE inventory_submission_review_comments (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    submission_id uuid NOT NULL,
    resource_type varchar(32) NOT NULL CHECK (resource_type IN ('business_system', 'network_asset')),
    resource_id uuid NOT NULL,
    resource_key text NOT NULL,
    resource_name text NOT NULL,
    comment text NOT NULL CHECK (length(btrim(comment)) BETWEEN 1 AND 2000),
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (submission_id, resource_type, resource_id)
        REFERENCES inventory_submission_items(submission_id, resource_type, resource_id)
        ON DELETE CASCADE
);
CREATE INDEX inventory_submission_review_comments_submission_idx
    ON inventory_submission_review_comments(submission_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS inventory_submission_review_comments;
