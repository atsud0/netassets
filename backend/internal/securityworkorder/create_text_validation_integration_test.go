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
)

// Uses its own actor/org/assets, never truncates or edits another test's data.
func TestSecurityCreateTextBoundariesPersistAndRejectWithoutSideEffects(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if url == "" {
		t.Skip("requires a dedicated migrated test database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var org, user, assetID string
	t.Cleanup(func() {
		for _, query := range []string{
			`DELETE FROM notification_events WHERE actor_user_id=$1::uuid`,
			`DELETE FROM audit_logs WHERE user_id=$1::uuid`,
			`DELETE FROM security_work_orders WHERE created_by=$1::uuid`,
			`DELETE FROM security_findings WHERE created_by=$1::uuid`,
			`DELETE FROM assets WHERE created_by=$1::uuid`,
			`DELETE FROM users WHERE id=$1::uuid`,
		} {
			if user != "" {
				if _, err := pool.Exec(context.Background(), query, user); err != nil {
					t.Errorf("clean own fixture: %v", err)
				}
			}
		}
		if org != "" {
			if _, err := pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1::uuid`, org); err != nil {
				t.Errorf("clean own organization: %v", err)
			}
		}
	})
	id := func(query string, args ...any) string {
		t.Helper()
		var result string
		if err := pool.QueryRow(ctx, query, args...).Scan(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	stamp := fmt.Sprint(time.Now().UnixNano())
	org = id(`INSERT INTO organizations(organization_code,organization_name,organization_type) VALUES($1,'字段落库边界回归','subsidiary') RETURNING id::text`, "SEC_TEXT_"+stamp)
	user = id(`INSERT INTO users(username,password_hash,display_name) VALUES($1,'unused','字段落库测试员') RETURNING id::text`, "sec_text_"+stamp)
	assetID = id(`INSERT INTO assets(asset_type,asset_value,normalized_value,created_by) VALUES('domain',$1,$1,$2::uuid) RETURNING id::text`, "text-"+stamp+".invalid", user)
	if _, err := pool.Exec(ctx, `INSERT INTO asset_organizations(asset_id,organization_id,relationship_type,is_primary,owner_user_id) VALUES($1::uuid,$2::uuid,'owner',true,$3::uuid)`, assetID, org, user); err != nil {
		t.Fatal(err)
	}
	actor := auth.Principal{UserID: user, Roles: []string{auth.RoleSecurityOperator}, Permissions: []string{auth.PermissionSecurityFinding, auth.PermissionSecurityWorkOrder}}
	unauthorized := auth.Principal{UserID: user, Roles: []string{"organization_admin"}, Permissions: actor.Permissions}
	service := NewService(pool)
	snapshot := func() string {
		t.Helper()
		return id(`SELECT jsonb_build_array(
   (SELECT coalesce(jsonb_agg(to_jsonb(f) ORDER BY f.id),'[]'::jsonb) FROM security_findings f WHERE f.created_by=$1::uuid),
   (SELECT coalesce(jsonb_agg(to_jsonb(w) ORDER BY w.id),'[]'::jsonb) FROM security_work_orders w WHERE w.created_by=$1::uuid),
   (SELECT coalesce(jsonb_agg(to_jsonb(l) ORDER BY l.work_order_id,l.finding_id),'[]'::jsonb) FROM security_work_order_findings l JOIN security_work_orders w ON w.id=l.work_order_id WHERE w.created_by=$1::uuid),
   (SELECT coalesce(jsonb_agg(to_jsonb(e) ORDER BY e.id),'[]'::jsonb) FROM security_work_order_events e JOIN security_work_orders w ON w.id=e.work_order_id WHERE w.created_by=$1::uuid),
   (SELECT coalesce(jsonb_agg(to_jsonb(a) ORDER BY a.id),'[]'::jsonb) FROM audit_logs a WHERE a.user_id=$1::uuid),
   (SELECT coalesce(jsonb_agg(to_jsonb(n) ORDER BY n.id),'[]'::jsonb) FROM notification_events n WHERE n.actor_user_id=$1::uuid)
  )::text`, user)
	}
	rejected := func(t *testing.T, want string, call func() error) {
		t.Helper()
		before := snapshot()
		err := call()
		var field *InputValidationError
		if !errors.Is(err, ErrInvalid) || !errors.As(err, &field) || field.Message != want {
			t.Fatalf("field rejection=%v, want %s", err, want)
		}
		if after := snapshot(); after != before {
			t.Fatal("rejected input changed finding/order/link/event/audit/notification state")
		}
	}
	for _, tc := range []struct{ name, char string }{{"ASCII", "a"}, {"Chinese", "中"}, {"emoji", "🙂"}} {
		t.Run(tc.name, func(t *testing.T) {
			input := FindingInput{Title: strings.Repeat(tc.char, 255), Severity: "high", Status: "pending", AssigneeOrganizationID: org, AssetID: assetID, VulnerabilityType: strings.Repeat(tc.char, 128), Description: strings.Repeat(tc.char, 20000), Evidence: strings.Repeat(tc.char, 30000), RemediationAdvice: strings.Repeat(tc.char, 20000), Source: "manual"}
			finding, err := service.CreateFinding(ctx, actor, input, "sec-text-create-"+stamp+"-"+tc.name, "")
			if err != nil {
				t.Fatal(err)
			}
			assertFinding := func(item Finding) {
				t.Helper()
				if item.Title != input.Title || item.VulnerabilityType != input.VulnerabilityType || item.Description != input.Description || item.Evidence != input.Evidence || item.RemediationAdvice != input.RemediationAdvice {
					t.Fatal("finding text was truncated or changed")
				}
			}
			assertFinding(finding)
			read, err := service.getFinding(ctx, finding.ID)
			if err != nil {
				t.Fatal(err)
			}
			assertFinding(read)
			input.ExpectedUpdatedAt = &finding.UpdatedAt
			for _, field := range []struct {
				name, message string
				change        func(*FindingInput)
			}{
				{"title", "漏洞标题最多 255 字", func(i *FindingInput) { i.Title += tc.char }},
				{"type", "漏洞类型最多 128 字", func(i *FindingInput) { i.VulnerabilityType += tc.char }},
				{"description", "漏洞描述最多 20000 字", func(i *FindingInput) { i.Description += tc.char }},
				{"evidence", "漏洞证据最多 30000 字", func(i *FindingInput) { i.Evidence += tc.char }},
				{"advice", "修复建议最多 20000 字", func(i *FindingInput) { i.RemediationAdvice += tc.char }},
				{"blank title", "漏洞标题不能为空", func(i *FindingInput) { i.Title = "　 \t" }},
				{"blank description", "漏洞描述不能为空", func(i *FindingInput) { i.Description = "　 \t" }},
			} {
				t.Run("finding/"+field.name, func(t *testing.T) {
					bad := input
					field.change(&bad)
					rejected(t, field.message, func() error {
						_, err := service.CreateFinding(ctx, actor, bad, "sec-text-rejected-"+stamp, "")
						return err
					})
					rejected(t, field.message, func() error {
						_, err := service.UpdateFinding(ctx, actor, finding.ID, bad, "sec-text-rejected-"+stamp, "")
						return err
					})
				})
			}
			order := WorkOrderInput{Title: strings.Repeat(tc.char, 255), AssigneeOrganizationID: org, Description: strings.Repeat(tc.char, 10000), RemediationRequirement: strings.Repeat(tc.char, 20000), DueAt: time.Now().Add(24 * time.Hour), FindingIDs: []string{finding.ID}}
			for _, field := range []struct {
				name, message string
				change        func(*WorkOrderInput)
			}{
				{"title", "工单标题最多 255 字", func(i *WorkOrderInput) { i.Title += tc.char }},
				{"description", "工单说明最多 10000 字", func(i *WorkOrderInput) { i.Description += tc.char }},
				{"requirement", "整改要求最多 20000 字", func(i *WorkOrderInput) { i.RemediationRequirement += tc.char }},
				{"blank title", "工单标题不能为空", func(i *WorkOrderInput) { i.Title = "　 \t" }},
				{"blank requirement", "整改要求不能为空", func(i *WorkOrderInput) { i.RemediationRequirement = "　 \t" }},
			} {
				t.Run("order/"+field.name, func(t *testing.T) {
					bad := order
					field.change(&bad)
					rejected(t, field.message, func() error {
						_, err := service.CreateWorkOrder(ctx, actor, bad, "sec-text-rejected-"+stamp, "")
						return err
					})
				})
			}
			before := snapshot()
			badFinding := input
			badFinding.Title = ""
			badOrder := order
			badOrder.Title = ""
			_, createErr := service.CreateFinding(ctx, unauthorized, badFinding, "sec-text-forbidden-"+stamp, "")
			_, updateErr := service.UpdateFinding(ctx, unauthorized, finding.ID, badFinding, "sec-text-forbidden-"+stamp, "")
			_, orderErr := service.CreateWorkOrder(ctx, unauthorized, badOrder, "sec-text-forbidden-"+stamp, "")
			if !errors.Is(createErr, ErrForbidden) || !errors.Is(updateErr, ErrForbidden) || !errors.Is(orderErr, ErrForbidden) {
				t.Fatalf("unauthorized: %v / %v / %v", createErr, updateErr, orderErr)
			}
			if snapshot() != before {
				t.Fatal("forbidden requests changed persisted state")
			}
			saved, err := service.CreateWorkOrder(ctx, actor, order, "sec-text-order-"+stamp, "")
			if err != nil {
				t.Fatal(err)
			}
			if saved.Title != order.Title || saved.Description != order.Description || saved.RemediationRequirement != order.RemediationRequirement || saved.Status != "draft" {
				t.Fatal("work order text was truncated or changed")
			}
			var title, description, requirement string
			if err := pool.QueryRow(ctx, `SELECT title,description,remediation_requirement FROM security_work_orders WHERE id=$1::uuid`, saved.ID).Scan(&title, &description, &requirement); err != nil {
				t.Fatal(err)
			}
			if title != order.Title || description != order.Description || requirement != order.RemediationRequirement {
				t.Fatal("work order database text differs from submitted text")
			}
			var audits, notifications, links, events int
			if err := pool.QueryRow(ctx, `SELECT
    (SELECT count(*) FROM audit_logs WHERE user_id=$1::uuid AND resource_id IN ($2,$3)),
    (SELECT count(*) FROM notification_events WHERE actor_user_id=$1::uuid AND resource_id=$2),
    (SELECT count(*) FROM security_work_order_findings WHERE work_order_id=$3::uuid AND finding_id=$2::uuid),
    (SELECT count(*) FROM security_work_order_events WHERE work_order_id=$3::uuid AND action='create')`, user, finding.ID, saved.ID).Scan(&audits, &notifications, &links, &events); err != nil {
				t.Fatal(err)
			}
			if audits != 2 || notifications != 1 || links != 1 || events != 1 {
				t.Fatalf("creation side effects audits=%d notifications=%d links=%d events=%d, want 2/1/1/1", audits, notifications, links, events)
			}
		})
	}
}
