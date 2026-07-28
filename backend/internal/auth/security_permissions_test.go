package auth

import "testing"

func TestGroupSecurityPermissionBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		principal Principal
		test      bool
		finding   bool
		order     bool
	}{
		{
			name: "group administrator with permissions",
			principal: Principal{Roles: []string{RoleGroupAdmin}, Permissions: []string{
				PermissionSecurityTest, PermissionSecurityFinding, PermissionSecurityWorkOrder,
			}},
			test: true, finding: true, order: true,
		},
		{
			name: "security operator with all security permissions",
			principal: Principal{Roles: []string{RoleSecurityOperator}, Permissions: []string{
				PermissionSecurityTest, PermissionSecurityFinding, PermissionSecurityWorkOrder,
			}},
			test: true, finding: true, order: true,
		},
		{
			name: "security operator permissions remain independent",
			principal: Principal{Roles: []string{RoleSecurityOperator}, Permissions: []string{
				PermissionSecurityFinding,
			}},
			test: false, finding: true, order: false,
		},
		{
			name: "organization role cannot obtain group scope from permission alone",
			principal: Principal{Roles: []string{"organization_security"}, Permissions: []string{
				PermissionSecurityTest, PermissionSecurityFinding, PermissionSecurityWorkOrder,
			}},
			test: false, finding: false, order: false,
		},
		{name: "ordinary organization user", principal: Principal{Roles: []string{"organization_editor"}}},
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
		})
	}
}
