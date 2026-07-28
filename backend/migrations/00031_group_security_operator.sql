-- +goose Up
INSERT INTO permissions (permission_key, permission_name)
VALUES
    ('security_test.manage', '管理安全测试批次'),
    ('security_finding.manage', '管理安全发现与风险接受'),
    ('security_workorder.manage', '管理安全工单')
ON CONFLICT (permission_key) DO UPDATE SET permission_name = EXCLUDED.permission_name;

INSERT INTO roles (role_key, role_name, role_scope, description, is_system, status)
VALUES (
    'security_operator',
    '集团安全运营管理员',
    'group',
    '负责集团范围安全测试、漏洞治理和安全工单闭环',
    true,
    'enabled'
)
ON CONFLICT (role_key) DO UPDATE SET
    role_name = EXCLUDED.role_name,
    role_scope = EXCLUDED.role_scope,
    description = EXCLUDED.description,
    is_system = true,
    status = 'enabled';

INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, p.permission_key
FROM roles r
JOIN permissions p ON p.permission_key IN (
    'security_test.manage',
    'security_finding.manage',
    'security_workorder.manage'
)
WHERE r.role_key = 'security_operator'
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, 'security_finding.manage'
FROM roles r
WHERE r.role_key = 'group_admin'
ON CONFLICT DO NOTHING;

-- +goose Down
DELETE FROM user_roles
WHERE role_id = (SELECT id FROM roles WHERE role_key = 'security_operator');

DELETE FROM role_permissions
WHERE role_id = (SELECT id FROM roles WHERE role_key = 'security_operator');

DELETE FROM roles WHERE role_key = 'security_operator';

DELETE FROM role_permissions WHERE permission_key = 'security_finding.manage';
DELETE FROM permissions WHERE permission_key = 'security_finding.manage';
