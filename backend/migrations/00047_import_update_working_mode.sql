-- +goose Up
ALTER TABLE asset_imports
    ADD COLUMN import_mode varchar(32) NOT NULL DEFAULT 'skip_duplicates'
        CHECK (import_mode IN ('skip_duplicates', 'update_working')),
    ADD COLUMN created_count integer NOT NULL DEFAULT 0,
    ADD COLUMN updated_count integer NOT NULL DEFAULT 0;

UPDATE asset_imports SET created_count = success_count;

ALTER TABLE business_system_imports
    ADD COLUMN import_mode varchar(32) NOT NULL DEFAULT 'skip_duplicates'
        CHECK (import_mode IN ('skip_duplicates', 'update_working')),
    ADD COLUMN created_count integer NOT NULL DEFAULT 0,
    ADD COLUMN updated_count integer NOT NULL DEFAULT 0;

UPDATE business_system_imports SET created_count = success_count;

-- +goose Down
ALTER TABLE business_system_imports
    DROP COLUMN updated_count,
    DROP COLUMN created_count,
    DROP COLUMN import_mode;

ALTER TABLE asset_imports
    DROP COLUMN updated_count,
    DROP COLUMN created_count,
    DROP COLUMN import_mode;
