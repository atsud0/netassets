-- +goose Up
ALTER TABLE asset_candidates
    ADD COLUMN IF NOT EXISTS created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS updated_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN IF NOT EXISTS review_note text;

CREATE INDEX IF NOT EXISTS asset_candidates_status_created_idx
    ON asset_candidates(status, created_at DESC);

-- +goose Down
DROP INDEX IF EXISTS asset_candidates_status_created_idx;
ALTER TABLE asset_candidates
    DROP COLUMN IF EXISTS review_note,
    DROP COLUMN IF EXISTS updated_at,
    DROP COLUMN IF EXISTS created_by;
