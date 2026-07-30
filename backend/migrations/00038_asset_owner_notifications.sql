-- +goose Up
CREATE INDEX asset_organizations_owner_user_idx
    ON asset_organizations(owner_user_id)
    WHERE owner_user_id IS NOT NULL;

ALTER TABLE notification_events
    ADD COLUMN target_user_id uuid REFERENCES users(id) ON DELETE SET NULL;

ALTER TABLE notification_events
    DROP CONSTRAINT notification_events_event_type_target_organization_id_batch_key_key;

ALTER TABLE notification_events
    ADD CONSTRAINT notification_events_target_dedup_key
    UNIQUE NULLS NOT DISTINCT (event_type, target_organization_id, target_user_id, batch_key);

CREATE INDEX notification_events_target_user_time_idx
    ON notification_events(target_user_id, occurred_at DESC)
    WHERE target_user_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS notification_events_target_user_time_idx;

ALTER TABLE notification_events
    DROP CONSTRAINT IF EXISTS notification_events_target_dedup_key;

ALTER TABLE notification_events
    DROP COLUMN IF EXISTS target_user_id;

ALTER TABLE notification_events
    ADD CONSTRAINT notification_events_event_type_target_organization_id_batch_key_key
    UNIQUE (event_type, target_organization_id, batch_key);

DROP INDEX IF EXISTS asset_organizations_owner_user_idx;
