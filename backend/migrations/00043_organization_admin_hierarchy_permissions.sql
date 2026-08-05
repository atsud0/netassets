-- +goose Up
UPDATE roles
SET description = '管理授权组织树内的组织信息、用户、业务系统、网络资产和填报数据'
WHERE role_key = 'organization_admin';

INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, permission.permission_key
FROM roles r
CROSS JOIN unnest(ARRAY['organization.create', 'organization.update']::varchar[]) AS permission(permission_key)
WHERE r.role_key = 'organization_admin'
ON CONFLICT DO NOTHING;

-- +goose Down
DELETE FROM role_permissions rp
USING roles r
WHERE rp.role_id = r.id
  AND r.role_key = 'organization_admin'
  AND rp.permission_key IN ('organization.create', 'organization.update');

UPDATE roles
SET description = '管理所属组织的业务系统、网络资产和填报数据'
WHERE role_key = 'organization_admin';
