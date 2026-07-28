-- +goose Up
CREATE TABLE notification_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type varchar(80) NOT NULL,
    category varchar(64) NOT NULL,
    target_organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    actor_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    required_permission varchar(128) NOT NULL,
    resource_type varchar(64) NOT NULL,
    resource_id varchar(128),
    resource_label text NOT NULL DEFAULT '',
    batch_key varchar(255) NOT NULL,
    item_count integer NOT NULL DEFAULT 1 CHECK (item_count > 0),
    level varchar(16) NOT NULL DEFAULT 'info' CHECK (level IN ('info', 'warning', 'danger')),
    details jsonb NOT NULL DEFAULT '{}'::jsonb,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (event_type, target_organization_id, batch_key)
);

CREATE INDEX notification_events_target_time_idx
    ON notification_events(target_organization_id, occurred_at DESC);
CREATE INDEX notification_events_actor_idx
    ON notification_events(actor_user_id, occurred_at DESC);
CREATE INDEX notification_events_category_idx
    ON notification_events(category, occurred_at DESC);

-- +goose Down
DROP TABLE IF EXISTS notification_events;
