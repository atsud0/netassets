package securityworkorder

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
	"netassets/internal/pagination"
)

// Run only with a dedicated migrated database. Unique fixtures are retained;
// unlike older integration helpers this test does not truncate shared tables.
func TestFindingOrganizationFilterPreservesSecurityScope(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("NETASSETS_TEST_DATABASE_URL is not set; requires a dedicated migrated database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	p03Bootstrap(t, pool, "FILTER_"+suffix)
	groupID := p03InsertOrg(t, pool, "", "FILTER_G_"+suffix, "筛选集团", "group")
	orgID := p03InsertOrg(t, pool, groupID, "FILTER_A_"+suffix, "筛选子公司A", "subsidiary")
	otherOrgID := p03InsertOrg(t, pool, groupID, "FILTER_B_"+suffix, "筛选子公司B", "subsidiary")
	operatorID := p03InsertUser(t, pool, "filter_operator_"+suffix, "无组织任职安全运营员")
	childID := p03InsertUser(t, pool, "filter_child_"+suffix, "子公司管理员")
	customID := p03InsertUser(t, pool, "filter_custom_"+suffix, "同名权限自定义角色")
	p03AssignRole(t, pool, operatorID, "security_operator", "", "all")
	p03AssignRole(t, pool, childID, "organization_admin", orgID, "organization")
	operator := p03Principal(t, pool, operatorID, []string{"security_operator"})
	child := p03Principal(t, pool, childID, []string{"organization_admin"})
	var roleID string
	roleKey := "filter_custom_" + suffix
	if err := pool.QueryRow(ctx, `INSERT INTO roles(role_key,role_name,role_scope,status) VALUES($1,'同名权限角色','organization','enabled') RETURNING id::text`, roleKey).Scan(&roleID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO role_permissions(role_id,permission_key) VALUES($1::uuid,'security_finding.manage')`, roleID); err != nil {
		t.Fatal(err)
	}
	p03AssignRole(t, pool, customID, roleKey, orgID, "organization")
	custom := p03Principal(t, pool, customID, []string{roleKey})
	if !operator.CanManageSecurityFindings() || custom.CanManageSecurityFindings() || child.CanManageSecurityFindings() {
		t.Fatal("fixture roles do not reflect the security permission boundary")
	}
	for index, organizationID := range []string{orgID, otherOrgID} {
		var assetID string
		value := fmt.Sprintf("filter-%d-%s.example.com", index, suffix)
		if err := pool.QueryRow(ctx, `INSERT INTO assets(asset_type,asset_value,normalized_value,environment,network_scope,status,importance,source) VALUES('domain',$1,$1,'production','public','active','normal','manual') RETURNING id::text`, value).Scan(&assetID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO asset_organizations(asset_id,organization_id,relationship_type,is_primary) VALUES($1::uuid,$2::uuid,'owner',true)`, assetID, organizationID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO security_findings(finding_code,title,severity,status,assignee_organization_id,asset_id,description,source,created_by,updated_by) VALUES($1,$1,'high','pending',$2::uuid,$3::uuid,'组织筛选回归','manual',$4::uuid,$4::uuid)`, fmt.Sprintf("FILTER_%d_%s", index, suffix), organizationID, assetID, operatorID); err != nil {
			t.Fatal(err)
		}
	}
	service := NewService(pool, t.TempDir())
	page, _ := pagination.Normalize(1, 100)
	t.Run("global security role can narrow both list and summary", func(t *testing.T) {
		for _, organizationID := range []string{orgID, otherOrgID} {
			list, err := service.SearchFindings(ctx, operator, organizationID, "", "", page)
			if err != nil || list.Total != 1 || len(list.Items) != 1 || list.Items[0].AssigneeOrganizationID != organizationID {
				t.Errorf("operator filtered list = %+v, %v; want exactly own filter result", list, err)
			}
			summary, err := service.SummarizeFindings(ctx, operator, organizationID)
			if err != nil || summary.Total != 1 || summary.Pending != 1 {
				t.Errorf("operator filtered summary = %+v, %v; want total=pending=1", summary, err)
			}
		}
	})
	t.Run("organization administrator still sees only own ledger", func(t *testing.T) {
		list, err := service.SearchFindings(ctx, child, orgID, "", "", page)
		if err != nil || list.Total != 1 {
			t.Fatalf("child own filtered list = %+v, %v", list, err)
		}
		summary, err := service.SummarizeFindings(ctx, child, orgID)
		if err != nil || summary.Total != 1 {
			t.Fatalf("child own summary = %+v, %v", summary, err)
		}
	})
	withoutPermission := operator
	withoutPermission.Permissions = nil
	for _, actor := range []struct {
		name      string
		principal auth.Principal
	}{
		{"child foreign organization", child},
		{"custom role with same permission", custom},
		{"security role without permission", withoutPermission},
	} {
		t.Run(actor.name, func(t *testing.T) {
			if _, err := service.SearchFindings(ctx, actor.principal, otherOrgID, "", "", page); !errors.Is(err, ErrForbidden) {
				t.Errorf("foreign filtered list error = %v; want forbidden", err)
			}
			if _, err := service.SummarizeFindings(ctx, actor.principal, otherOrgID); !errors.Is(err, ErrForbidden) {
				t.Errorf("foreign summary error = %v; want forbidden", err)
			}
		})
	}
	t.Run("global security role still validates filter UUID", func(t *testing.T) {
		if _, err := service.SearchFindings(ctx, operator, "not-a-uuid", "", "", page); !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid filter list error = %v; want invalid", err)
		}
		if _, err := service.SummarizeFindings(ctx, operator, "not-a-uuid"); !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid filter summary error = %v; want invalid", err)
		}
	})
}
