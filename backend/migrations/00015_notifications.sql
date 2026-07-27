-- +goose Up
CREATE TABLE notification_reads (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    notification_key varchar(255) NOT NULL,
    read_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, notification_key)
);
CREATE INDEX notification_reads_read_at_idx ON notification_reads(user_id, read_at DESC);

-- +goose Down
DROP TABLE IF EXISTS notification_reads;
