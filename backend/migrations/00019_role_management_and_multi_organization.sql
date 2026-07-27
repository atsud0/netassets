-- +goose Up
-- Support a session-level working organization while keeping the primary organization
-- as the account's default identity.
ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS current_organization_id uuid REFERENCES organizations(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS sessions_current_organization_idx ON sessions(current_organization_id);

-- System roles are seeded by bootstrap and cannot be changed through the management UI.
-- Custom roles can be disabled without removing historical role assignments.
ALTER TABLE roles
    ADD COLUMN IF NOT EXISTS status varchar(16) NOT NULL DEFAULT 'enabled'
        CHECK (status IN ('enabled', 'disabled'));
CREATE INDEX IF NOT EXISTS roles_scope_status_idx ON roles(role_scope, status);

-- Existing data predates the one-primary-organization invariant. Normalize it first,
-- then protect it with a partial unique index. Service code guarantees at least one
-- appointment and exactly one primary appointment for every managed user.
WITH ranked AS (
    SELECT user_id, organization_id,
           row_number() OVER (PARTITION BY user_id ORDER BY is_primary DESC, created_at ASC, organization_id) AS position
    FROM user_organizations
)
UPDATE user_organizations uo
SET is_primary = (ranked.position = 1)
FROM ranked
WHERE ranked.user_id = uo.user_id AND ranked.organization_id = uo.organization_id;

CREATE UNIQUE INDEX IF NOT EXISTS user_organizations_one_primary_idx
    ON user_organizations(user_id)
    WHERE is_primary;

-- +goose Down
DROP INDEX IF EXISTS user_organizations_one_primary_idx;
DROP INDEX IF EXISTS roles_scope_status_idx;
ALTER TABLE roles DROP COLUMN IF EXISTS status;
DROP INDEX IF EXISTS sessions_current_organization_idx;
ALTER TABLE sessions DROP COLUMN IF EXISTS current_organization_id;
