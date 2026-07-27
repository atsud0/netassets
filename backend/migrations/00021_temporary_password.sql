-- +goose Up
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS must_change_password boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE users
    DROP COLUMN IF EXISTS must_change_password;
