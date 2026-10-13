// Package findingstatus is the single source of truth for security finding
// status semantics. Dashboard breakdowns, notification labels, work order
// validation and remediation completion rules all read from here so that the
// same value cannot mean "closed" in one place and "open" in another.
package findingstatus

import "strings"

// Status values stored in security_findings.status.
const (
	Pending       = "pending"
	InProgress    = "in_progress"
	Confirmed     = "confirmed"
	Resolved      = "resolved"
	Fixed         = "fixed"
	FalsePositive = "false_positive"
	Ignored       = "ignored"
	Duplicate     = "duplicate"
	RiskAccepted  = "risk_accepted"
)

// Asset-link and remediation values are deliberately kept separate from the
// vulnerability status above.  A finding can be confirmed while it is still
// waiting for an asset to be identified, and it can be fixed while the asset
// itself is retired; collapsing either dimension back into status makes those
// cases impossible to explain.
const (
	AssetUnlinked = "unlinked"
	AssetPending  = "pending"
	AssetLive     = "live"
	AssetRetired  = "retired"

	RemediationNotStarted            = "not_started"
	RemediationPendingAcceptance     = "pending_acceptance"
	RemediationInProgress            = "in_progress"
	RemediationPendingOrgReview      = "pending_org_review"
	RemediationPendingSecurityReview = "pending_security_review"
	RemediationCompleted             = "completed"
)

var transitionTable = map[string]map[string]struct{}{
	Pending: {
		InProgress: {}, Confirmed: {}, FalsePositive: {}, Ignored: {}, Duplicate: {},
	},
	InProgress: {
		Pending: {}, Confirmed: {}, Resolved: {},
	},
	Confirmed: {
		InProgress: {}, Resolved: {}, FalsePositive: {}, Ignored: {}, Duplicate: {},
	},
	Resolved: {
		InProgress: {}, Confirmed: {}, Fixed: {},
	},
	Fixed: {
		Confirmed: {}, InProgress: {},
	},
	FalsePositive: {
		Pending: {}, Confirmed: {},
	},
	Ignored: {
		Pending: {}, Confirmed: {},
	},
	Duplicate: {
		Pending: {}, Confirmed: {},
	},
	// Risk acceptance is entered and left by the dedicated risk workflow.  It
	// intentionally has no direct generic transition.
	RiskAccepted: {},
}

// Definition carries the persisted value together with its display label and
// the order used by status breakdowns.
type Definition struct {
	Value     string
	Label     string
	SortOrder int
}

// Definitions lists every status in presentation order.
func Definitions() []Definition {
	return []Definition{
		{Pending, "待处理", 1},
		{InProgress, "处理中", 2},
		{Confirmed, "已确认", 3},
		{Resolved, "已处理", 4},
		{Fixed, "已修复", 5},
		{FalsePositive, "误报", 6},
		{Ignored, "忽略", 7},
		{Duplicate, "重复", 8},
		{RiskAccepted, "风险接受", 9},
	}
}

// All returns every known status value.
func All() []string {
	definitions := Definitions()
	values := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		values = append(values, definition.Value)
	}
	return values
}

// Valid reports whether value is a known finding status.
func Valid(value string) bool {
	for _, candidate := range All() {
		if candidate == value {
			return true
		}
	}
	return false
}

// CanTransition reports whether a generic finding edit may move a finding
// between two vulnerability states.  Dedicated actions (risk acceptance,
// risk revocation/expiry and duplicate linking) must still perform their own
// evidence and permission checks; this function is not a substitute for
// those checks.
func CanTransition(from, to string) bool {
	if !Valid(from) || !Valid(to) {
		return false
	}
	if from == to {
		return true
	}
	_, ok := transitionTable[from][to]
	return ok
}

// CanSetDirectly reports whether a generic create/import operation may start
// with a status.  The two evidence-bearing states must be entered through a
// dedicated workflow or an explicit import mapping.
func CanSetDirectly(value string) bool {
	return Valid(value) && value != RiskAccepted && value != Duplicate
}

// Label returns the Chinese label for a status, falling back to the raw value
// so an unexpected stored value still renders instead of disappearing.
func Label(value string) string {
	for _, definition := range Definitions() {
		if definition.Value == value {
			return definition.Label
		}
	}
	return value
}

// closedStatuses are the statuses that mean "this finding no longer needs
// remediation work". resolved/ignored/duplicate are closed even though they
// were previously excluded from this set, which made dashboards report a
// finding as closed while its work order could never be completed.
func closedStatuses() []string {
	return []string{Resolved, Fixed, FalsePositive, Ignored, Duplicate, RiskAccepted}
}

// Closed reports whether a finding in this status is finished.
func Closed(value string) bool {
	for _, candidate := range closedStatuses() {
		if candidate == value {
			return true
		}
	}
	return false
}

// Open reports the inverse of Closed.
func Open(value string) bool {
	return Valid(value) && !Closed(value)
}

// ClosedSQL renders an `IN (...)` list for the closed statuses, for embedding
// directly in a query.
func ClosedSQL() string {
	quoted := make([]string, 0, len(closedStatuses()))
	for _, candidate := range closedStatuses() {
		quoted = append(quoted, "'"+candidate+"'")
	}
	return strings.Join(quoted, ", ")
}
