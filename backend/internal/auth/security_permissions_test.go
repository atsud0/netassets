package auth

import "testing"

func TestGroupSecurityPermissionBoundaries(t *testing.T) {
	tests := []struct {
		name              string
		principal         Principal
		test              bool
		finding           bool
		order             bool
		view              bool
		execute           bool
		candidateView     bool
		candidateFollowUp bool
	}{
		{
			name: "group administrator with permissions",
			principal: Principal{Roles: []string{RoleGroupAdmin}, Permissions: []string{
				PermissionSecurityTest, PermissionSecurityFinding, PermissionSecurityWorkOrder,
			}},
			test: true, finding: true, order: true, view: true, execute: true, candidateView: true, candidateFollowUp: true,
		},
		{
			name: "security operator with all security permissions",
			principal: Principal{Roles: []string{RoleSecurityOperator}, Permissions: []string{
				PermissionSecurityTest, PermissionSecurityFinding, PermissionSecurityWorkOrder, PermissionAssetCandidateView, PermissionAssetCandidateFollowUp,
			}},
			test: true, finding: true, order: true, view: true, execute: true, candidateView: true, candidateFollowUp: true,
		},
		{
			name: "security operator permissions remain independent",
			principal: Principal{Roles: []string{RoleSecurityOperator}, Permissions: []string{
				PermissionSecurityFinding,
			}},
			test: false, finding: true, order: false,
		},
		{
			name:          "security operator candidate collaboration is independent",
			principal:     Principal{Roles: []string{RoleSecurityOperator}, Permissions: []string{PermissionAssetCandidateView}},
			candidateView: true, candidateFollowUp: false,
		},
		{
			name: "organization role cannot obtain group scope from permission alone",
			principal: Principal{Roles: []string{"organization_security"}, Permissions: []string{
				PermissionSecurityTest, PermissionSecurityFinding, PermissionSecurityWorkOrder,
			}},
			test: false, finding: false, order: false, view: false, execute: false,
		},
		{
			name:      "scoped organization executor",
			principal: Principal{Roles: []string{"organization_admin"}, Permissions: []string{PermissionSecurityWorkOrderView, PermissionSecurityWorkOrderExecute}},
			view:      true, execute: true,
		},
		{name: "ordinary organization user", principal: Principal{Roles: []string{"organization_editor"}}, view: false, execute: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.principal.CanManageSecurityTests(); got != tt.test {
				t.Fatalf("CanManageSecurityTests() = %v, want %v", got, tt.test)
			}
			if got := tt.principal.CanManageSecurityFindings(); got != tt.finding {
				t.Fatalf("CanManageSecurityFindings() = %v, want %v", got, tt.finding)
			}
			if got := tt.principal.CanManageSecurityWorkOrders(); got != tt.order {
				t.Fatalf("CanManageSecurityWorkOrders() = %v, want %v", got, tt.order)
			}
			if got := tt.principal.CanViewSecurityWorkOrders(); got != tt.view {
				t.Fatalf("CanViewSecurityWorkOrders() = %v, want %v", got, tt.view)
			}
			if got := tt.principal.CanExecuteSecurityWorkOrders(); got != tt.execute {
				t.Fatalf("CanExecuteSecurityWorkOrders() = %v, want %v", got, tt.execute)
			}
			if got := tt.principal.CanViewAssetCandidates(); got != tt.candidateView {
				t.Fatalf("CanViewAssetCandidates() = %v, want %v", got, tt.candidateView)
			}
			if got := tt.principal.CanFollowUpAssetCandidates(); got != tt.candidateFollowUp {
				t.Fatalf("CanFollowUpAssetCandidates() = %v, want %v", got, tt.candidateFollowUp)
			}
		})
	}
}
