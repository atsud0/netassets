package notification

import (
	"strings"
	"testing"

	"netassets/internal/auth"
)

func TestValidateCategory(t *testing.T) {
	for _, category := range []string{
		"", "work_order_review", "work_order_received", "work_order_returned", "work_order_overdue", "work_order_due_soon",
		"risk_review", "quality_review", "quality_received", "quality_returned", "quality_exemption",
		CategoryAssetChange, CategoryBusinessSystemChange, CategoryFindingReceived,
		CategoryInventoryTaskReview, CategoryInventoryTaskReceived, CategoryInventoryTaskReturned,
		CategoryInventoryTaskDueSoon, CategoryInventoryTaskOverdue, CategoryWorkingVersion,
	} {
		if !ValidateCategory(category) {
			t.Fatalf("ValidateCategory(%q) = false", category)
		}
	}
	for _, category := range []string{"unknown", "risk", "work_order"} {
		if ValidateCategory(category) {
			t.Fatalf("ValidateCategory(%q) = true", category)
		}
	}
}

func TestWorkingVersionNotificationsExcludeGroupAdministrators(t *testing.T) {
	for _, fragment := range []string{
		"ne.category <> 'working_version' OR NOT $2::boolean",
		"WHEN 'working_version.published' THEN '工作版本已发布'",
		"WHEN ne.event_type='working_version.published' THEN '/inventory-tasks?task_id='",
		"task_id IS NULL AND draft_status='editing' AND updated_at < now()-interval '7 days'",
		"'working_version:stale:' || stale.resource_kind",
		"visible.permission_key IN (",
	} {
		if !strings.Contains(notificationCandidatesCTE, fragment) {
			t.Fatalf("notification candidate SQL does not contain %q", fragment)
		}
	}
}

func TestSecurityOperatorNotificationScope(t *testing.T) {
	operator := auth.Principal{
		Roles: []string{auth.RoleSecurityOperator},
		Permissions: []string{
			auth.PermissionSecurityFinding,
			auth.PermissionSecurityWorkOrder,
		},
	}
	scope := scopeFor(operator)
	if scope.groupAdmin {
		t.Fatal("security operator must not receive group administrator data-quality scope")
	}
	if !scope.canManageSecurityWorkOrders || !scope.canManageSecurityFindings {
		t.Fatalf("security operator notification scope = %+v, want work-order and finding scopes", scope)
	}

	organizationRole := auth.Principal{
		Roles:       []string{"organization_security"},
		Permissions: []string{auth.PermissionSecurityFinding, auth.PermissionSecurityWorkOrder},
	}
	organizationScope := scopeFor(organizationRole)
	if organizationScope.groupAdmin || organizationScope.canManageSecurityWorkOrders || organizationScope.canManageSecurityFindings {
		t.Fatalf("organization role notification scope = %+v, want no group scope", organizationScope)
	}
}
