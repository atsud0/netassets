package httpserver

import "testing"

func TestRepairAccountsHaveOnlyTheirWorkspaceAndAccountEndpoints(t *testing.T) {
	for _, path := range []string{"/api/v1/repair/tasks", "/api/v1/repair/tasks/task-id/actions/submit", "/api/v1/auth/me", "/api/v1/auth/profile", "/api/v1/auth/profile/password", "/api/v1/auth/sessions", "/api/v1/notifications"} {
		if !allowsRepairAccountRequest("GET", path) {
			t.Errorf("expected permitted account/workspace path: %s", path)
		}
	}
	for _, path := range []string{"/api/v1/assets", "/api/v1/business-systems", "/api/v1/security-findings", "/api/v1/security-work-orders", "/api/v1/remediation-tasks", "/api/v1/remediation-grants", "/api/v1/users", "/api/v1/organizations", "/api/v1/auth/api-tokens", "/api/v1/export-jobs", "/api/v1/audit-logs"} {
		if allowsRepairAccountRequest("GET", path) {
			t.Errorf("executor can access management path: %s", path)
		}
	}
}
