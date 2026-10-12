-- +goose Up
INSERT INTO permissions (permission_key, permission_name)
VALUES
    ('security_workorder.view', '查看安全工单'),
    ('security_workorder.execute', '执行组织级安全工单'),
    ('security_workorder.assign', '分派安全工单'),
    ('security_workorder.review', '复核安全工单'),
    ('security_workorder.close', '关闭安全工单')
ON CONFLICT (permission_key) DO UPDATE SET permission_name = EXCLUDED.permission_name;

-- Existing group roles must receive the new permissions without requiring a
-- manual role repair. Organization administrators receive only the scoped
-- read/execute pair; they do not gain group-level issue/review/close rights.
INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, p.permission_key
FROM roles r
JOIN permissions p ON p.permission_key IN (
    'security_workorder.view',
    'security_workorder.execute',
    'security_workorder.assign',
    'security_workorder.review',
    'security_workorder.close'
)
WHERE r.role_key IN ('group_admin', 'security_operator')
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, p.permission_key
FROM roles r
JOIN permissions p ON p.permission_key IN ('security_workorder.view', 'security_workorder.execute')
WHERE r.role_key = 'organization_admin'
ON CONFLICT DO NOTHING;

-- +goose Down
DELETE FROM role_permissions
WHERE permission_key IN (
    'security_workorder.view',
    'security_workorder.execute',
    'security_workorder.assign',
    'security_workorder.review',
    'security_workorder.close'
);
DELETE FROM permissions
WHERE permission_key IN (
    'security_workorder.view',
    'security_workorder.execute',
    'security_workorder.assign',
    'security_workorder.review',
    'security_workorder.close'
);
