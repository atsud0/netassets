-- +goose Up
CREATE TABLE api_tokens (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name varchar(100) NOT NULL,
    token_prefix varchar(16) NOT NULL,
    token_hash char(64) NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    last_used_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz,
    CHECK (expires_at > created_at)
);

CREATE UNIQUE INDEX api_tokens_user_active_name_idx
    ON api_tokens (user_id, lower(name))
    WHERE revoked_at IS NULL;
CREATE INDEX api_tokens_user_created_idx ON api_tokens (user_id, created_at DESC);
CREATE INDEX api_tokens_active_hash_idx ON api_tokens (token_hash) WHERE revoked_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS api_tokens;
