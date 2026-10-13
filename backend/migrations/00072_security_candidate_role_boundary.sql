-- +goose Up
-- The security operator may inspect and follow up asset discoveries, but must
-- not confirm ownership or create an organization working version. That
-- capability was accidentally reintroduced by the bootstrap role seed after
-- migration 00066 separated candidate collaboration from asset management.
DELETE FROM role_permissions
WHERE role_id = (SELECT id FROM roles WHERE role_key = 'security_operator')
  AND permission_key = 'asset_candidate.confirm';

-- +goose Down
-- Restore the pre-00072 role seed for an explicit rollback. The permission row
-- itself may be absent in an old database, so only restore it when both the
-- role and permission already exist; bootstrap/migrations own permission seed
-- creation and this rollback must not invent a new permission definition.
INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, p.permission_key
FROM roles r
JOIN permissions p ON p.permission_key = 'asset_candidate.confirm'
WHERE r.role_key = 'security_operator'
ON CONFLICT DO NOTHING;
