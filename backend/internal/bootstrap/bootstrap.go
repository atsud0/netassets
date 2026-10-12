package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"netassets/internal/config"
)

var permissions = map[string]string{
	"remediation.assign": "分派修复任务", "remediation.review": "确认修复材料", "remediation.people.manage": "管理修复人员授权", "remediation.execute": "执行本人修复任务", "vendor.manage": "管理合作厂商",
	"organization.read":          "查看组织机构",
	"organization.create":        "新增组织机构",
	"organization.update":        "编辑组织机构",
	"system.manage":              "管理业务系统",
	"asset.manage":               "管理网络资产和网络区域",
	"inventory.manage":           "管理资产填报与审核",
	"data_quality.manage":        "处理数据质量问题",
	"security_test.manage":       "管理安全测试批次",
	"security_test.request":      "发起上线安全测试申请",
	"security_finding.manage":    "管理安全发现与风险接受",
	"security_workorder.manage":  "管理安全工单",
	"security_workorder.view":    "查看安全工单",
	"security_workorder.execute": "执行组织级安全工单",
	"security_workorder.assign":  "分派安全工单",
	"security_workorder.review":  "复核安全工单",
	"security_workorder.close":   "关闭安全工单",
	"asset_candidate.view":       "查看资产候选",
	"asset_candidate.follow_up":  "跟踪资产候选",
	"asset_candidate.confirm":    "确认资产发现并建立工作版本",
	"audit.read":                 "查看审计日志和变更记录",
	"recycle_bin.manage":         "管理回收站",
	"user.manage":                "管理用户、角色和任职",
}

