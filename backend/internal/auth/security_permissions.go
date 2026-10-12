package auth

const (
	RoleGroupAdmin       = "group_admin"
	RoleSecurityOperator = "security_operator"

	PermissionSecurityTest             = "security_test.manage"
	PermissionSecurityTestRequest      = "security_test.request"
	PermissionSecurityFinding          = "security_finding.manage"
	PermissionSecurityWorkOrder        = "security_workorder.manage"
	PermissionSecurityWorkOrderView    = "security_workorder.view"
	PermissionSecurityWorkOrderExecute = "security_workorder.execute"
	PermissionSecurityWorkOrderAssign  = "security_workorder.assign"
	PermissionSecurityWorkOrderReview  = "security_workorder.review"
	PermissionSecurityWorkOrderClose   = "security_workorder.close"
	PermissionAssetCandidateView       = "asset_candidate.view"
	PermissionAssetCandidateFollowUp   = "asset_candidate.follow_up"
)

// CanViewSecurityWorkOrders reports whether the principal has a global security
// work-order role or a scoped read permission. Callers must still check the
// organization scope for scoped permissions.
func (p Principal) CanViewSecurityWorkOrders() bool {
	return p.CanManageSecurityWorkOrders() || p.HasPermission(PermissionSecurityWorkOrderView) || p.HasPermission(PermissionSecurityWorkOrderExecute)
}

// CanExecuteSecurityWorkOrders reports whether the principal may perform the
// legacy organization-level execution actions. The organization scope is
// checked separately by the service layer.
func (p Principal) CanExecuteSecurityWorkOrders() bool {
	return p.CanManageSecurityWorkOrders() || p.HasPermission(PermissionSecurityWorkOrderExecute)
}

func (p Principal) CanAssignSecurityWorkOrders() bool {
	return p.CanManageSecurityWorkOrders() || p.HasPermission(PermissionSecurityWorkOrderAssign)
}

func (p Principal) CanReviewSecurityWorkOrders() bool {
	return p.CanManageSecurityWorkOrders() || p.HasPermission(PermissionSecurityWorkOrderReview)
}

func (p Principal) CanCloseSecurityWorkOrders() bool {
	return p.CanManageSecurityWorkOrders() || p.HasPermission(PermissionSecurityWorkOrderClose)
}

// CanViewAssetCandidates reports the group security role capability to inspect
// the cross-organization candidate queue. It deliberately does not grant
// asset.manage or any candidate settlement capability.
func (p Principal) CanViewAssetCandidates() bool {
	return p.HasRole(RoleGroupAdmin) || (p.HasRole(RoleSecurityOperator) && p.HasPermission(PermissionAssetCandidateView))
}

// CanFollowUpAssetCandidates reports whether the principal may add follow-up
// information to candidate collaboration records. The service still checks the
// organization scope and the concrete action.
func (p Principal) CanFollowUpAssetCandidates() bool {
	return p.HasRole(RoleGroupAdmin) || (p.HasRole(RoleSecurityOperator) && p.HasPermission(PermissionAssetCandidateFollowUp))
}

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
