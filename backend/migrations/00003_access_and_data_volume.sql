-- +goose Up
CREATE UNIQUE INDEX users_username_lower_unique_idx
    ON users (lower(username))
    WHERE deleted_at IS NULL;

ALTER TABLE business_system_data_security_profiles
    ADD COLUMN data_volume_over_100k varchar(16) NOT NULL DEFAULT 'unknown'
    CHECK (data_volume_over_100k IN ('yes', 'no', 'unknown'));

CREATE INDEX system_data_volume_over_100k_idx
    ON business_system_data_security_profiles(data_volume_over_100k);

-- +goose Down
DROP INDEX IF EXISTS system_data_volume_over_100k_idx;
ALTER TABLE business_system_data_security_profiles DROP COLUMN IF EXISTS data_volume_over_100k;
DROP INDEX IF EXISTS users_username_lower_unique_idx;