func Ensure(ctx context.Context, pool *pgxpool.Pool, cfg config.Config, logger *slog.Logger) error {
	if strings.TrimSpace(cfg.BootstrapAdminPassword) == "" {
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
			return fmt.Errorf("check bootstrap users: %w", err)
		}
		if count == 0 {
			return fmt.Errorf("BOOTSTRAP_ADMIN_PASSWORD must be configured when no users exist")
		}
		return nil
	}
	if len(cfg.BootstrapAdminPassword) < 10 {
		return fmt.Errorf("BOOTSTRAP_ADMIN_PASSWORD must contain at least 10 characters")
	}

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin bootstrap transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('netassets-bootstrap'))`); err != nil {
		return fmt.Errorf("lock bootstrap: %w", err)
	}

	var organizationID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO organizations (organization_code, organization_name, short_name, organization_type, status, notes)
		VALUES ($1, $2, $3, 'group', 'enabled', '系统初始化创建的集团根组织')
		ON CONFLICT (organization_code) DO UPDATE SET
			organization_code = organizations.organization_code
		RETURNING id::text
	`, cfg.BootstrapGroupCode, cfg.BootstrapGroupName, cfg.BootstrapGroupShortName).Scan(&organizationID); err != nil {
		return fmt.Errorf("bootstrap group organization: %w", err)
	}

	for key, name := range permissions {
		if _, err := tx.Exec(ctx, `
			INSERT INTO permissions (permission_key, permission_name)
			VALUES ($1, $2)
			ON CONFLICT (permission_key) DO UPDATE SET permission_name = EXCLUDED.permission_name
		`, key, name); err != nil {
			return fmt.Errorf("bootstrap permission %s: %w", key, err)
		}
	}

	var roleID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO roles (role_key, role_name, role_scope, description, is_system)
		VALUES ('group_admin', '集团管理员', 'group', '拥有集团范围内全部管理权限', true)
		ON CONFLICT (role_key) DO UPDATE SET role_name = EXCLUDED.role_name, description = EXCLUDED.description
		RETURNING id::text
	`).Scan(&roleID); err != nil {
		return fmt.Errorf("bootstrap group administrator role: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO role_permissions (role_id, permission_key)
		SELECT $1::uuid, permission_key FROM permissions
		ON CONFLICT DO NOTHING
	`, roleID); err != nil {
		return fmt.Errorf("bootstrap role permissions: %w", err)
	}

	var securityOperatorRoleID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO roles (role_key, role_name, role_scope, description, is_system)
		VALUES ('security_operator', '集团安全运营管理员', 'group', '负责集团范围安全测试、漏洞治理和安全工单闭环', true)
		ON CONFLICT (role_key) DO UPDATE SET
			role_name = EXCLUDED.role_name,
			role_scope = EXCLUDED.role_scope,
			description = EXCLUDED.description,
			is_system = true,
			status = 'enabled'
		RETURNING id::text
	`).Scan(&securityOperatorRoleID); err != nil {
		return fmt.Errorf("bootstrap security operator role: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO role_permissions (role_id, permission_key)
		SELECT $1::uuid, unnest($2::varchar[])
		ON CONFLICT DO NOTHING
	`, securityOperatorRoleID, []string{"security_test.manage", "security_finding.manage", "security_workorder.manage", "security_workorder.view", "security_workorder.execute", "security_workorder.assign", "security_workorder.review", "security_workorder.close", "asset_candidate.view", "asset_candidate.follow_up", "asset_candidate.confirm"}); err != nil {
		return fmt.Errorf("bootstrap security operator permissions: %w", err)
	}

	organizationRoles := []struct {
		Key         string
		Name        string
		Description string
		Permissions []string
	}{
		{Key: "organization_admin", Name: "子公司管理员", Description: "管理授权组织树内的组织信息、用户、业务系统、网络资产和填报数据", Permissions: []string{"organization.read", "organization.create", "organization.update", "system.manage", "asset.manage", "inventory.manage", "data_quality.manage", "security_test.request", "user.manage", "remediation.assign", "remediation.review", "remediation.people.manage", "vendor.manage", "security_workorder.view", "security_workorder.execute"}},
		{Key: "organization_editor", Name: "子公司填报员", Description: "维护所属组织的业务系统、网络资产和填报数据", Permissions: []string{"organization.read", "system.manage", "asset.manage", "inventory.manage", "security_test.request"}},
	}
	for _, role := range organizationRoles {
		var organizationRoleID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO roles (role_key, role_name, role_scope, description, is_system)
			VALUES ($1, $2, 'organization', $3, true)
			ON CONFLICT (role_key) DO UPDATE SET role_name = EXCLUDED.role_name, description = EXCLUDED.description
			RETURNING id::text
		`, role.Key, role.Name, role.Description).Scan(&organizationRoleID); err != nil {
			return fmt.Errorf("bootstrap organization role %s: %w", role.Key, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO role_permissions (role_id, permission_key)
			SELECT $1::uuid, unnest($2::varchar[])
			ON CONFLICT DO NOTHING
		`, organizationRoleID, role.Permissions); err != nil {
			return fmt.Errorf("bootstrap organization role permissions %s: %w", role.Key, err)
		}
	}

	var userID string
	created := false
	err = tx.QueryRow(ctx, `SELECT id::text FROM users WHERE lower(username) = lower($1) AND deleted_at IS NULL`, cfg.BootstrapAdminUsername).Scan(&userID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("find bootstrap administrator: %w", err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		hash, err := bcrypt.GenerateFromPassword([]byte(cfg.BootstrapAdminPassword), 12)
		if err != nil {
			return fmt.Errorf("hash bootstrap administrator password: %w", err)
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO users (username, password_hash, display_name, status, password_changed_at)
			VALUES ($1, $2, $3, 'enabled', now())
			RETURNING id::text
		`, cfg.BootstrapAdminUsername, string(hash), cfg.BootstrapAdminDisplayName).Scan(&userID); err != nil {
			return fmt.Errorf("create bootstrap administrator: %w", err)
		}
		created = true
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO user_organizations (user_id, organization_id, is_primary)
		VALUES ($1::uuid, $2::uuid, true)
		ON CONFLICT (user_id, organization_id) DO UPDATE SET is_primary = true
	`, userID, organizationID); err != nil {
		return fmt.Errorf("attach bootstrap administrator organization: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_roles (user_id, role_id, organization_id, data_scope)
		SELECT $1::uuid, $2::uuid, NULL, 'all'
		WHERE NOT EXISTS (
			SELECT 1 FROM user_roles
			WHERE user_id = $1::uuid AND role_id = $2::uuid AND organization_id IS NULL
		)
	`, userID, roleID); err != nil {
		return fmt.Errorf("assign bootstrap administrator role: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE user_roles SET data_scope = 'all'
		WHERE user_id = $1::uuid AND role_id = $2::uuid AND organization_id IS NULL
	`, userID, roleID); err != nil {
		return fmt.Errorf("update bootstrap administrator role scope: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit bootstrap transaction: %w", err)
	}
	logger.Info("bootstrap data ensured", "group_code", cfg.BootstrapGroupCode, "admin_username", cfg.BootstrapAdminUsername, "admin_created", created)
	return nil
}
