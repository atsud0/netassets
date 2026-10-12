-- +goose Up
-- Candidate collaboration is deliberately separate from asset.manage. Group
-- security personnel may follow the queue without confirming or publishing an
-- asset on behalf of an organization.
INSERT INTO permissions (permission_key, permission_name)
VALUES
    ('asset_candidate.view', '查看资产候选'),
    ('asset_candidate.follow_up', '跟踪资产候选')
ON CONFLICT (permission_key) DO UPDATE SET permission_name = EXCLUDED.permission_name;

INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, p.permission_key
FROM roles r
JOIN permissions p ON p.permission_key IN ('asset_candidate.view', 'asset_candidate.follow_up')
WHERE r.role_key = 'security_operator'
ON CONFLICT DO NOTHING;

-- +goose Down
DELETE FROM role_permissions
WHERE permission_key IN ('asset_candidate.view', 'asset_candidate.follow_up');
DELETE FROM permissions
WHERE permission_key IN ('asset_candidate.view', 'asset_candidate.follow_up');
