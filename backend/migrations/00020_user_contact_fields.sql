-- +goose Up
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS mobile_phone varchar(64),
    ADD COLUMN IF NOT EXISTS office_phone varchar(64);

-- +goose Down
ALTER TABLE users
    DROP COLUMN IF EXISTS office_phone,
    DROP COLUMN IF EXISTS mobile_phone;
