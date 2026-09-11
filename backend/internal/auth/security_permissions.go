package auth

const (
	RoleGroupAdmin       = "group_admin"
	RoleSecurityOperator = "security_operator"

	PermissionSecurityTest        = "security_test.manage"
	PermissionSecurityTestRequest = "security_test.request"
	PermissionSecurityFinding     = "security_finding.manage"
	PermissionSecurityWorkOrder   = "security_workorder.manage"
)

// HasGroupSecurityRole limits group-wide security data to built-in group roles.
// Organization-scoped custom roles may carry similarly named permissions, but
// must never gain access to all subsidiaries' security data.
func (p Principal) HasGroupSecurityRole() bool {
	return p.HasRole(RoleGroupAdmin) || p.HasRole(RoleSecurityOperator)
}

func (p Principal) CanManageSecurityTests() bool {
	return p.HasGroupSecurityRole() && p.HasPermission(PermissionSecurityTest)
}

func (p Principal) CanManageSecurityFindings() bool {
	return p.HasGroupSecurityRole() && p.HasPermission(PermissionSecurityFinding)
}

func (p Principal) CanManageSecurityWorkOrders() bool {
	return p.HasGroupSecurityRole() && p.HasPermission(PermissionSecurityWorkOrder)
}

func (p Principal) CanViewGroupSecurityDashboard() bool {
	return p.CanManageSecurityTests() || p.CanManageSecurityFindings() || p.CanManageSecurityWorkOrders()
}
